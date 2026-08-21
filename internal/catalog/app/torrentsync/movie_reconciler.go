package torrentsync

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/alexmorbo/seasonfill/internal/shared/clients/radarr"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
)

// ADR-0023 B1.3 — the movie half of the torrent bridge. Backfills
// torrent_movie_map for torrents that never came through the B1.2 Grab
// webhook, including ones the user added to qBit by hand.
//
// Sources, in write priority order (first-source-wins):
//
//	1. webhook            — B1.2, written elsewhere. NOT read here; the
//	                        repo's ON CONFLICT DO UPDATE created_at makes
//	                        the webhook row immune to a later reconciler
//	                        write, so re-offering such a hash is a
//	                        harmless no-op that costs one upsert.
//	2. grab_record        — SKIPPED. There is no movie grab table (the
//	                        series-only grab_records feeds source 2 of the
//	                        series pass). Deliberate, per story scope.
//	3. radarr_queue       — GET /api/v3/queue.
//	4. radarr_history     — GET /api/v3/history?eventType=1 (grabbed) and
//	                        ?eventType=2 (downloadFolderImported).
//
// Provenance (HARD, operator semantics):
//
//	hash present in grabbed-history  => radarr_search  (Radarr grabbed it,
//	                                    so B2 can re-grab by identifier)
//	otherwise                        => manual_import  (the user added it,
//	                                    B2 is signal-only)

// MovieHistoryPageCap bounds the /history walk per event type per
// reconciler pass. Two event types are walked, so the per-pass upstream
// budget is 2 * MovieHistoryPageCap pages — deliberately equal to the
// series pass's single HistoryPageCap=10 walk.
const MovieHistoryPageCap = 5

// MovieHistoryPageSize is the per-page record count for the movie
// /history pulls. Radarr's own default.
const MovieHistoryPageSize = 50

// RadarrReconciler is the narrow Radarr surface the movie pass consumes.
// Implemented in production by *radarr.Client. Twin of SonarrReconciler
// with one extra method — see ImportHistoryPaged's docstring for why the
// import stream is not optional.
type RadarrReconciler interface {
	QueueAll(ctx context.Context) (radarr.QueuePayload, error)
	GrabHistoryPaged(ctx context.Context, page, pageSize int) (radarr.HistoryPage, error)
	ImportHistoryPaged(ctx context.Context, page, pageSize int) (radarr.HistoryPage, error)
}

// applyMovieSources runs the movie pass for one instance and returns the
// hashes still unmapped afterwards. Upstream failures are reported in the
// error and — with ONE exception — never abort the pass: whatever the other
// sources did resolve is still written, and the returned slice always
// reflects the writes that actually landed.
//
// The exception is the grabbed-history fetch, the provenance oracle: if it
// fails the pass writes nothing at all, because no row's provenance could
// be derived soundly. See the call site for why that is unrecoverable.
//
// runHistory gates the EXPENSIVE import-history walk. The grabbed-history
// walk is NOT gated: it is the /queue source's provenance oracle, and /queue
// runs every tick, so a throttled oracle would either drop queue rows or
// stamp them manual_import permanently (first-source-wins). Gating only the
// import walk keeps /queue mapping every tick with sound provenance while
// still throttling the one history stream that has no cheap-source
// dependency. On a cheap-only tick (runHistory=false) the import stream is
// skipped, so an import-only hash — hand-added and already dropped from
// /queue — maps on the next history tick (boot tick or every Nth).
func (r *Reconciler) applyMovieSources(ctx context.Context, instance domain.InstanceName, client RadarrReconciler, hashes []string, runHistory bool) ([]string, error) {
	if r.movieMaps == nil || client == nil || len(hashes) == 0 {
		return hashes, nil
	}
	wanted := setOf(hashes)

	// Bound the upstream fan-out. The reconciler runs on the torrentsync
	// loop's long-lived rootCtx; WithTimeout nests correctly under any
	// tighter caller deadline (it never loosens an earlier one). Same
	// 2-minute budget as applyQueue.
	fetchCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()

	var firstErr error
	note := func(what string, err error) {
		wrapped := fmt.Errorf("%s: %w", what, err)
		r.logger.WarnContext(ctx, "torrentsync_reconciler_movie_source_failed",
			slog.String("instance", string(instance)),
			slog.String("movie_source", what),
			slog.String("error", err.Error()),
		)
		if firstErr == nil {
			firstErr = wrapped
		}
	}

	// Window A — grabbed history. Doubles as the provenance oracle:
	// membership in this set is the ONLY signal that Radarr itself grabbed
	// the release. Fetched FIRST because both later writes read it.
	//
	// A fetch failure here ABORTS the pass before any write. Provenance is
	// only soundly derivable over a COMPLETE grabbed window: with a partial
	// (or empty) oracle every queue/import edge would be stamped
	// manual_import, and that stamp is PERMANENT — the repo's ON CONFLICT
	// DO UPDATE created_at never rewrites provenance, and SetMovieMapping
	// stops the hash from ever being re-offered. The edges themselves are
	// cheap to re-derive next pass; a wrong provenance is not recoverable.
	// (Contrast the /queue and import-history failures below, which only
	// shrink the write set for this pass.)
	grabbed, err := r.movieHistoryWindow(fetchCtx, client.GrabHistoryPaged, wanted)
	if err != nil {
		note("radarr_grab_history", err)
		return hashes, firstErr
	}

	// Window B — downloadFolderImported history. The only surface that
	// still carries downloadId -> movieId for a hand-added torrent Radarr
	// has already imported and dropped from /queue. EXPENSIVE and gated:
	// walked only on a history tick. On a cheap-only tick it stays empty, so
	// import-only hashes wait for the next history tick — queue-derived and
	// grabbed rows still land every tick.
	var imported map[string]domain.RadarrMovieID
	if runHistory {
		imported, err = r.movieHistoryWindow(fetchCtx, client.ImportHistoryPaged, wanted)
		if err != nil {
			note("radarr_import_history", err)
		}
	}

	// Source 3 — /queue. One global fan-out, matched locally.
	queued := make(map[string]domain.RadarrMovieID)
	payload, qerr := client.QueueAll(fetchCtx)
	if qerr != nil {
		note("radarr_queue", qerr)
	} else {
		for _, rec := range payload.Records {
			hash := strings.ToLower(rec.DownloadID)
			if hash == "" || rec.MovieID <= 0 {
				continue
			}
			if _, want := wanted[hash]; !want {
				continue
			}
			if _, dup := queued[hash]; dup {
				continue
			}
			queued[hash] = rec.MovieID
		}
	}

	mapped := make(map[string]struct{}, len(queued)+len(grabbed)+len(imported))
	// Write priority: radarr_queue > radarr_history. Within history the
	// grabbed stream wins over the import stream (a Radarr-grabbed release
	// is also imported, and we want the grabbed edge recorded).
	r.writeMovieBatch(ctx, instance, queued, grabbed, MovieMapSourceRadarrQueue, mapped)
	r.writeMovieBatch(ctx, instance, grabbed, grabbed, MovieMapSourceRadarrHistory, mapped)
	r.writeMovieBatch(ctx, instance, imported, grabbed, MovieMapSourceRadarrHistory, mapped)

	return filterMapped(hashes, mapped), firstErr
}

// movieHistoryWindow walks the FRESHEST MovieHistoryPageCap pages of one
// Radarr history event stream and returns hash -> movieId restricted to the
// hashes we care about. The freshest event for a hash wins (the stream is
// sorted date-descending, so the first hit is the freshest).
//
// Deliberately NOT cursor-paged like the series source 4: provenance
// derivation is only sound over a COMPLETE window. With a rolling cursor a
// Radarr-grabbed release whose grab event sits outside the page the cursor
// is parked on would be classified manual_import — and first-source-wins
// makes that misclassification permanent. Every unmapped hash is by
// definition still present in qBit, i.e. recent, so the freshest
// MovieHistoryPageCap*MovieHistoryPageSize events are the right window.
//
// ACCEPTED LIMITATION: a Radarr-grabbed release whose grab event has aged
// out of that window AND is still unmapped in qBit will be written
// manual_import. B2 re-derives provenance when it acts on a row.
//
// End-of-data is decided on HistoryPage.RawCount, never on len(Records) —
// a page made entirely of usenet grabs filters down to zero records while
// more torrent grabs sit on the next page.
func (r *Reconciler) movieHistoryWindow(
	ctx context.Context,
	fetch func(ctx context.Context, page, pageSize int) (radarr.HistoryPage, error),
	wanted map[string]struct{},
) (map[string]domain.RadarrMovieID, error) {
	out := make(map[string]domain.RadarrMovieID)
	for page := 1; page <= MovieHistoryPageCap; page++ {
		hp, err := fetch(ctx, page, MovieHistoryPageSize)
		if err != nil {
			return out, fmt.Errorf("history page %d: %w", page, err)
		}
		for _, rec := range hp.Records {
			hash := strings.ToLower(rec.DownloadID)
			if hash == "" || rec.MovieID <= 0 {
				continue
			}
			if _, want := wanted[hash]; !want {
				continue
			}
			if _, dup := out[hash]; dup {
				continue // freshest event already recorded
			}
			out[hash] = rec.MovieID
		}
		// Prefer the server-reported page size (as QueueAll does) so a
		// server that clamps our requested pageSize does not make a full
		// clamped page look short and cut the walk to page 1.
		effPageSize := MovieHistoryPageSize
		if hp.PageSize > 0 {
			effPageSize = hp.PageSize
		}
		if hp.RawCount < effPageSize {
			break // end of data
		}
	}
	return out, nil
}

// writeMovieBatch upserts one source's hash -> movieId edges, skipping any
// hash a higher-priority source already wrote in this pass. `grabbed` is
// the provenance oracle; it is passed even when it IS the source map (the
// grabbed-history batch), which is exactly right — every hash in that batch
// is by construction radarr_search.
func (r *Reconciler) writeMovieBatch(
	ctx context.Context,
	instance domain.InstanceName,
	src map[string]domain.RadarrMovieID,
	grabbed map[string]domain.RadarrMovieID,
	source MovieMapSource,
	mapped map[string]struct{},
) {
	for hash, movieID := range src {
		if _, done := mapped[hash]; done {
			continue
		}
		provenance := MovieProvenanceManualImport
		if _, ok := grabbed[hash]; ok {
			provenance = MovieProvenanceRadarrSearch
		}
		row := MovieMapRow{
			Instance:      instance,
			Hash:          hash,
			RadarrMovieID: movieID,
			Source:        source,
			Provenance:    provenance,
			CreatedAt:     r.now(),
		}
		if err := r.movieMaps.Upsert(ctx, row); err != nil {
			r.logger.WarnContext(ctx, "torrentsync_reconciler_movie_map_write_failed",
				slog.String("instance", string(instance)),
				slog.String("hash", hash),
				slog.String("source", string(source)),
				slog.String("error", err.Error()),
			)
			continue
		}
		mapped[hash] = struct{}{}
		r.store.SetMovieMapping(instance, hash, movieID)
		r.logger.InfoContext(ctx, "torrentsync_reconciler_movie_mapped",
			slog.String("instance", string(instance)),
			slog.String("hash", hash),
			slog.String("source", string(source)),
			slog.String("provenance", string(provenance)),
			slog.Int("radarr_movie_id", int(movieID)),
		)
	}
}
