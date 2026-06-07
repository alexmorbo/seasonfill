package handlers

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"sort"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"golang.org/x/sync/errgroup"

	"github.com/alexmorbo/seasonfill/application/ports"
	"github.com/alexmorbo/seasonfill/application/regrab"
	"github.com/alexmorbo/seasonfill/interface/http/dto"
)

// RollupSnapshotProvider is the read-only slice of *regrab.UseCase the
// rollup handler uses.
type RollupSnapshotProvider interface {
	Snapshot(instance string) (regrab.RuntimeState, bool)
	SnapshotAll() map[string]regrab.RuntimeState
}

// InstanceLister returns every configured Sonarr instance name.
type InstanceLister interface {
	ListNames(ctx context.Context) ([]string, error)
}

// InstanceIDLookup maps a name to its DB id.
type InstanceIDLookup interface {
	IDByName(ctx context.Context, name string) (uint, bool, error)
}

// rollupGrabCounter is the narrowed slice of GrabRepository the handler
// needs. *repositories.GrabRepository satisfies it via the methods added
// in this story.
type rollupGrabCounter interface {
	CountReplaysSince(ctx context.Context, instance string, since time.Time) (int, error)
	CountReplaysAll(ctx context.Context, instance string) (int, error)
}

// rollupBlacklistCounter is the narrowed slice of
// WatchdogBlacklistRepository the handler needs.
type rollupBlacklistCounter interface {
	CountByInstance(ctx context.Context, id uint) (int, error)
}

// WatchdogRollupHandler serves the two watchdog rollup endpoints.
type WatchdogRollupHandler struct {
	settings       regrab.SettingsLookup
	snapshots      RollupSnapshotProvider
	grabs          rollupGrabCounter
	blacklist      rollupBlacklistCounter
	instances      InstanceLister
	instanceLookup InstanceIDLookup
	logger         *slog.Logger
	now            func() time.Time
}

func NewWatchdogRollupHandler(
	settings regrab.SettingsLookup,
	snapshots RollupSnapshotProvider,
	grabs rollupGrabCounter,
	blacklist rollupBlacklistCounter,
	instances InstanceLister,
	instanceLookup InstanceIDLookup,
	logger *slog.Logger,
) *WatchdogRollupHandler {
	if logger == nil {
		logger = slog.Default()
	}
	return &WatchdogRollupHandler{
		settings: settings, snapshots: snapshots, grabs: grabs, blacklist: blacklist,
		instances: instances, instanceLookup: instanceLookup, logger: logger,
		now: func() time.Time { return time.Now().UTC() },
	}
}

// WithClock pins the time source — tests only.
func (h *WatchdogRollupHandler) WithClock(f func() time.Time) *WatchdogRollupHandler {
	if f != nil {
		h.now = f
	}
	return h
}

// One handles GET /api/v1/instances/:name/watchdog/rollups.
//
// @Summary     Watchdog rollups for one instance
// @Tags        watchdog
// @Produce     json
// @Param       name  path  string  true  "Instance name"
// @Success     200   {object}  dto.WatchdogRollup
// @Failure     404   {object}  dto.ErrorResponse
// @Failure     500   {object}  dto.ErrorResponse
// @Security    CookieAuth
// @Security    ApiKeyAuth
// @Router      /instances/{name}/watchdog/rollups [get]
func (h *WatchdogRollupHandler) One(c *gin.Context) {
	name := c.Param("name")
	ctx := c.Request.Context()

	id, ok, err := h.instanceLookup.IDByName(ctx, name)
	if err != nil {
		writeInternalError(c, h.logger, "watchdog_rollups_lookup_failed", err,
			slog.String("instance", name))
		return
	}
	if !ok {
		writeError(c, http.StatusNotFound, "unknown instance: "+name)
		return
	}
	row, err := h.buildOne(ctx, name, id)
	if err != nil {
		writeInternalError(c, h.logger, "watchdog_rollups_build_failed", err,
			slog.String("instance", name))
		return
	}
	c.JSON(http.StatusOK, row)
}

// All handles GET /api/v1/watchdog/rollups.
//
// @Summary     Watchdog rollups for every configured instance
// @Tags        watchdog
// @Produce     json
// @Success     200  {object}  dto.WatchdogRollupList
// @Failure     500  {object}  dto.ErrorResponse
// @Security    CookieAuth
// @Security    ApiKeyAuth
// @Router      /watchdog/rollups [get]
func (h *WatchdogRollupHandler) All(c *gin.Context) {
	ctx := c.Request.Context()
	names, err := h.instances.ListNames(ctx)
	if err != nil {
		writeInternalError(c, h.logger, "watchdog_rollups_list_failed", err)
		return
	}
	rows := make([]dto.WatchdogRollup, len(names))
	var mu sync.Mutex
	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(5)

	for i, name := range names {
		i, name := i, name
		g.Go(func() error {
			id, ok, err := h.instanceLookup.IDByName(gctx, name)
			if err != nil {
				return err
			}
			if !ok {
				mu.Lock()
				rows[i] = dto.WatchdogRollup{InstanceName: name}
				mu.Unlock()
				return nil
			}
			row, err := h.buildOne(gctx, name, id)
			if err != nil {
				return err
			}
			mu.Lock()
			rows[i] = row
			mu.Unlock()
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		writeInternalError(c, h.logger, "watchdog_rollups_fan_out_failed", err)
		return
	}
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].InstanceName < rows[j].InstanceName })
	c.JSON(http.StatusOK, dto.WatchdogRollupList{Items: rows})
}

func (h *WatchdogRollupHandler) buildOne(ctx context.Context, name string, instanceID uint) (dto.WatchdogRollup, error) {
	row := dto.WatchdogRollup{InstanceName: name}
	sett, err := h.settings.Lookup(ctx, name)
	if err != nil && !errors.Is(err, ports.ErrNotFound) {
		return row, err
	}
	if errors.Is(err, ports.ErrNotFound) {
		return row, nil
	}
	row.Enabled = sett.Enabled
	row.PollIntervalSec = int(sett.PollInterval.Seconds())
	row.CooldownHours = int(sett.RegrabCooldown.Hours())
	row.NoBetterMax = sett.MaxConsecutiveNoBetter

	now := h.now()
	cg, cctx := errgroup.WithContext(ctx)
	var unreg, r24, r7, blist int
	cg.Go(func() error {
		v, e := h.grabs.CountReplaysAll(cctx, name)
		if e == nil {
			unreg = v
		}
		return e
	})
	cg.Go(func() error {
		v, e := h.grabs.CountReplaysSince(cctx, name, now.Add(-24*time.Hour))
		if e == nil {
			r24 = v
		}
		return e
	})
	cg.Go(func() error {
		v, e := h.grabs.CountReplaysSince(cctx, name, now.Add(-7*24*time.Hour))
		if e == nil {
			r7 = v
		}
		return e
	})
	cg.Go(func() error {
		v, e := h.blacklist.CountByInstance(cctx, instanceID)
		if e == nil {
			blist = v
		}
		return e
	})
	if err := cg.Wait(); err != nil {
		return row, err
	}
	row.Unregistered = unreg
	row.Regrabs24h = r24
	row.Regrabs7d = r7
	row.BlacklistSize = blist

	if st, ok := h.snapshots.Snapshot(name); ok {
		t := st.LastPollAt
		row.LastPollAt = &t
		if st.LastPollResult != "" {
			r := st.LastPollResult
			row.LastPollResult = &r
		}
		row.QbitReachable = st.QbitReachable
		row.Watched = st.Watched
		if sett.Enabled && sett.PollInterval > 0 && !st.LastPollAt.IsZero() {
			next := st.LastPollAt.Add(sett.PollInterval)
			row.NextPollAt = &next
		}
	}
	row.Active = row.Enabled && row.QbitReachable
	return row, nil
}
