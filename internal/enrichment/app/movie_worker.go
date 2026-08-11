// movie_worker.go — Ф6-R-4a (L3-2) movie TMDB hydration worker.
//
// MovieWorker.HandleForced hydrates ONE canon movie row from /movie/{id}:
// load canon → GetMovie(language-aware) → map → COALESCE Upsert → localized
// movie_i18n write → MarkTMDBSynced. Far simpler than SeriesWorker.HandleForced
// (no seasons/episodes/people/taxonomy fanout) — a movie is a single canon row
// plus one localized side-table row.
//
// This worker does NOT import discovery and consumes only narrow ports
// (movie_ports.go). It is driven by the SEPARATE MovieRefreshScheduler (its own
// budget/ticker) so movie hydration never dequeues series worker budget.
package enrichment

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/alexmorbo/seasonfill/internal/shared/clients/tmdb"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
	sharedports "github.com/alexmorbo/seasonfill/internal/shared/ports"
)

// MovieWorkerDeps is the construction surface. TMDB + Movies are required;
// I18n / Resolver are nil-OK; BaseLang / Logger / Clock default.
type MovieWorkerDeps struct {
	TMDB     MovieTMDBClient
	Movies   MovieCanonRepo
	I18n     MovieI18nWriter // nil-OK
	Resolver MediaResolver   // nil-OK (reuses the series A4 resolver port)
	// OMDb — Ф6-R-4a (L3-3) post-hydrate OMDb ratings trigger. nil-OK (OMDb
	// disabled at boot). When set AND the hydrated movie carries an imdb_id, the
	// worker fires it right after MarkTMDBSynced (enqueue-after-imdb): OMDb only
	// runs once TMDB has stamped the imdb_id. Best-effort — an OMDb failure is
	// logged, never failing or rolling back the committed TMDB hydrate.
	OMDb MovieOMDbHandler
	// Collections — Ф6-R-5 fail-soft collection populate seam. nil-OK (disabled at
	// boot = exact pre-R-5 behavior). When set AND the hydrated canon carries a
	// non-nil CollectionID, the worker fires it AFTER MarkTMDBSynced. Best-effort:
	// a failure is logged, never failing/rolling back the committed TMDB hydrate.
	Collections MovieCollectionPopulator
	BaseLang    string // default tmdb.DefaultLanguage
	Logger      *slog.Logger
	Clock       func() time.Time
}

// MovieOMDbHandler is the post-hydrate OMDb trigger seam. Production impl is
// *MovieOMDbWorker.HandleMovieOMDb. Kept a narrow one-method port so the TMDB
// hydration worker never imports the OMDb client — the wiring layer binds the
// concrete worker.
type MovieOMDbHandler interface {
	HandleMovieOMDb(ctx context.Context, movieID int64) error
}

// MovieWorker hydrates canon movies from TMDB. Satisfies MovieForceRefresher.
type MovieWorker struct {
	deps     MovieWorkerDeps
	baseLang string
}

// NewMovieWorker validates required deps and applies defaults. Returns an error
// (rather than panicking) so the boot wirer can log "movie worker disabled".
func NewMovieWorker(deps MovieWorkerDeps) (*MovieWorker, error) {
	if deps.TMDB == nil {
		return nil, errors.New("movie worker: TMDB is required")
	}
	if deps.Movies == nil {
		return nil, errors.New("movie worker: Movies is required")
	}
	if deps.Logger == nil {
		deps.Logger = sharedports.DomainLogger(slog.Default(), "enrichment")
	}
	if deps.Clock == nil {
		deps.Clock = func() time.Time { return time.Now().UTC() }
	}
	baseLang := deps.BaseLang
	if baseLang == "" {
		baseLang = tmdb.DefaultLanguage
	}
	return &MovieWorker{deps: deps, baseLang: baseLang}, nil
}

// HandleForced hydrates the canon movie identified by movieID. Idempotent: each
// write (canon Upsert, movie_i18n Upsert, MarkTMDBSynced) is independently
// COALESCE-safe, so a partial failure just leaves the row stale for the next
// tick to retry (self-healing; no cross-write transaction needed for R-4a).
func (w *MovieWorker) HandleForced(ctx context.Context, movieID int64) error {
	canon, err := w.deps.Movies.Get(ctx, domain.MovieID(movieID))
	if err != nil {
		return fmt.Errorf("movie worker: load canon %d: %w", movieID, err)
	}
	if canon.TMDBID == nil {
		// A tmdb-less movie (Radarr orphan) has nothing to fetch. Skip cleanly —
		// mirror the series worker's nil-tmdb skip. Not an error.
		w.deps.Logger.DebugContext(ctx, "enrichment.movie.skipped",
			slog.Int64("movie_id", movieID),
			slog.String("reason", "no_tmdb_id"),
		)
		return nil
	}
	tmdbID := int64(*canon.TMDBID)

	// language-aware detail fetch (#1184 guard — GetMovie calls c.languageFor).
	resp, err := w.deps.TMDB.GetMovie(ctx, tmdbID, w.baseLang)
	if err != nil {
		return fmt.Errorf("movie worker: GetMovie(%d): %w", tmdbID, err)
	}

	mapped := tmdb.MapMovieToCanon(resp)
	mapped.ID = canon.ID // target the existing row by PK (COALESCE Upsert path)

	// Media pre-warm side-effect (nil-OK): mint media_assets pending rows so the
	// eventual read has stable sha256 handles. Canon keeps RAW TMDB paths (mirror
	// of the discovery stub + series A4 "canon carries raw paths" choice).
	if w.deps.Resolver != nil {
		if mapped.PosterAsset != nil {
			_ = w.deps.Resolver.Resolve(ctx, mapped.PosterAsset, "w342", "poster_w342")
		}
		if mapped.BackdropAsset != nil {
			_ = w.deps.Resolver.Resolve(ctx, mapped.BackdropAsset, "w1280", "backdrop_w1280")
		}
	}

	if _, err := w.deps.Movies.Upsert(ctx, mapped); err != nil {
		return fmt.Errorf("movie worker: upsert canon %d: %w", canon.ID, err)
	}

	now := w.deps.Clock()

	// Localized base-language row (nil-OK). resp.Title/Overview/Tagline are
	// already localized to w.baseLang (GetMovie requested language=baseLang).
	if w.deps.I18n != nil {
		if err := w.deps.I18n.UpsertEnriched(ctx, canon.ID, w.baseLang,
			resp.Title, resp.Overview, resp.Tagline,
			mapped.PosterAsset, mapped.BackdropAsset, now); err != nil {
			return fmt.Errorf("movie worker: upsert i18n %d: %w", canon.ID, err)
		}
	}

	if err := w.deps.Movies.MarkTMDBSynced(ctx, canon.ID, now); err != nil {
		return fmt.Errorf("movie worker: mark synced %d: %w", canon.ID, err)
	}

	w.deps.Logger.InfoContext(ctx, "enrichment.movie.hydrated",
		slog.Int64("movie_id", int64(canon.ID)),
		slog.Int64("tmdb_id", tmdbID),
	)

	// Ф6-R-4a L3-3: enqueue-after-imdb OMDb ratings follow-up. Runs inline on the
	// movie scheduler goroutine (isolated from the series worker/dispatcher budget
	// — the movie mirror of the EntityOMDb precedent). Gated on a present imdb_id
	// (mapped from this fetch, or preserved on the canon). Best-effort: a failure
	// is logged only and must not fail the already-committed TMDB hydrate.
	if w.deps.OMDb != nil && (mapped.IMDBID != nil || canon.IMDBID != nil) {
		if oerr := w.deps.OMDb.HandleMovieOMDb(ctx, int64(canon.ID)); oerr != nil {
			w.deps.Logger.WarnContext(ctx, "enrichment.movie.omdb_followup_failed",
				slog.Int64("movie_id", int64(canon.ID)),
				slog.String("error", oerr.Error()),
			)
		}
	}

	// Ф6-R-5: fail-soft collection populate. Runs AFTER the movie hydrate is
	// committed (canon Upsert + MarkTMDBSynced). Gated on a configured populator
	// AND a non-nil CollectionID on the freshly hydrated canon. Best-effort — a
	// failure is logged only and MUST NOT fail the already-committed movie
	// hydrate (mirror of the OMDb follow-up policy above).
	if w.deps.Collections != nil && mapped.CollectionID != nil {
		if cerr := w.deps.Collections.PopulateCollection(ctx, *mapped.CollectionID); cerr != nil {
			w.deps.Logger.WarnContext(ctx, "enrichment.movie.collection_populate_failed",
				slog.Int64("movie_id", int64(canon.ID)),
				slog.Int("collection_id", *mapped.CollectionID),
				slog.String("error", cerr.Error()),
			)
		}
	}
	return nil
}
