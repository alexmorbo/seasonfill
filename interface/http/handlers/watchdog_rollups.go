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

// QbitProbe is the on-demand qBT reachability check. Implementations
// return true when qBT responded to a lightweight health call within
// the supplied ctx deadline. Story 090 added this so the rollup
// handler can fill QbitReachable before the regrab loop's first poll.
type QbitProbe interface {
	Probe(ctx context.Context, s regrab.Settings) (bool, error)
}

// probeCacheEntry is one cached probe result. expiresAt is wall-clock.
type probeCacheEntry struct {
	reachable bool
	expiresAt time.Time
}

// WatchdogRollupHandler serves the two watchdog rollup endpoints.
type WatchdogRollupHandler struct {
	settings       regrab.SettingsLookup
	snapshots      RollupSnapshotProvider
	grabs          rollupGrabCounter
	blacklist      rollupBlacklistCounter
	instances      InstanceLister
	instanceLookup InstanceIDLookup
	probe          QbitProbe
	logger         *slog.Logger
	now            func() time.Time

	probeTimeout time.Duration
	probeMu      sync.Mutex
	probeCache   map[string]probeCacheEntry
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
		now:          func() time.Time { return time.Now().UTC() },
		probeTimeout: 3 * time.Second,
		probeCache:   make(map[string]probeCacheEntry),
	}
}

// WithClock pins the time source — tests only.
func (h *WatchdogRollupHandler) WithClock(f func() time.Time) *WatchdogRollupHandler {
	if f != nil {
		h.now = f
	}
	return h
}

// WithQbitProbe wires the on-demand reachability probe. Story 090.
// nil disables on-demand probing (the handler reverts to snapshot-only
// behaviour). Tests use this to inject a stub; production wires the
// real qBT-backed adapter via cmd/server.
func (h *WatchdogRollupHandler) WithQbitProbe(p QbitProbe) *WatchdogRollupHandler {
	h.probe = p
	return h
}

// WithProbeTimeout overrides the per-probe ctx deadline. Defaults to
// 3s; tests use a smaller value to keep the suite fast.
func (h *WatchdogRollupHandler) WithProbeTimeout(d time.Duration) *WatchdogRollupHandler {
	if d > 0 {
		h.probeTimeout = d
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

	st, snapOK := h.snapshots.Snapshot(name)
	if snapOK {
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

	// Story 090: fill QbitReachable on demand when the regrab loop hasn't
	// stamped a fresh snapshot yet. Without this, every pod restart shows
	// "недоступен" until the first 30-minute poll cycle completes — even
	// when qBT is healthy.
	if sett.Enabled && h.probe != nil && h.shouldProbe(now, st, snapOK, sett) {
		reachable := h.probeWithCache(ctx, name, sett, now)
		row.QbitReachable = reachable
	}

	row.Active = row.Enabled && row.QbitReachable
	return row, nil
}

// shouldProbe returns true when an on-demand qBT reachability probe is
// useful. We probe when:
//   - no snapshot exists yet (post-restart cold cache),
//   - OR the snapshot's LastPollAt is older than the poll interval
//     (the regrab loop is configured to poll at PollInterval; a stale
//     snapshot means the loop hasn't run yet or is wedged),
//   - OR the snapshot says unreachable BUT is older than 60s, so we
//     give qBT a chance to recover without waiting for the next poll.
func (h *WatchdogRollupHandler) shouldProbe(now time.Time, st regrab.RuntimeState, snapOK bool, sett regrab.Settings) bool {
	if !snapOK || st.LastPollAt.IsZero() {
		return true
	}
	age := now.Sub(st.LastPollAt)
	if sett.PollInterval > 0 && age > sett.PollInterval {
		return true
	}
	if !st.QbitReachable && age > 60*time.Second {
		return true
	}
	return false
}

// probeWithCache runs the probe with a short ctx deadline. Results are
// cached for min(15s, PollInterval/2) so the rollup UI's 30s refetch
// cadence does not hammer qBT. Returns false on probe error.
func (h *WatchdogRollupHandler) probeWithCache(ctx context.Context, name string, sett regrab.Settings, now time.Time) bool {
	h.probeMu.Lock()
	if entry, ok := h.probeCache[name]; ok && entry.expiresAt.After(now) {
		h.probeMu.Unlock()
		return entry.reachable
	}
	h.probeMu.Unlock()

	probeCtx, cancel := context.WithTimeout(ctx, h.probeTimeout)
	defer cancel()
	reachable, err := h.probe.Probe(probeCtx, sett)
	if err != nil {
		h.logger.DebugContext(ctx, "watchdog_rollup_probe_failed",
			slog.String("instance", name),
			slog.String("error", err.Error()))
	}

	ttl := 15 * time.Second
	if sett.PollInterval > 0 && sett.PollInterval/2 < ttl {
		ttl = sett.PollInterval / 2
	}
	h.probeMu.Lock()
	h.probeCache[name] = probeCacheEntry{
		reachable: reachable,
		expiresAt: now.Add(ttl),
	}
	h.probeMu.Unlock()
	return reachable
}
