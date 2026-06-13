package webhook

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/alexmorbo/seasonfill/application/errtext"
	"github.com/alexmorbo/seasonfill/application/ports"
	"github.com/alexmorbo/seasonfill/application/scan"
	"github.com/alexmorbo/seasonfill/application/torrentsync"
	"github.com/alexmorbo/seasonfill/domain/cooldown"
	"github.com/alexmorbo/seasonfill/domain/grab"
	"github.com/alexmorbo/seasonfill/domain/series"
	"github.com/alexmorbo/seasonfill/domain/webhook"
	"github.com/alexmorbo/seasonfill/infrastructure/sonarr"
	"github.com/alexmorbo/seasonfill/internal/observability"
	"github.com/alexmorbo/seasonfill/internal/runtime"
)

// GuidCooldownLookup returns the per-instance guid-after-failed-import
// cooldown duration. Built by the wiring layer (cmd/server/main.go)
// from cfg.SonarrInstances. A return value of 0 disables the cooldown
// write for that instance — used both for explicit opt-out and for
// graceful degradation when a webhook fires from an instance not in
// our config.
type GuidCooldownLookup func(instance string) time.Duration

// SeriesSyncer is the E-1 (Story 210) hook: when non-nil it overrides
// the thin CacheEntry path on SeriesAdd with a full Sonarr API sync
// (PRD §5.5 sonarr_sync worker trigger — new series_cache from webhook).
type SeriesSyncer interface {
	SyncFromSonarrAPI(ctx context.Context, instanceName string, sonarrSeriesID int) error
}

// UseCase processes a Sonarr webhook event end-to-end: it looks up the
// matching grab_records row, transitions its status, and (on
// import_failed) adds a guid-scope cooldown. Status update and cooldown
// write are atomic via the injected Transactor.
type UseCase struct {
	grabs              ports.GrabRepository
	cooldowns          ports.CooldownRepository
	seriesCache        ports.SeriesCacheRepository
	tx                 ports.Transactor
	guidCooldownLookup GuidCooldownLookup
	logger             *slog.Logger
	now                func() time.Time
	sonarrClientFor    func(name string) (ports.SonarrClient, bool)
	instanceFor        func(name string) (runtime.InstanceSnapshot, bool)
	// seriesSyncer is the E-1 hook; when non-nil OnSeriesAdd runs the
	// full Sonarr API sync instead of the thin CacheEntry path. Nil-OK
	// (pre-E-1 wiring runs unchanged).
	seriesSyncer SeriesSyncer
	// episodeStates is the E-2 cascade hook. Nil-OK: when not supplied
	// the SeriesDelete cascade only soft-deletes the cache row (pre-E-2
	// behaviour preserved). Production wiring passes the live repo.
	episodeStates scan.EpisodeStatesSoftDeleter
	// torrentSeriesMap is the narrow port the reconciler also writes
	// through. Webhook captures invoke UpsertTx inside the same tx as
	// the grab_records.torrent_hash update so a rollback of either
	// rolls both back. Nil-OK — pre-Story-221 wiring runs unchanged
	// (the map row is then populated later by the reconciler's
	// grab_record source).
	torrentSeriesMap torrentsync.MapRepo
}

// Deps groups constructor parameters.
type Deps struct {
	Grabs              ports.GrabRepository
	Cooldowns          ports.CooldownRepository
	SeriesCache        ports.SeriesCacheRepository
	Tx                 ports.Transactor
	GUIDCooldownLookup GuidCooldownLookup
	Logger             *slog.Logger
	// SonarrClientFor returns the live Sonarr client for an instance.
	// Nil-OK: a nil lookup (or one returning ok=false) disables the
	// 044b parse-on-grab hook silently.
	SonarrClientFor func(name string) (ports.SonarrClient, bool)
	// InstanceFor returns the live snapshot for an instance — used to
	// read ParseOnGrabEnabled. Nil-OK with the same semantics.
	InstanceFor func(name string) (runtime.InstanceSnapshot, bool)
	// SeriesSyncer is the E-1 hook. Nil-OK; pre-E-1 thin CacheEntry
	// path runs unchanged when nil.
	SeriesSyncer SeriesSyncer
	// EpisodeStates is the E-2 SeriesDelete cascade hook. Nil-OK.
	EpisodeStates scan.EpisodeStatesSoftDeleter
	// TorrentSeriesMap is the torrentsync bridge port — webhook
	// grab captures invoke UpsertTx in the same tx as
	// UpdateTorrentHash so a rollback of either rolls both back.
	// Nil-OK: pre-Story-221 wiring runs unchanged.
	TorrentSeriesMap torrentsync.MapRepo
}

// New constructs a UseCase. Logger defaults to slog.Default().
// A nil GUIDCooldownLookup normalises to a closure returning 0 —
// same behaviour as the pre-008c cooldown-disabled path.
func New(d Deps) *UseCase {
	lg := d.Logger
	if lg == nil {
		lg = slog.Default()
	}
	lookup := d.GUIDCooldownLookup
	if lookup == nil {
		lookup = func(string) time.Duration { return 0 }
	}
	clientFor := d.SonarrClientFor
	if clientFor == nil {
		clientFor = func(string) (ports.SonarrClient, bool) { return nil, false }
	}
	instFor := d.InstanceFor
	if instFor == nil {
		instFor = func(string) (runtime.InstanceSnapshot, bool) { return runtime.InstanceSnapshot{}, false }
	}
	return &UseCase{
		grabs:              d.Grabs,
		cooldowns:          d.Cooldowns,
		seriesCache:        d.SeriesCache,
		tx:                 d.Tx,
		guidCooldownLookup: lookup,
		logger:             lg,
		now:                func() time.Time { return time.Now().UTC() },
		sonarrClientFor:    clientFor,
		instanceFor:        instFor,
		seriesSyncer:       d.SeriesSyncer,
		episodeStates:      d.EpisodeStates,
		torrentSeriesMap:   d.TorrentSeriesMap,
	}
}

// WithClock swaps the time source — tests-only.
func (u *UseCase) WithClock(f func() time.Time) *UseCase { u.now = f; return u }

// Process consumes a domain.Event and applies it. Returns nil on
// Unsupported/Grabbed events, orphan events, and already-terminal rows
// (idempotent re-delivery). Returns non-nil only on real downstream
// failures (DB unavailable, transactor error).
func (u *UseCase) Process(ctx context.Context, evt webhook.Event) error {
	switch evt.Type {
	case webhook.EventTypeUnsupported:
		u.logger.DebugContext(ctx, "webhook_event_no_op",
			slog.String("event_type", string(evt.Type)),
			slog.String("instance", evt.InstanceName),
			slog.String("raw_event_type", evt.RawEventType),
		)
		return nil
	case webhook.EventTypeGrabbed:
		return u.handleGrabbed(ctx, evt)
	case webhook.EventTypeSeriesAdd:
		return u.handleSeriesAdd(ctx, evt)
	case webhook.EventTypeSeriesDeleted:
		return u.handleSeriesDelete(ctx, evt)
	case webhook.EventTypeImported, webhook.EventTypeImportFailed:
		// fall through
	default:
		u.logger.WarnContext(ctx, "webhook_event_unknown_type",
			slog.String("event_type", string(evt.Type)),
			slog.String("instance", evt.InstanceName),
		)
		return nil
	}

	target := mapEventToStatus(evt.Type)

	key := ports.MatchKey{
		DownloadID:   evt.DownloadID,
		ReleaseTitle: evt.ReleaseTitle,
		SeriesID:     evt.SeriesID,
		SeasonNumber: evt.SeasonNumber,
		InstanceName: evt.InstanceName,
	}

	rec, err := u.grabs.MatchLatest(ctx, key)
	if err != nil {
		if errors.Is(err, ports.ErrNotFound) {
			u.logger.InfoContext(ctx, "webhook_orphan_event",
				slog.String("instance", evt.InstanceName),
				slog.String("event_type", string(evt.Type)),
				slog.String("download_id", evt.DownloadID),
				slog.String("release_title", evt.ReleaseTitle),
				slog.Int("series_id", evt.SeriesID),
				slog.Int("season", evt.SeasonNumber),
				slog.String("raw_event_type", evt.RawEventType),
			)
			return nil
		}
		// Wrap raw repo error with ErrDBUnavailable so 007c's
		// IsTransient classifier maps it to HTTP 500 (Sonarr retries).
		// ErrNotFound is handled above; everything else here is
		// driver-level (connection refused, query timeout, etc.).
		return fmt.Errorf("match grab record: %w: %w", ports.ErrDBUnavailable, err)
	}

	if !rec.Status.CanTransitionTo(target) {
		u.logger.DebugContext(ctx, "webhook_event_idempotent_skip",
			slog.String("instance", evt.InstanceName),
			slog.String("event_type", string(evt.Type)),
			slog.String("current_status", string(rec.Status)),
			slog.String("target_status", string(target)),
			slog.String("grab_id", rec.ID.String()),
			slog.String("raw_event_type", evt.RawEventType),
		)
		return nil
	}

	if target == grab.StatusImportFailed && u.guidCooldownLookup(evt.InstanceName) == 0 {
		u.logger.WarnContext(ctx, "webhook_unknown_instance_no_cooldown",
			slog.String("instance", evt.InstanceName),
			slog.String("event_type", string(evt.Type)),
			slog.String("grab_id", rec.ID.String()),
			slog.String("reason", "lookup_returned_zero_or_unconfigured"),
		)
	}

	work := func(txCtx context.Context) error {
		// F-P2-4: cap upstream message at 4 KiB before persistence.
		// Sonarr's DownloadStatusMessages are usually <200 bytes but a
		// pathological multi-tracker concatenation could grow unboundedly.
		message := errtext.Clamp(evt.Message)
		if err := u.grabs.UpdateStatus(txCtx, rec.ID, target, message); err != nil {
			return fmt.Errorf("update status: %w", err)
		}
		if target == grab.StatusImportFailed {
			if dur := u.guidCooldownLookup(evt.InstanceName); dur > 0 {
				cd := cooldown.Cooldown{
					Scope:     cooldown.ScopeGUID,
					Key:       cooldown.GUIDKey(rec.ReleaseGUID),
					ExpiresAt: evt.OccurredAt.Add(dur),
					Reason:    "guid_after_failed_import",
					CreatedAt: evt.OccurredAt.UTC(),
				}
				if err := u.cooldowns.Set(txCtx, cd); err != nil {
					return fmt.Errorf("set guid cooldown: %w", err)
				}
			}
		}
		return nil
	}

	var txErr error
	if u.tx != nil {
		txErr = u.tx.Transaction(ctx, work)
	} else {
		txErr = work(ctx)
	}
	if txErr != nil {
		u.logger.ErrorContext(ctx, "webhook_process_failed",
			slog.String("instance", evt.InstanceName),
			slog.String("event_type", string(evt.Type)),
			slog.String("grab_id", rec.ID.String()),
			slog.String("error", txErr.Error()),
		)
		// Preserve already-classified sentinels — ErrInvalidStatusTransition
		// from the repo's defence-in-depth check is a logic error and
		// must NOT be relabelled as transient. Everything else gets the
		// ErrDBUnavailable wrap so 007c routes it to HTTP 500.
		if errors.Is(txErr, grab.ErrInvalidStatusTransition) ||
			errors.Is(txErr, ports.ErrDBUnavailable) {
			return txErr
		}
		return fmt.Errorf("webhook transaction: %w: %w", ports.ErrDBUnavailable, txErr)
	}

	u.logger.InfoContext(ctx, "webhook_event_applied",
		slog.String("instance", evt.InstanceName),
		slog.String("event_type", string(evt.Type)),
		slog.String("status", string(target)),
		slog.String("grab_id", rec.ID.String()),
		slog.String("guid", rec.ReleaseGUID),
	)
	return nil
}

func mapEventToStatus(t webhook.EventType) grab.Status {
	switch t {
	case webhook.EventTypeImported:
		return grab.StatusImported
	case webhook.EventTypeImportFailed:
		return grab.StatusImportFailed
	default:
		return ""
	}
}

// handleGrabbed captures the qBit info-hash from a Sonarr OnGrab webhook
// onto the matching grab_records row, and also stamps the release size.
// Both are idempotent — never overwrites an already-set value. Status
// transition is NOT applied: the row is already "grabbed" by the time
// the webhook fires. Orphan grabbed events (no matching row) log at INFO
// and return nil — webhook-only flows or pre-Phase-10/12 rows will not
// have one.
func (u *UseCase) handleGrabbed(ctx context.Context, evt webhook.Event) error {
	parsed := grab.ParseTorrentHash(evt.DownloadID)
	if parsed == nil && evt.ReleaseSize == 0 {
		u.logger.DebugContext(ctx, "webhook_grab_no_metadata",
			slog.String("instance", evt.InstanceName),
			slog.String("download_id", evt.DownloadID),
			slog.String("raw_event_type", evt.RawEventType),
		)
		return nil
	}

	key := ports.MatchKey{
		DownloadID:   evt.DownloadID,
		ReleaseTitle: evt.ReleaseTitle,
		SeriesID:     evt.SeriesID,
		SeasonNumber: evt.SeasonNumber,
		InstanceName: evt.InstanceName,
	}
	rec, err := u.grabs.MatchLatest(ctx, key)
	if err != nil {
		if errors.Is(err, ports.ErrNotFound) {
			u.logger.InfoContext(ctx, "webhook_grab_orphan_no_row",
				slog.String("instance", evt.InstanceName),
				slog.String("download_id", evt.DownloadID),
				slog.String("release_title", evt.ReleaseTitle),
				slog.Int("series_id", evt.SeriesID),
				slog.Int("season", evt.SeasonNumber),
			)
			return nil
		}
		return fmt.Errorf("match grab record (grabbed branch): %w: %w", ports.ErrDBUnavailable, err)
	}

	if parsed != nil {
		if rec.TorrentHash != nil {
			u.logger.DebugContext(ctx, "webhook_grab_hash_already_set",
				slog.String("instance", evt.InstanceName),
				slog.String("grab_id", rec.ID.String()))
		} else {
			hash := strings.ToLower(*parsed)
			work := func(txCtx context.Context) error {
				if err := u.grabs.UpdateTorrentHash(txCtx, rec.ID, hash); err != nil {
					return fmt.Errorf("update torrent_hash: %w", err)
				}
				if u.torrentSeriesMap != nil && rec.SeriesID > 0 {
					row := torrentsync.MapRow{
						Instance:     evt.InstanceName,
						Hash:         hash,
						SeriesID:     rec.SeriesID,
						SeasonNumber: rec.SeasonNumber,
						Source:       torrentsync.MapSourceWebhook,
						CreatedAt:    u.now(),
					}
					if err := u.torrentSeriesMap.UpsertTx(txCtx, row); err != nil {
						return fmt.Errorf("upsert torrent_series_map (webhook): %w", err)
					}
				}
				return nil
			}
			var txErr error
			if u.tx != nil {
				txErr = u.tx.Transaction(ctx, work)
			} else {
				txErr = work(ctx)
			}
			if txErr != nil {
				if errors.Is(txErr, ports.ErrNotFound) {
					u.logger.InfoContext(ctx, "webhook_grab_row_vanished",
						slog.String("instance", evt.InstanceName),
						slog.String("grab_id", rec.ID.String()))
					return nil
				}
				return fmt.Errorf("webhook grab hash+map: %w: %w", ports.ErrDBUnavailable, txErr)
			}
			u.logger.InfoContext(ctx, "webhook_grab_hash_captured",
				slog.String("instance", evt.InstanceName),
				slog.String("grab_id", rec.ID.String()),
				slog.String("download_id", evt.DownloadID),
				slog.String("hash", hash),
				slog.Int("series_id", rec.SeriesID),
				slog.Int("season_number", rec.SeasonNumber),
				slog.String("source", string(torrentsync.MapSourceWebhook)),
			)
		}
	}

	if evt.ReleaseSize > 0 {
		if rec.SizeBytes != nil {
			u.logger.DebugContext(ctx, "webhook_grab_size_already_set",
				slog.String("instance", evt.InstanceName),
				slog.String("grab_id", rec.ID.String()),
				slog.Int64("existing_size", *rec.SizeBytes))
		} else if err := u.grabs.UpdateSizeBytes(ctx, rec.ID, evt.ReleaseSize); err != nil {
			if errors.Is(err, ports.ErrNotFound) {
				u.logger.InfoContext(ctx, "webhook_grab_size_row_vanished",
					slog.String("instance", evt.InstanceName),
					slog.String("grab_id", rec.ID.String()))
				return nil
			}
			return fmt.Errorf("update size_bytes: %w: %w", ports.ErrDBUnavailable, err)
		} else {
			u.logger.InfoContext(ctx, "webhook_grab_size_captured",
				slog.String("instance", evt.InstanceName),
				slog.String("grab_id", rec.ID.String()),
				slog.Int64("size_bytes", evt.ReleaseSize))
		}
	}

	u.runParseOnGrab(ctx, rec.ID, evt)
	return nil
}

// handleSeriesAdd upserts series_cache from a Sonarr SeriesAdd webhook.
// Errors are WARN-logged and swallowed — Sonarr retries on non-2xx and
// the cache is a best-effort sidecar (D-2.5). Nil seriesCache returns
// immediately (feature off). Missing series.id is a silent skip.
func (u *UseCase) handleSeriesAdd(ctx context.Context, evt webhook.Event) error {
	if evt.SeriesID == 0 {
		u.logger.DebugContext(ctx, "webhook_series_add_missing_id",
			slog.String("instance", evt.InstanceName),
			slog.String("raw_event_type", evt.RawEventType),
		)
		return nil
	}
	// E-1 priority path: full Sonarr API sync when wired.
	if u.seriesSyncer != nil {
		if err := u.seriesSyncer.SyncFromSonarrAPI(ctx, evt.InstanceName, evt.SeriesID); err != nil {
			u.logger.WarnContext(ctx, "webhook_series_add_full_sync_failed",
				slog.String("instance", evt.InstanceName),
				slog.Int("series_id", evt.SeriesID),
				slog.String("error", err.Error()),
			)
			// Fall through to the thin path as a safety net.
		} else {
			u.logger.InfoContext(ctx, "webhook_series_add_synced",
				slog.String("instance", evt.InstanceName),
				slog.Int("series_id", evt.SeriesID),
			)
			return nil
		}
	}
	if u.seriesCache == nil {
		return nil
	}
	entry := webhookSeriesToCacheEntry(evt)
	if err := u.seriesCache.Upsert(ctx, entry); err != nil {
		u.logger.WarnContext(ctx, "webhook_series_add_upsert_failed",
			slog.String("instance", evt.InstanceName),
			slog.Int("series_id", evt.SeriesID),
			slog.String("error", err.Error()),
		)
		return nil
	}
	u.logger.InfoContext(ctx, "webhook_series_add_cached",
		slog.String("instance", evt.InstanceName),
		slog.Int("series_id", evt.SeriesID),
		slog.String("title", evt.SeriesTitle),
	)
	return nil
}

// handleSeriesDelete soft-deletes the series_cache row + (when the
// E-2 cascade port is wired) every episode_states row for the same
// (instance, series_id) pair. Errors WARN-logged and swallowed —
// SeriesDelete is fire-and-forget. Soft-delete is idempotent at the
// repo layer; re-deliveries are harmless.
func (u *UseCase) handleSeriesDelete(ctx context.Context, evt webhook.Event) error {
	if u.seriesCache == nil {
		return nil
	}
	if evt.SeriesID == 0 {
		u.logger.DebugContext(ctx, "webhook_series_delete_missing_id",
			slog.String("instance", evt.InstanceName),
			slog.String("raw_event_type", evt.RawEventType),
		)
		return nil
	}
	cacheDeleted, episodeRows, err := scan.CascadeSeriesDelete(
		ctx,
		scan.CascadeDeleteDeps{
			SeriesCache:   u.seriesCache,
			EpisodeStates: u.episodeStates,
			Tx:            u.tx,
			Logger:        u.logger,
		},
		evt.InstanceName, evt.SeriesID,
	)
	if err != nil {
		u.logger.WarnContext(ctx, "webhook_series_delete_cascade_failed",
			slog.String("instance", evt.InstanceName),
			slog.Int("series_id", evt.SeriesID),
			slog.String("error", err.Error()),
		)
		return nil
	}
	u.logger.InfoContext(ctx, "webhook_series_deleted_cascade_ok",
		slog.String("instance", evt.InstanceName),
		slog.Int("series_id", evt.SeriesID),
		slog.Bool("cache_deleted", cacheDeleted),
		slog.Int("episode_states_deleted", episodeRows),
	)
	return nil
}

// webhookSeriesToCacheEntry adapts the trimmed series fields from the
// webhook payload onto a series.CacheEntry. Webhook schema is narrower
// than /api/v3/series (no genres, no images, no overview); missing
// fields stay nil/zero. The next scan-tick fillSeriesCache (041e)
// replaces the row with the rich version — eventual consistency.
func webhookSeriesToCacheEntry(evt webhook.Event) series.CacheEntry {
	entry := series.CacheEntry{
		InstanceName:   evt.InstanceName,
		SonarrSeriesID: evt.SeriesID,
		Title:          evt.SeriesTitle,
		TitleSlug:      evt.SeriesTitleSlug,
		Monitored:      true, // SeriesAdd fires on additions; assume monitored
	}
	if evt.SeriesTVDBID > 0 {
		v := evt.SeriesTVDBID
		entry.TVDBID = &v
	}
	if evt.SeriesIMDBID != "" {
		v := evt.SeriesIMDBID
		entry.IMDBID = &v
	}
	return entry
}

// runParseOnGrab fires Sonarr /api/v3/parse + ExtractExtras for an
// already-persisted grab record, then writes the result onto the row
// via UpdateParsed. Failure-isolated by design — any error path logs
// at WARN and returns; the caller's grab-row persistence is unaffected.
// Per-instance parse_on_grab_enabled = false short-circuits with metric
// result=disabled. Idempotent: a row whose Parsed is already populated
// (re-delivery) is skipped silently.
func (u *UseCase) runParseOnGrab(ctx context.Context, id uuid.UUID, evt webhook.Event) {
	snap, ok := u.instanceFor(evt.InstanceName)
	if !ok {
		observability.IncParseRelease(evt.InstanceName, "skipped")
		return
	}
	if !snap.ParseOnGrabEnabled {
		observability.IncParseRelease(evt.InstanceName, "disabled")
		u.logger.DebugContext(ctx, "webhook_parse_disabled",
			slog.String("instance", evt.InstanceName))
		return
	}
	client, ok := u.sonarrClientFor(evt.InstanceName)
	if !ok || client == nil {
		observability.IncParseRelease(evt.InstanceName, "skipped")
		return
	}
	title := strings.TrimSpace(evt.ReleaseTitle)
	if title == "" {
		observability.IncParseRelease(evt.InstanceName, "skipped")
		return
	}

	start := time.Now()
	pr, err := client.ParseRelease(ctx, title)
	dur := time.Since(start).Seconds()
	observability.ObserveParseReleaseDuration(evt.InstanceName, dur)
	if err != nil {
		observability.IncParseRelease(evt.InstanceName, "error")
		u.logger.WarnContext(ctx, "webhook_parse_failed",
			slog.String("instance", evt.InstanceName),
			slog.String("grab_id", id.String()),
			slog.String("error", err.Error()))
		return
	}

	extras := sonarr.ExtractExtras(title)
	merged := sonarr.MergeParse(sonarr.ParseResult{
		Quality:      pr.Quality,
		Source:       pr.Source,
		Resolution:   pr.Resolution,
		Languages:    pr.Languages,
		ReleaseGroup: pr.ReleaseGroup,
	}, extras)

	parsedAt := u.now()
	var payload *grab.Parsed
	if !merged.IsZero() {
		payload = &merged
	}
	if err := u.grabs.UpdateParsed(ctx, id, payload, parsedAt); err != nil {
		observability.IncParseRelease(evt.InstanceName, "error")
		u.logger.WarnContext(ctx, "webhook_parse_persist_failed",
			slog.String("instance", evt.InstanceName),
			slog.String("grab_id", id.String()),
			slog.String("error", err.Error()))
		return
	}
	observability.IncParseRelease(evt.InstanceName, "ok")
	u.logger.InfoContext(ctx, "webhook_parse_applied",
		slog.String("instance", evt.InstanceName),
		slog.String("grab_id", id.String()),
		slog.Bool("merged_is_zero", merged.IsZero()))
}
