// movie_ports.go — Ф6-R-4a (L3-2) movie enrichment ports. Additive-only: new
// narrow seams for the movie hydration worker + separate movie refresh
// scheduler. NONE of the existing TV ports (ports.go) are edited. The movie
// worker consumes these instead of the ~20-repo SeriesWorkerDeps because movie
// hydration is a single canon row + one localized side-table row (no seasons/
// episodes/people/taxonomy fanout).
package enrichment

import (
	"context"
	"time"

	"github.com/alexmorbo/seasonfill/internal/catalog/domain/movie"
	enrichdomain "github.com/alexmorbo/seasonfill/internal/enrichment/domain/enrichment"
	"github.com/alexmorbo/seasonfill/internal/shared/clients/tmdb"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
)

// MovieTMDBClient is the movie detail-fetch seam. Production impl is a wiring-
// local adapter over the runtime-swappable TMDB holder (mirror of
// changesListerFromHolder); tests pass a fake returning a fixture response.
type MovieTMDBClient interface {
	// GetMovie fetches /movie/{id} with the canonical append_to_response,
	// localised to language (BCP-47). Language-aware (#1184 guard).
	GetMovie(ctx context.Context, id int64, language string) (*tmdb.MovieResponse, error)
}

// MovieCanonRepo is the worker's canon read/write + freshness-stamp surface
// against the `movies` table. Production impl is
// *enrichpersistence.MovieRepository (Go duck-typing). Upsert is COALESCE-
// guarded (movieUpsertAssignments) so a partial hydrate never blanks an OMDb
// column, and tmdb_changed_at (excluded from the assignments) stays sole-writer.
type MovieCanonRepo interface {
	Get(ctx context.Context, id domain.MovieID) (movie.Canon, error)
	Upsert(ctx context.Context, c movie.Canon) (domain.MovieID, error)
	MarkTMDBSynced(ctx context.Context, id domain.MovieID, now time.Time) error
}

// MovieI18nWriter is the enrichment-OWNED per-language movie_i18n upsert seam.
// Distinct from the discovery stub seeder (DO NOTHING on conflict): the worker
// owns the base-language row and refreshes it every hydrate (DoUpdates, COALESCE
// -guarded). Production impl is *enrichpersistence.MovieI18nSeeder.UpsertEnriched.
// nil-OK on MovieWorkerDeps — when nil the worker skips the localized-row write.
type MovieI18nWriter interface {
	UpsertEnriched(ctx context.Context, movieID domain.MovieID, lang, title, overview, tagline string, poster, backdrop *string, now time.Time) error
}

// MovieRefreshCandidate mirrors the persistence DTO into the app layer so the
// scheduler owns its own picker port without leaking GORM/domain-id types.
type MovieRefreshCandidate struct {
	MovieID int64
	Tier    enrichdomain.RefreshTier
}

// MovieRefreshPicker is the movie tiered picker port the MovieRefreshScheduler
// depends on. SEPARATE from RefreshPicker (series) so a movie tick never
// dequeues series candidates. Production impl wraps
// *MovieRepository.PickMovieRefreshCandidates. It reuses the domain RefreshTTL
// type but reads only .Normal (movies have a single non-changed staleness tier).
type MovieRefreshPicker interface {
	PickMovieRefreshCandidates(ctx context.Context, now time.Time, ttl enrichdomain.RefreshTTL, limit int) ([]MovieRefreshCandidate, error)
}

// MovieForceRefresher is the movie worker port the scheduler calls. Production:
// *MovieWorker via MovieWorker.HandleForced.
type MovieForceRefresher interface {
	HandleForced(ctx context.Context, movieID int64) error
}

// MovieRefreshMetrics is the narrow movie-scoped metric port. Movie metrics are
// a SEPARATE series family from the series refresh metrics so the two budgets
// are independently observable (movie ticks never inflate series counters).
// Production impl: observability.MovieRefreshMetrics. Tests pass a fake / rely
// on the noop default.
type MovieRefreshMetrics interface {
	IncRefresh(tier enrichdomain.RefreshTier, result string)
	ObserveBatchSize(n int)
	ObserveTickDuration(d time.Duration)
}

// noopMovieRefreshMetrics is the zero-value default so an unconfigured metrics
// port never panics. Mirrors noopRefreshMetrics.
type noopMovieRefreshMetrics struct{}

func (noopMovieRefreshMetrics) IncRefresh(enrichdomain.RefreshTier, string) {}
func (noopMovieRefreshMetrics) ObserveBatchSize(int)                        {}
func (noopMovieRefreshMetrics) ObserveTickDuration(time.Duration)           {}
