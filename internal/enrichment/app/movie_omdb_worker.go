// movie_omdb_worker.go — Ф6-R-4a (L3-3) movie OMDb ratings worker.
//
// MovieOMDbWorker.HandleMovieOMDb fetches OMDb ratings for ONE canon movie by
// its imdb_id and writes the four OMDb-owned columns (imdb_rating, imdb_votes,
// omdb_rated, omdb_awards) as PLAIN values onto the movies row. It is the movie
// analog of OMDbWorker (series) but deliberately far leaner: no budget lanes,
// no enrichment_errors journal, no dispatcher — R-4a drives it inline off the
// MovieWorker's post-hydrate hook (enqueue-after-imdb), which itself runs on the
// SEPARATE MovieRefreshScheduler goroutine. That goroutine isolation is the
// movie mirror of the EntityOMDb "don't compete with series" precedent
// (ports.go:76): movie OMDb never touches the series worker/dispatcher budget or
// retry queue.
//
// omdb.Client.GetByIMDB + omdb.Map are reused verbatim — both are imdb-keyed and
// media-agnostic (they carry no series assumption), so the movie path adds zero
// client code and shares the runtime-swappable OMDb holder with the series
// worker via the same OMDbClient getter seam.
//
// NOTE (R-4a scope): no durable enrichment_errors journal for movie OMDb — the
// generic EntityType enum has no `movie` member and R-4a does not extend it, so
// upstream failures are log-only and simply retried on the next refresh tick
// (movies are few + dark-launched; a terminal-failure guard is an R-4b follow-up
// per story §3.7). The movie shares the SAME OMDb daily quota as series but has
// no budget guard in R-4a — acceptable at dark-launch volume; a Cold-lane guard
// is the natural follow-up if movie volume grows.
package enrichment

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/alexmorbo/seasonfill/internal/catalog/domain/movie"
	"github.com/alexmorbo/seasonfill/internal/shared/clients/omdb"
	ports "github.com/alexmorbo/seasonfill/internal/shared/dataports"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
	sharedports "github.com/alexmorbo/seasonfill/internal/shared/ports"
)

// MovieOMDbRepo is the worker's narrow canon-read + OMDb-owned-write surface over
// the movies table. Production impl is *enrichpersistence.MovieRepository (Go
// duck-typing). UpdateMovieOMDbColumns is the SOLE owner of the four rating
// columns and folds MarkOMDBSynced into the same call.
type MovieOMDbRepo interface {
	Get(ctx context.Context, id domain.MovieID) (movie.Canon, error)
	UpdateMovieOMDbColumns(ctx context.Context, id domain.MovieID, e omdb.Enrichment, now time.Time) error
}

// MovieOMDbWorkerDeps is the construction surface. Client (getter) + Movies are
// required; Logger / Clock default. Client is a getter — mirror of
// OMDbWorkerDeps.Client — so a runtime OMDb key swap is picked up without a
// rebuild.
type MovieOMDbWorkerDeps struct {
	Client func() OMDbClient // getter over the runtime-swappable OMDb holder
	Movies MovieOMDbRepo
	Logger *slog.Logger
	Clock  func() time.Time
}

// MovieOMDbWorker is the bound worker. Construct via NewMovieOMDbWorker.
type MovieOMDbWorker struct {
	deps MovieOMDbWorkerDeps
}

// NewMovieOMDbWorker validates required deps and applies defaults.
func NewMovieOMDbWorker(deps MovieOMDbWorkerDeps) (*MovieOMDbWorker, error) {
	if deps.Client == nil {
		return nil, errors.New("movie omdb worker: Client getter required")
	}
	if deps.Movies == nil {
		return nil, errors.New("movie omdb worker: Movies required")
	}
	if deps.Logger == nil {
		deps.Logger = sharedports.DomainLogger(slog.Default(), "enrichment")
	}
	if deps.Clock == nil {
		deps.Clock = func() time.Time { return time.Now().UTC() }
	}
	return &MovieOMDbWorker{deps: deps}, nil
}

// HandleMovieOMDb fetches + writes OMDb ratings for the canon movie identified by
// movieID. Terminal outcomes (movie missing / no imdb_id / client unavailable /
// upstream not_found / auth / transient upstream) return nil after logging — the
// caller's post-hydrate hook is best-effort and must not fail the TMDB hydrate.
// Only a WRITE failure bubbles up. Idempotent: safe to re-run every refresh; a
// success writes+stamps enrichment_omdb_synced_at, a skip leaves it untouched so
// the next tick retries.
func (w *MovieOMDbWorker) HandleMovieOMDb(ctx context.Context, movieID int64) error {
	log := w.deps.Logger.With(
		slog.String("entity_type", "movie"),
		slog.Int64("entity_id", movieID),
		slog.String("source", "omdb"),
	)

	canon, err := w.deps.Movies.Get(ctx, domain.MovieID(movieID))
	if err != nil {
		if errors.Is(err, ports.ErrNotFound) {
			log.WarnContext(ctx, "enrichment.movie_omdb.movie_missing")
			return nil
		}
		return fmt.Errorf("movie omdb worker: load canon %d: %w", movieID, err)
	}
	if canon.IMDBID == nil || *canon.IMDBID == "" {
		log.DebugContext(ctx, "enrichment.movie_omdb.skipped", slog.String("reason", "no_imdb_id"))
		return nil
	}
	imdbID := *canon.IMDBID

	client := w.deps.Client()
	if client == nil {
		// Reload race / OMDb disabled at boot — leave the row unstamped so a later
		// tick (once the holder is populated) retries. Not an error.
		log.WarnContext(ctx, "enrichment.movie_omdb.client_unavailable")
		return nil
	}

	resp, err := client.GetByIMDB(ctx, imdbID)
	if err != nil {
		// not_found: imdb id unknown to OMDb — log + skip, no write (do NOT clear a
		// prior rating on a lookup miss). auth/limit + generic errors are transient;
		// log and let the next refresh tick retry (row stays unstamped).
		switch {
		case errors.Is(err, omdb.ErrNotFound):
			log.InfoContext(ctx, "enrichment.movie_omdb.not_found", slog.String("imdb_id", string(imdbID)))
		case errors.Is(err, omdb.ErrInvalidKey) || errors.Is(err, omdb.ErrDailyLimit):
			log.WarnContext(ctx, "enrichment.movie_omdb.auth_failed", slog.String("error", err.Error()))
		default:
			log.WarnContext(ctx, "enrichment.movie_omdb.fetch_failed", slog.String("error", err.Error()))
		}
		return nil
	}

	// omdb.Map yields value-or-nil per field ("N/A" → nil). The plain-value writer
	// lands nil as SQL NULL so a stale rating is actively cleared on an all-N/A
	// response (sole-owner semantics).
	mapped := omdb.Map(resp)
	now := w.deps.Clock()
	if err := w.deps.Movies.UpdateMovieOMDbColumns(ctx, domain.MovieID(movieID), mapped, now); err != nil {
		return fmt.Errorf("movie omdb worker: write %d: %w", movieID, err)
	}

	log.InfoContext(ctx, "enrichment.movie_omdb.ok",
		slog.String("imdb_id", string(imdbID)),
		slog.Bool("has_rating", mapped.IMDBRating != nil),
	)
	return nil
}
