package torrentsync

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/alexmorbo/seasonfill/infrastructure/sonarr"
)

// HistoryPageCap is the strict upper bound on /history pages walked
// per reconciler tick per instance. Old libraries have thousands
// of pages; without this cap one tick exceeds the 30 s torrentsync
// cadence. PRD §4.5 risk note + story 221 strict constraint.
const HistoryPageCap = 10

// HistoryPageSize is the per-page record count for /history pulls.
// Sonarr's default is 50; the reconciler MUST keep this stable —
// the cursor walks page numbers, not record offsets, so changing
// the size mid-walk shifts the alignment of "next page".
const HistoryPageSize = 50

// ReconcilerEveryNthTick is the sub-cadence at which the reconciler
// runs inside the torrentsync loop. N=10 with the default 30 s
// cadence => a reconciler pass every ~5 minutes. Tests pin the
// value via a UseCase option; production uses this constant.
const ReconcilerEveryNthTick = 10

// MapRepo is the narrow port the reconciler writes to. Implemented
// in production by repositories.TorrentSeriesMapRepository.
type MapRepo interface {
	// Upsert writes one (instance, hash) row. First-source-wins:
	// a row that already exists is NOT overwritten — the source
	// discriminator stays as it was on first insert. Returns nil
	// for "row already exists" — the reconciler treats it as a
	// successful no-op.
	Upsert(ctx context.Context, row MapRow) error

	// UpsertTx is the same as Upsert but joins an existing tx
	// scope on the supplied ctx. Used by the webhook path so the
	// map write joins the same tx as the grab_records.torrent_hash
	// update. Production implementation routes via dbFromContext.
	UpsertTx(ctx context.Context, row MapRow) error
}

// MapRow is the row payload for MapRepo.Upsert.
type MapRow struct {
	Instance     string
	Hash         string
	SeriesID     int
	SeasonNumber int // 0 = unknown (column is nullable)
	Source       MapSource
	CreatedAt    time.Time
}

// MapSource discriminates the path that produced the row.
// Values match the strings persisted to torrent_series_map.source.
type MapSource string

const (
	MapSourceWebhook    MapSource = "webhook"
	MapSourceGrabRecord MapSource = "grab_record"
	MapSourceQueue      MapSource = "sonarr_queue"
	MapSourceHistory    MapSource = "sonarr_history"
)

// GrabHashLookup is the narrow port for batch lookup of
// `grab_records.torrent_hash -> (sonarr_series_id, season_number)`.
// Implemented by GrabRepository.FindSeriesByTorrentHashes.
type GrabHashLookup interface {
	FindSeriesByTorrentHashes(ctx context.Context, instance string, hashes []string) ([]GrabHashRow, error)
}

// GrabHashRow is one batch-lookup result row.
type GrabHashRow struct {
	Hash         string
	SeriesID     int
	SeasonNumber int
}

// SonarrReconciler is the narrow Sonarr surface the reconciler
// consumes. Implemented in production by *sonarr.Client (just the
// methods §1/§2 add).
type SonarrReconciler interface {
	QueueAll(ctx context.Context) (sonarr.QueuePayload, error)
	GrabHistoryPaged(ctx context.Context, page, pageSize int) (sonarr.HistoryPage, error)
}

// UnmappedGauge is the narrow metric surface the reconciler emits
// to. Implemented in production by observability.SetTorrentsyncUnmapped.
type UnmappedGauge interface {
	SetTorrentsyncUnmapped(instance string, count int)
}

// Reconciler runs the 4-source torrent->series mapping pass for one
// instance. State (the history cursor) is held in-memory and keyed
// by instance — the reconciler value itself is safe for concurrent
// reads from different instance loops.
//
// Cursor lifecycle:
//   - Initialised to page 1 on first MaybeRun for an instance.
//   - Advanced after each /history page consumed during a tick.
//   - Reset to page 1 when /history returns fewer records than
//     HistoryPageSize (end-of-data signal).
//   - Discarded on pod restart — no DB persistence is needed
//     because the per-tick cap bounds the catch-up work and
//     newer grabs are caught by webhook + grab_record first.
type Reconciler struct {
	store         *Store
	maps          MapRepo
	grabs         GrabHashLookup
	sonarrFor     func(instance string) (SonarrReconciler, bool)
	gauge         UnmappedGauge
	logger        *slog.Logger
	now           func() time.Time
	historyEveryN int

	mu      sync.Mutex
	cursor  map[string]int // instance -> next history page to fetch
	tickIdx map[string]int // instance -> counter of torrentsync ticks observed
}

// NewReconciler wires the 4-source dispatcher. Nil dependencies
// degrade gracefully: a nil sonarrFor short-circuits sources 3+4
// (we still run sources 1+2 against the store + grab_records);
// a nil gauge is silently skipped; a nil maps is a programming
// error and panics at construction.
func NewReconciler(
	store *Store,
	maps MapRepo,
	grabs GrabHashLookup,
	sonarrFor func(instance string) (SonarrReconciler, bool),
	gauge UnmappedGauge,
	logger *slog.Logger,
) *Reconciler {
	if maps == nil {
		panic("torrentsync.NewReconciler: maps must not be nil")
	}
	if logger == nil {
		logger = slog.Default()
	}
	if sonarrFor == nil {
		sonarrFor = func(string) (SonarrReconciler, bool) { return nil, false }
	}
	return &Reconciler{
		store:         store,
		maps:          maps,
		grabs:         grabs,
		sonarrFor:     sonarrFor,
		gauge:         gauge,
		logger:        logger,
		now:           func() time.Time { return time.Now().UTC() },
		historyEveryN: ReconcilerEveryNthTick,
		cursor:        make(map[string]int),
		tickIdx:       make(map[string]int),
	}
}

// WithEveryN overrides the sub-cadence. Tests use this to force a
// reconciler pass on every torrentsync tick instead of every 10th.
func (r *Reconciler) WithEveryN(n int) *Reconciler {
	if n > 0 {
		r.historyEveryN = n
	}
	return r
}

// WithClock pins the clock for tests.
func (r *Reconciler) WithClock(f func() time.Time) *Reconciler {
	r.now = f
	return r
}

// MaybeRun is the per-torrentsync-tick entrypoint. It returns
// without doing any work on 9 out of every 10 ticks; on the
// reconciler tick it walks all four sources, writes new map rows,
// and emits the unmapped-count gauge.
//
// The error returned summarises the worst per-source outcome —
// the loop logs it WARN but never lets it propagate (one bad
// reconciler tick must not stall the qBit refresh path).
func (r *Reconciler) MaybeRun(ctx context.Context, instance string) error {
	r.mu.Lock()
	r.tickIdx[instance]++
	due := r.tickIdx[instance]%r.historyEveryN == 0
	r.mu.Unlock()
	if !due {
		return nil
	}
	return r.run(ctx, instance)
}

// run is the unconditional reconciler pass — exposed for tests
// that want to drive it without dialling the tick counter.
func (r *Reconciler) run(ctx context.Context, instance string) error {
	unmapped := r.unmappedHashes(instance)
	startedAt := r.now()
	r.logger.InfoContext(ctx, "torrentsync_reconciler_start",
		slog.String("instance", instance),
		slog.Int("unmapped_count", len(unmapped)),
	)

	if len(unmapped) == 0 {
		r.emitGauge(instance, 0)
		return nil
	}

	// Source 2: grab_records.torrent_hash batch lookup.
	if r.grabs != nil {
		remaining, err := r.applyGrabRecords(ctx, instance, unmapped)
		if err != nil {
			r.logger.WarnContext(ctx, "torrentsync_reconciler_grab_record_failed",
				slog.String("instance", instance),
				slog.String("error", err.Error()),
			)
		} else {
			unmapped = remaining
		}
	}

	// Source 3: Sonarr /queue.
	if client, ok := r.sonarrFor(instance); ok && len(unmapped) > 0 {
		remaining, err := r.applyQueue(ctx, instance, client, unmapped)
		if err != nil {
			r.logger.WarnContext(ctx, "torrentsync_reconciler_queue_failed",
				slog.String("instance", instance),
				slog.String("error", err.Error()),
			)
		} else {
			unmapped = remaining
		}

		// Source 4: Sonarr /history paginated with cursor.
		if len(unmapped) > 0 {
			remaining, err = r.applyHistory(ctx, instance, client, unmapped)
			if err != nil {
				r.logger.WarnContext(ctx, "torrentsync_reconciler_history_failed",
					slog.String("instance", instance),
					slog.String("error", err.Error()),
				)
			} else {
				unmapped = remaining
			}
		}
	}

	r.emitGauge(instance, len(unmapped))
	r.logger.InfoContext(ctx, "torrentsync_reconciler_done",
		slog.String("instance", instance),
		slog.Int("unmapped_count", len(unmapped)),
		slog.Duration("elapsed", r.now().Sub(startedAt)),
	)
	return nil
}

// unmappedHashes returns the lower-cased hashes in the store that
// don't yet have a bySeries entry. The store is the source of
// truth for "currently in qBit"; the map row presence is the
// source of truth for "already bridged".
func (r *Reconciler) unmappedHashes(instance string) []string {
	rows := r.store.All(instance)
	if len(rows) == 0 {
		return nil
	}
	out := make([]string, 0, len(rows))
	for h := range rows {
		if r.store.SeriesForHash(instance, h) != 0 {
			continue
		}
		out = append(out, strings.ToLower(h))
	}
	return out
}

// applyGrabRecords runs source 2 (PRD §4.5). Returns the unmapped
// slice with mapped hashes removed.
func (r *Reconciler) applyGrabRecords(ctx context.Context, instance string, hashes []string) ([]string, error) {
	rows, err := r.grabs.FindSeriesByTorrentHashes(ctx, instance, hashes)
	if err != nil {
		return hashes, fmt.Errorf("grab_records lookup: %w", err)
	}
	mapped := make(map[string]struct{}, len(rows))
	for _, row := range rows {
		hash := strings.ToLower(row.Hash)
		if err := r.writeMap(ctx, MapRow{
			Instance:     instance,
			Hash:         hash,
			SeriesID:     row.SeriesID,
			SeasonNumber: row.SeasonNumber,
			Source:       MapSourceGrabRecord,
			CreatedAt:    r.now(),
		}); err != nil {
			r.logger.WarnContext(ctx, "torrentsync_reconciler_map_write_failed",
				slog.String("instance", instance),
				slog.String("hash", hash),
				slog.String("source", string(MapSourceGrabRecord)),
				slog.String("error", err.Error()),
			)
			continue
		}
		mapped[hash] = struct{}{}
		r.logger.InfoContext(ctx, "torrentsync_reconciler_mapped",
			slog.String("instance", instance),
			slog.String("hash", hash),
			slog.String("source", string(MapSourceGrabRecord)),
			slog.Int("series_id", row.SeriesID),
			slog.Int("season_number", row.SeasonNumber),
		)
	}
	return filterMapped(hashes, mapped), nil
}

// applyQueue runs source 3. Sonarr /queue with includeSeries=false
// returns one record per active download; downloadId is the
// lowercase hash for torrent grabs.
func (r *Reconciler) applyQueue(ctx context.Context, instance string, client SonarrReconciler, hashes []string) ([]string, error) {
	payload, err := client.QueueAll(ctx)
	if err != nil {
		return hashes, fmt.Errorf("sonarr queue: %w", err)
	}
	wanted := setOf(hashes)
	mapped := make(map[string]struct{}, len(payload.Records))
	for _, rec := range payload.Records {
		hash := strings.ToLower(rec.DownloadID)
		if hash == "" {
			continue
		}
		if _, want := wanted[hash]; !want {
			continue
		}
		if rec.SeriesID == 0 {
			continue
		}
		if err := r.writeMap(ctx, MapRow{
			Instance:     instance,
			Hash:         hash,
			SeriesID:     rec.SeriesID,
			SeasonNumber: rec.SeasonNumber,
			Source:       MapSourceQueue,
			CreatedAt:    r.now(),
		}); err != nil {
			r.logger.WarnContext(ctx, "torrentsync_reconciler_map_write_failed",
				slog.String("instance", instance),
				slog.String("hash", hash),
				slog.String("source", string(MapSourceQueue)),
				slog.String("error", err.Error()),
			)
			continue
		}
		mapped[hash] = struct{}{}
		r.logger.InfoContext(ctx, "torrentsync_reconciler_mapped",
			slog.String("instance", instance),
			slog.String("hash", hash),
			slog.String("source", string(MapSourceQueue)),
			slog.Int("series_id", rec.SeriesID),
			slog.Int("season_number", rec.SeasonNumber),
		)
	}
	return filterMapped(hashes, mapped), nil
}

// applyHistory runs source 4 — paginated /history with cursor.
// Walks up to HistoryPageCap pages per tick. When a page comes
// back short (len(records) < HistoryPageSize) the cursor resets
// to page 1: end-of-data, the next tick re-scans the freshest
// grabs.
func (r *Reconciler) applyHistory(ctx context.Context, instance string, client SonarrReconciler, hashes []string) ([]string, error) {
	wanted := setOf(hashes)
	mapped := make(map[string]struct{})

	r.mu.Lock()
	page := r.cursor[instance]
	if page <= 0 {
		page = 1
	}
	r.mu.Unlock()

	pagesWalked := 0
	endOfData := false
	for pagesWalked < HistoryPageCap && len(wanted) > len(mapped) {
		hp, err := client.GrabHistoryPaged(ctx, page, HistoryPageSize)
		if err != nil {
			// Persist whatever cursor advanced before erroring so
			// the next tick resumes past the failed page.
			r.advanceCursor(instance, page, endOfData)
			return filterMapped(hashes, mapped), fmt.Errorf("history page %d: %w", page, err)
		}
		for _, rec := range hp.Records {
			hash := strings.ToLower(rec.DownloadID)
			if hash == "" {
				continue
			}
			if _, want := wanted[hash]; !want {
				continue
			}
			if _, already := mapped[hash]; already {
				continue
			}
			if rec.SeriesID == 0 {
				continue
			}
			if err := r.writeMap(ctx, MapRow{
				Instance:     instance,
				Hash:         hash,
				SeriesID:     rec.SeriesID,
				SeasonNumber: rec.SeasonNumber,
				Source:       MapSourceHistory,
				CreatedAt:    r.now(),
			}); err != nil {
				r.logger.WarnContext(ctx, "torrentsync_reconciler_map_write_failed",
					slog.String("instance", instance),
					slog.String("hash", hash),
					slog.String("source", string(MapSourceHistory)),
					slog.String("error", err.Error()),
				)
				continue
			}
			mapped[hash] = struct{}{}
			r.logger.InfoContext(ctx, "torrentsync_reconciler_mapped",
				slog.String("instance", instance),
				slog.String("hash", hash),
				slog.String("source", string(MapSourceHistory)),
				slog.Int("series_id", rec.SeriesID),
				slog.Int("season_number", rec.SeasonNumber),
			)
		}
		pagesWalked++
		page++
		// Sonarr returns fewer records than pageSize when we
		// run past the end. Reset cursor so the next tick
		// re-scans the freshest grabs.
		if len(hp.Records) < HistoryPageSize {
			endOfData = true
			break
		}
	}
	r.advanceCursor(instance, page, endOfData)
	r.logger.InfoContext(ctx, "torrentsync_reconciler_history_page_walk",
		slog.String("instance", instance),
		slog.Int("pages_walked", pagesWalked),
		slog.Int("next_page", page),
		slog.Bool("end_of_data", endOfData),
		slog.Int("mapped_this_pass", len(mapped)),
	)
	return filterMapped(hashes, mapped), nil
}

// advanceCursor persists the next history page to read. When
// endOfData is true the cursor resets to 1 so the next reconciler
// tick walks from the freshest grabs again.
func (r *Reconciler) advanceCursor(instance string, nextPage int, endOfData bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if endOfData {
		r.cursor[instance] = 1
		return
	}
	r.cursor[instance] = nextPage
}

// writeMap is the single map-row write entrypoint — also updates
// the store's bySeries index so 222's read endpoint can answer
// from memory without a DB round-trip per request.
func (r *Reconciler) writeMap(ctx context.Context, row MapRow) error {
	if row.CreatedAt.IsZero() {
		row.CreatedAt = r.now()
	}
	if err := r.maps.Upsert(ctx, row); err != nil {
		return fmt.Errorf("upsert torrent_series_map (%s): %w", row.Source, err)
	}
	r.store.SetSeriesMapping(row.Instance, row.Hash, row.SeriesID)
	return nil
}

func (r *Reconciler) emitGauge(instance string, n int) {
	if r.gauge == nil {
		return
	}
	r.gauge.SetTorrentsyncUnmapped(instance, n)
}

// CursorPageFor exposes the next history page the reconciler will
// fetch for the named instance. Test-only diagnostic.
func (r *Reconciler) CursorPageFor(instance string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	p := r.cursor[instance]
	if p <= 0 {
		return 1
	}
	return p
}

// TickIndexFor exposes the per-instance torrentsync tick counter.
// Test-only diagnostic.
func (r *Reconciler) TickIndexFor(instance string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.tickIdx[instance]
}

func filterMapped(in []string, mapped map[string]struct{}) []string {
	if len(mapped) == 0 {
		return in
	}
	out := make([]string, 0, len(in)-len(mapped))
	for _, h := range in {
		if _, ok := mapped[h]; ok {
			continue
		}
		out = append(out, h)
	}
	return out
}

func setOf(hashes []string) map[string]struct{} {
	out := make(map[string]struct{}, len(hashes))
	for _, h := range hashes {
		out[strings.ToLower(h)] = struct{}{}
	}
	return out
}
