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
	"github.com/alexmorbo/seasonfill/internal/enrichment/domain/taxonomy"
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
	// MarkCastSynced stamps movies.enrichment_cast_synced_at = now on a successful
	// cast write (Ф1.1a). Called inside the cast-write tx so the clock and the
	// person_credits rows commit atomically.
	MarkCastSynced(ctx context.Context, id domain.MovieID, now time.Time) error
	// MarkKeywordsSynced stamps movies.enrichment_keywords_synced_at = now on a successful
	// keyword write (Ф1.1b). Called inside the keyword-write tx so the clock and the
	// movie_keywords rows commit atomically.
	MarkKeywordsSynced(ctx context.Context, id domain.MovieID, now time.Time) error
}

// MovieI18nWriter is the enrichment-OWNED per-language movie_i18n upsert seam.
// Distinct from the discovery stub seeder (DO NOTHING on conflict): the worker
// owns the base-language row and refreshes it every hydrate (DoUpdates, COALESCE
// -guarded). Production impl is *enrichpersistence.MovieI18nSeeder.UpsertEnriched.
// nil-OK on MovieWorkerDeps — when nil the worker skips the localized-row write.
type MovieI18nWriter interface {
	UpsertEnriched(ctx context.Context, movieID domain.MovieID, lang, title, overview, tagline string, poster, backdrop *string, now time.Time) error
}

// MovieCollectionPopulator is the Ф6-R-5 collection-populate seam. Production
// impl: *MovieCollectionWorker.PopulateCollection. Kept a narrow one-method port
// (mirror of MovieOMDbHandler) so the hydration worker never imports the
// collections persistence/TMDB collection surface directly. nil-OK on
// MovieWorkerDeps — when nil the collection populate step is disabled (exact
// pre-R-5 behavior).
type MovieCollectionPopulator interface {
	PopulateCollection(ctx context.Context, collectionTMDBID int) error
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

// MovieGenresWriter is the movie genre taxonomy write surface (Ф1.1b): seed the shared
// `genres` dict by tmdb_id (Upsert → local id), seed the base-lang genres_i18n row
// (UpsertI18n), and DELETE+INSERT the movie_genres join (SetMovie). Production impl:
// wiring.GenresRepoAdapter (composes *GenresRepository + *GenresI18nRepository). nil-OK on
// MovieWorkerDeps — when nil the worker skips the genre write.
type MovieGenresWriter interface {
	Upsert(ctx context.Context, g taxonomy.Genre) (int64, error)
	UpsertI18n(ctx context.Context, genreID int64, language, name string) error
	SetMovie(ctx context.Context, movieID domain.MovieID, genreIDs []int64) error
}

// MovieKeywordsWriter mirrors MovieGenresWriter for keywords. Production impl:
// wiring.KeywordsRepoAdapter. nil-OK.
type MovieKeywordsWriter interface {
	Upsert(ctx context.Context, k taxonomy.Keyword) (int64, error)
	UpsertI18n(ctx context.Context, keywordID int64, language, name string) error
	SetMovie(ctx context.Context, movieID domain.MovieID, keywordIDs []int64) error
}

// MovieCompaniesWriter is the movie production-company write surface (Ф1.1b). Companies have
// no i18n table, so no UpsertI18n. Production impl: *enrichpersistence.CompaniesRepository
// (satisfies this directly). nil-OK.
type MovieCompaniesWriter interface {
	Upsert(ctx context.Context, c taxonomy.ProductionCompany) (int64, error)
	SetMovie(ctx context.Context, movieID domain.MovieID, companyIDs []int64) error
}
