package rest

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"golang.org/x/sync/errgroup"

	catalogrest "github.com/alexmorbo/seasonfill/internal/catalog/rest"
	"github.com/alexmorbo/seasonfill/internal/shared/clients/qbit"
	ports "github.com/alexmorbo/seasonfill/internal/shared/dataports"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
	"github.com/alexmorbo/seasonfill/internal/shared/http/dto"
	"github.com/alexmorbo/seasonfill/internal/shared/http/handlers"
	"github.com/alexmorbo/seasonfill/internal/watchdog/app/regrab"
)

// RollupSnapshotProvider is the read-only slice of *regrab.UseCase the
// rollup handler uses.
type RollupSnapshotProvider interface {
	Snapshot(instance domain.InstanceName) (regrab.RuntimeState, bool)
	SnapshotAll() map[domain.InstanceName]regrab.RuntimeState
}

// InstanceIDLookup maps a name to its DB id.
type InstanceIDLookup interface {
	IDByName(ctx context.Context, name string) (uint, bool, error)
}

// rollupGrabCounter is the narrowed slice of GrabRepository the handler
// needs. *grabpersistence.GrabRepository satisfies it via the methods added
// in this story.
type rollupGrabCounter interface {
	CountReplaysSince(ctx context.Context, instance domain.InstanceName, since time.Time) (int, error)
	CountReplaysAll(ctx context.Context, instance domain.InstanceName) (int, error)
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

// QbitTorrentsLister is the on-demand qBT torrents-list call used by
// the rollup handler to compute Watched and Unregistered without
// waiting for the regrab loop's next 30-minute poll cycle. Story 094.
// Implementations apply Settings.Category as a server-side filter when
// non-empty.
type QbitTorrentsLister interface {
	ListTorrents(ctx context.Context, s regrab.Settings) ([]qbit.Torrent, error)
}

// probeCacheEntry is one cached probe result. expiresAt is wall-clock.
type probeCacheEntry struct {
	reachable bool
	expiresAt time.Time
}

// torrentsCacheEntry is one cached torrents-list snapshot. The watched
// and unregistered values are pre-computed at insertion so the hot path
// avoids re-scanning Tags strings on every cache hit.
type torrentsCacheEntry struct {
	watched      int
	unregistered int
	expiresAt    time.Time
	ok           bool // false when the underlying call failed; fallback path will use snapshot
}

// unregisteredTagMarkers are the case-insensitive substrings the rollup
// handler scans for in qBit's per-torrent Tags string to count
// unregistered torrents on demand. qbit_manage (the de-facto
// companion utility) tags affected torrents with "issue" by default;
// the other two markers cover community variants. Matching is
// substring-based + case-insensitive so "Issue", "tracker_error",
// "unregistered torrent" all hit.
var unregisteredTagMarkers = []string{"issue", "unregistered", "tracker_error"}

// WatchdogRollupHandler serves the two watchdog rollup endpoints.
type WatchdogRollupHandler struct {
	settings       regrab.SettingsLookup
	snapshots      RollupSnapshotProvider
	grabs          rollupGrabCounter
	blacklist      rollupBlacklistCounter
	instances      catalogrest.InstanceLister
	instanceLookup InstanceIDLookup
	probe          QbitProbe
	lister         QbitTorrentsLister
	logger         *slog.Logger
	now            func() time.Time

	probeTimeout    time.Duration
	probeMu         sync.Mutex
	probeCache      map[string]probeCacheEntry
	torrentsTimeout time.Duration
	torrentsMu      sync.Mutex
	torrentsCache   map[string]torrentsCacheEntry
}

func NewWatchdogRollupHandler(
	settings regrab.SettingsLookup,
	snapshots RollupSnapshotProvider,
	grabs rollupGrabCounter,
	blacklist rollupBlacklistCounter,
	instances catalogrest.InstanceLister,
	instanceLookup InstanceIDLookup,
	logger *slog.Logger,
) *WatchdogRollupHandler {
	if logger == nil {
		logger = slog.Default()
	}
	return &WatchdogRollupHandler{
		settings: settings, snapshots: snapshots, grabs: grabs, blacklist: blacklist,
		instances: instances, instanceLookup: instanceLookup, logger: logger,
		now:             func() time.Time { return time.Now().UTC() },
		probeTimeout:    3 * time.Second,
		probeCache:      make(map[string]probeCacheEntry),
		torrentsTimeout: 3 * time.Second,
		torrentsCache:   make(map[string]torrentsCacheEntry),
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

// WithQbitTorrentsLister wires the on-demand watched/unregistered
// counter source. Story 094. nil disables on-demand counting (the
// handler reverts to snapshot-only behaviour for Watched and leaves
// Unregistered at zero — matching the pre-094 cold-start UX). Tests
// inject a stub; production wires the real qBT-backed adapter via
// cmd/server.
func (h *WatchdogRollupHandler) WithQbitTorrentsLister(l QbitTorrentsLister) *WatchdogRollupHandler {
	h.lister = l
	return h
}

// WithTorrentsTimeout overrides the per-list ctx deadline. Defaults to
// 3s — the same ceiling Story 090 picked for the reachability probe;
// listing torrents on a healthy qBit is comparable in cost to /version
// because qBit's list endpoint is in-memory.
func (h *WatchdogRollupHandler) WithTorrentsTimeout(d time.Duration) *WatchdogRollupHandler {
	if d > 0 {
		h.torrentsTimeout = d
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
		handlers.WriteInternalError(c, h.logger, "watchdog_rollups_lookup_failed", err,
			slog.String("instance", name))
		return
	}
	if !ok {
		handlers.WriteError(c, http.StatusNotFound, "unknown instance: "+name)
		return
	}
	row, err := h.buildOne(ctx, name, id)
	if err != nil {
		handlers.WriteInternalError(c, h.logger, "watchdog_rollups_build_failed", err,
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
		handlers.WriteInternalError(c, h.logger, "watchdog_rollups_list_failed", err)
		return
	}
	rows := make([]dto.WatchdogRollup, len(names))
	var mu sync.Mutex
	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(5)

	for i, name := range names {
		g.Go(func() error {
			id, ok, err := h.instanceLookup.IDByName(gctx, name)
			if err != nil {
				return err
			}
			if !ok {
				mu.Lock()
				rows[i] = dto.WatchdogRollup{InstanceName: domain.InstanceName(name)}
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
		handlers.WriteInternalError(c, h.logger, "watchdog_rollups_fan_out_failed", err)
		return
	}
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].InstanceName < rows[j].InstanceName })
	c.JSON(http.StatusOK, dto.WatchdogRollupList{Items: rows})
}

func (h *WatchdogRollupHandler) buildOne(ctx context.Context, name string, instanceID uint) (dto.WatchdogRollup, error) {
	instName := domain.InstanceName(name)
	row := dto.WatchdogRollup{InstanceName: instName}
	sett, err := h.settings.Lookup(ctx, instName)
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
	var r24, r7, blist int
	cg.Go(func() error {
		v, e := h.grabs.CountReplaysSince(cctx, instName, now.Add(-24*time.Hour))
		if e == nil {
			r24 = v
		}
		return e
	})
	cg.Go(func() error {
		v, e := h.grabs.CountReplaysSince(cctx, instName, now.Add(-7*24*time.Hour))
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
	row.Regrabs24h = r24
	row.Regrabs7d = r7
	row.BlacklistSize = blist

	st, snapOK := h.snapshots.Snapshot(instName)
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

	// Story 094: compute Watched and Unregistered on demand whenever
	// the snapshot is missing or stale. Same recovery posture as the
	// 090 probe — best effort, fall back to snapshot on failure so a
	// transient qBT blip never zeros the UI gauges. Skipped when the
	// instance is disabled (no work to display) or qBT is known
	// unreachable (the list call would just time out).
	if sett.Enabled && h.lister != nil && row.QbitReachable && h.shouldListTorrents(now, st, snapOK, sett) {
		watched, unreg, ok := h.listTorrentsWithCache(ctx, name, sett, now)
		if ok {
			row.Watched = watched
			row.Unregistered = unreg
		}
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

// shouldListTorrents mirrors shouldProbe but for the watched +
// unregistered on-demand counters added by Story 094. The qBT torrent
// list is materially more expensive than /version, so the trigger set
// is conservative: only list when the snapshot is missing entirely,
// when LastPollAt is older than the configured poll interval (the
// regrab loop hasn't caught up), or — once a fresh list is cached —
// when the cache itself has expired.
func (h *WatchdogRollupHandler) shouldListTorrents(now time.Time, st regrab.RuntimeState, snapOK bool, sett regrab.Settings) bool {
	if !snapOK || st.LastPollAt.IsZero() {
		return true
	}
	age := now.Sub(st.LastPollAt)
	if sett.PollInterval > 0 && age > sett.PollInterval {
		return true
	}
	return false
}

// listTorrentsWithCache runs the qBT list call with a short ctx
// deadline and caches the post-processing result for min(15s,
// PollInterval/2). Returns ok=false when the underlying call failed
// AND no cached entry exists; the caller treats !ok as "preserve the
// snapshot value and don't zero the UI".
func (h *WatchdogRollupHandler) listTorrentsWithCache(ctx context.Context, name string, sett regrab.Settings, now time.Time) (int, int, bool) {
	h.torrentsMu.Lock()
	if entry, ok := h.torrentsCache[name]; ok && entry.expiresAt.After(now) {
		h.torrentsMu.Unlock()
		return entry.watched, entry.unregistered, entry.ok
	}
	h.torrentsMu.Unlock()

	listCtx, cancel := context.WithTimeout(ctx, h.torrentsTimeout)
	defer cancel()
	torrents, err := h.lister.ListTorrents(listCtx, sett)
	if err != nil {
		h.logger.DebugContext(ctx, "watchdog_rollup_list_torrents_failed",
			slog.String("instance", name),
			slog.String("error", err.Error()))
		// Negative cache the failure briefly so a thundering herd of
		// rollup requests doesn't hammer a sick qBT — but keep the TTL
		// short so recovery is fast.
		h.torrentsMu.Lock()
		h.torrentsCache[name] = torrentsCacheEntry{ok: false, expiresAt: now.Add(5 * time.Second)}
		h.torrentsMu.Unlock()
		return 0, 0, false
	}

	watched, unreg := countTorrents(torrents, sett.Category)

	ttl := 15 * time.Second
	if sett.PollInterval > 0 && sett.PollInterval/2 < ttl {
		ttl = sett.PollInterval / 2
	}
	h.torrentsMu.Lock()
	h.torrentsCache[name] = torrentsCacheEntry{
		watched:      watched,
		unregistered: unreg,
		expiresAt:    now.Add(ttl),
		ok:           true,
	}
	h.torrentsMu.Unlock()
	return watched, unreg, true
}

// countTorrents derives the two counters from one torrent list.
// Watched mirrors the regrab use case's post-category-filter count
// (`application/regrab/regrab_usecase.go` line ~230). Unregistered is
// a qbit_manage-style heuristic: torrents whose Tags string contains
// one of unregisteredTagMarkers (case-insensitive substring). Empty
// category means the qBT server-side filter already applied — every
// returned torrent counts as watched.
func countTorrents(torrents []qbit.Torrent, category string) (int, int) {
	watched := 0
	unreg := 0
	for _, t := range torrents {
		if t.Category != "" && category != "" && t.Category != category {
			continue
		}
		watched++
		if hasUnregisteredTag(t.Tags) {
			unreg++
		}
	}
	return watched, unreg
}

// hasUnregisteredTag reports whether the qBit tags string contains any
// of the unregisteredTagMarkers, case-insensitive substring match. We
// scan the raw comma-separated string instead of tokenising it because
// the markers are themselves substrings ("issue" inside "issue_tracker"
// still counts, which is the operator's intent).
func hasUnregisteredTag(tags string) bool {
	if tags == "" {
		return false
	}
	lower := strings.ToLower(tags)
	for _, m := range unregisteredTagMarkers {
		if strings.Contains(lower, m) {
			return true
		}
	}
	return false
}
