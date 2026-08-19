package wiring

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"gorm.io/gorm"

	"github.com/alexmorbo/seasonfill/cmd/server/adapters"
	catalogpersistence "github.com/alexmorbo/seasonfill/internal/catalog/persistence"
	catalogrest "github.com/alexmorbo/seasonfill/internal/catalog/rest"
	appenrich "github.com/alexmorbo/seasonfill/internal/enrichment/app"
	enrichpersistence "github.com/alexmorbo/seasonfill/internal/enrichment/persistence"
	mdadapters "github.com/alexmorbo/seasonfill/internal/mediadetail/adapters"
	mdapp "github.com/alexmorbo/seasonfill/internal/moviedetail/app"
	mdrest "github.com/alexmorbo/seasonfill/internal/moviedetail/rest"
	"github.com/alexmorbo/seasonfill/internal/shared/clients/tmdb"
	ports "github.com/alexmorbo/seasonfill/internal/shared/dataports"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
	"github.com/alexmorbo/seasonfill/internal/shared/media"
	sharedports "github.com/alexmorbo/seasonfill/internal/shared/ports"
)

// MovieDetailBundle groups the movie read handlers (Ф6-R-6a detail +
// Ф6-R-6b library list + Ф2.1 cast sub-endpoint).
type MovieDetailBundle struct {
	Handler                *mdrest.Handler
	LibraryHandler         *catalogrest.MovieLibraryHandler
	CastHandler            *mdrest.MovieCastHandler            // Ф2.1
	CastETagFreshness      *mdapp.MovieETagFreshnessAdapter    // Ф2.1 first live movie ETag wiring
	OverviewHandler        *mdrest.MovieOverviewHandler        // Ф2.2 movie overview sub-endpoint
	RatingsHandler         *mdrest.MovieRatingsHandler         // Ф2.3 movie ratings sub-endpoint
	RecommendationsHandler *mdrest.MovieRecommendationsHandler // Ф2.4 movie recommendations sub-endpoint
	// FreshenerHolder — S1a read-through freshener. Constructed here WITHOUT a
	// worker (BuildMovieDetail runs before BuildMovieEnrichment); server.go's
	// late-bind zone calls FreshenerHolder.Set(movieEnrich.Worker).
	FreshenerHolder *mdapp.MovieFreshener
	// HotEnqueuer — S1b Hot-lane enqueuer holder. Constructed here WITHOUT a
	// dispatcher (BuildMovieDetail has no dispatcher in scope); server.go's
	// late-bind zone calls HotEnqueuer.Set(adapter over enrichBundle.Dispatcher).
	HotEnqueuer *mdapp.MovieHotEnqueuer
	// StubResolverHolder — S2 stub-create-on-view resolver holder. Constructed
	// here WITHOUT a TMDB holder (BuildMovieDetail has no TMDB holder in scope);
	// server.go's late-bind zone calls StubResolverHolder.Set(adapter over the
	// runtime TMDB holder + the discovery movie seed insert). Until Set, an
	// unknown tmdb id keeps the pre-S2 404.
	StubResolverHolder *mdapp.MovieStubResolverHolder
	// EngineFreshener — ADR-0022 S2a: the engine-backed freshenerPort REPLACING the
	// raw *MovieFreshener as the movie-text driver. Late-bound with the engine in
	// server.go after BuildMediaDetail.
	EngineFreshener *mdadapters.MovieEngineFreshener
}

// BuildMovieDetail wires the read-only movie-detail aggregate + the movie
// library list, both over local repos (movies canon + movie_states).
func BuildMovieDetail(db *gorm.DB, resolver *media.Resolver, log *slog.Logger) *MovieDetailBundle {
	domainLog := sharedports.DomainLogger(log, "http")
	movieRepo := enrichpersistence.NewMovieRepository(db)
	// Keep the legacy freshener constructed (still returned as FreshenerHolder for
	// the movie-worker late-bind reuse; unused-for-text — cleanup story), and add
	// the engine-backed adapter that REPLACES it as the usecase driver.
	freshener := mdapp.NewMovieFreshener(5*time.Second, time.Now, domainLog).
		WithI18nCoverage(enrichpersistence.NewMovieI18nReadRepository(db))
	engineFreshener := mdadapters.NewMovieEngineFreshener()
	hotEnqueuer := mdapp.NewMovieHotEnqueuer()
	stubResolverHolder := mdapp.NewMovieStubResolverHolder()
	uc := mdapp.New(
		movieRepo,
		enrichpersistence.NewMovieI18nReadRepository(db),
		enrichpersistence.NewMovieCollectionsRepository(db),
		catalogpersistence.NewMovieStatesRepository(db),
	).
		WithHydrationTrigger(movieRepo, time.Now, domainLog).
		WithFreshener(engineFreshener).
		WithEnrichmentEnqueuer(hotEnqueuer).
		WithStubResolver(stubResolverHolder).
		WithTaxonomy(
			enrichpersistence.NewGenresRepository(db),
			enrichpersistence.NewKeywordsRepository(db),
		).
		WithSidebar(
			enrichpersistence.NewCompaniesRepository(db),
			enrichpersistence.NewMovieVideosRepository(db),
		)
	// Ф6-R-6b — global movie library list (GET /api/v1/movies).
	movieI18nRead := enrichpersistence.NewMovieI18nReadRepository(db)
	libraryHandler := catalogrest.NewMovieLibraryHandler(
		catalogpersistence.NewMovieLibraryRepository(db), resolver, domainLog).
		WithLocalizer(movieI18nRead)
	// Ф2.1 — movie cast sub-endpoint. Reuses the person_credits reverse reader +
	// people name reader; movieI18nRead drives served_language via TitleLanguage.
	castUC := mdapp.NewCastUseCase(
		movieRepo,
		enrichpersistence.NewPersonCreditsRepository(db),
		enrichpersistence.NewPeopleRepository(db),
		movieI18nRead,
	)
	castHandler := mdrest.NewMovieCastHandler(castUC, resolver, domainLog)
	castETag := mdapp.NewMovieETagFreshnessAdapter(movieRepo)
	// Ф2.2 — movie overview sub-endpoint. movieI18nRead drives both the per-field
	// text ladder (Get) and served_language (TitleLanguage); movieRepo = canon.
	overviewUC := mdapp.NewOverviewUseCase(movieRepo, movieI18nRead, movieI18nRead)
	overviewHandler := mdrest.NewMovieOverviewHandler(overviewUC, domainLog)
	// Ф2.3 — movie ratings sub-endpoint. Read-only over the canon row (movieRepo);
	// no localization, no ETag, no live refresh.
	ratingsUC := mdapp.NewRatingsUseCase(movieRepo)
	ratingsHandler := mdrest.NewMovieRatingsHandler(ratingsUC, domainLog)
	// Ф2.4 — movie recommendations sub-endpoint. Read-only over local repos:
	// movie_recommendations rank list + movies canon batch (movieRepo doubles as
	// CanonReader and MovieCanonBatchReader). Reuses the movie ETag adapter (recs
	// section → enrichment_recs_synced_at) already built as castETag.
	recsUC := mdapp.NewRecommendationsUseCase(
		movieRepo,
		enrichpersistence.NewMovieRecommendationsRepository(db),
		movieRepo,
		movieI18nRead,
	).
		WithFreshener(engineFreshener)
	recsHandler := mdrest.NewMovieRecommendationsHandler(recsUC, resolver, domainLog)
	return &MovieDetailBundle{
		Handler:                mdrest.NewHandler(uc, resolver, domainLog),
		LibraryHandler:         libraryHandler,
		CastHandler:            castHandler,
		CastETagFreshness:      castETag,
		OverviewHandler:        overviewHandler,
		RatingsHandler:         ratingsHandler,
		RecommendationsHandler: recsHandler,
		FreshenerHolder:        freshener,
		EngineFreshener:        engineFreshener,
		HotEnqueuer:            hotEnqueuer,
		StubResolverHolder:     stubResolverHolder,
	}
}

// movieHotEnqueuerAdapter bridges the moviedetail EnrichmentEnqueuer port onto
// the enrichment dispatcher's Enqueue, pushing movie hydration jobs onto the
// interactive Hot lane (EntityMovie/PriorityHot). Lives here (not in
// moviedetail/app) so that package never imports enrichment/app — the port is
// satisfied structurally at this wiring seam. dispatch is the runtime dispatcher
// (or its holder), non-nil at the late-bind seam in cmd/server.
type movieHotEnqueuerAdapter struct {
	dispatch appenrich.Dispatcher
}

// NewMovieHotEnqueuerAdapter wraps the dispatcher as a moviedetail
// EnrichmentEnqueuer. Returned as the port type so cmd/server can Set it into
// the MovieHotEnqueuer holder without importing the concrete adapter.
func NewMovieHotEnqueuerAdapter(dispatch appenrich.Dispatcher) mdapp.EnrichmentEnqueuer {
	return movieHotEnqueuerAdapter{dispatch: dispatch}
}

func (a movieHotEnqueuerAdapter) EnqueueMovieHot(movieID domain.MovieID) {
	a.dispatch.Enqueue(appenrich.EntityMovie, int64(movieID), appenrich.PriorityHot)
}

// ---- S2 stub-create-on-view resolver (moviedetail) ------------------------

// movieDetailStubTMDB is the narrow runtime TMDB lookup the S2 resolver validates
// against. movieTMDBFromHolder (enrichment_movie.go) satisfies it — Load() per
// call keeps it swap-safe.
type movieDetailStubTMDB interface {
	GetMovie(ctx context.Context, id int64, language string) (*tmdb.MovieResponse, error)
}

// movieDetailStubWriter is the narrow seed insert the S2 resolver reuses.
// *movieStubUpserterAdapter (discovery_movie.go) satisfies it — the SAME
// COALESCE-guarded canon Upsert + movie_i18n seeder the discovery search path
// uses, so a stub-on-view and a discovery-seed converge on ONE idempotent row.
type movieDetailStubWriter interface {
	EnsureMovieStub(ctx context.Context, tmdbID domain.TMDBID, lang, title, originalTitle, originalLanguage string, poster, backdrop *string) (domain.MovieID, error)
}

// movieStubResolverAdapter satisfies mdapp.MovieStubResolver. It validates the
// tmdb id against TMDB (GetMovie) and, ONLY when TMDB resolves it, materialises a
// minimal seeded stub via the discovery seed insert. A TMDB not-found (or an
// empty payload) maps to ports.ErrNotFound so the moviedetail read keeps its 404
// and NO junk row is written. Any other TMDB error surfaces as-is (→ 500). The S1
// worker / freshener then hydrate the fresh stub off-request.
type movieStubResolverAdapter struct {
	tmdb   movieDetailStubTMDB
	writer movieDetailStubWriter
	log    *slog.Logger
}

// NewMovieStubResolverAdapter wires the S2 resolver over the runtime TMDB holder
// + the discovery movie seed insert (COALESCE Upsert + movie_i18n seeder).
// Returned as the mdapp.MovieStubResolver port so cmd/server can Set it into the
// MovieStubResolverHolder without importing the concrete adapter. log MUST already
// carry a domain tag.
func NewMovieStubResolverAdapter(holder *adapters.TMDBClientHolder, db *gorm.DB, log *slog.Logger) mdapp.MovieStubResolver {
	return &movieStubResolverAdapter{
		tmdb: movieTMDBFromHolder{holder: holder},
		writer: &movieStubUpserterAdapter{
			movies: enrichpersistence.NewMovieRepository(db),
			i18n:   enrichpersistence.NewMovieI18nSeeder(db),
		},
		log: log,
	}
}

// EnsureStub validates tmdbID against TMDB and seeds a minimal stub when it
// resolves. TMDB not-found / empty payload → ports.ErrNotFound (no row written).
func (a *movieStubResolverAdapter) EnsureStub(ctx context.Context, tmdbID domain.TMDBID, lang string) error {
	resp, err := a.tmdb.GetMovie(ctx, int64(tmdbID), lang)
	if err != nil {
		if tmdb.IsNotFound(err) {
			return fmt.Errorf("moviedetail stub: tmdb %d not found: %w", int64(tmdbID), ports.ErrNotFound)
		}
		return fmt.Errorf("moviedetail stub: tmdb lookup %d: %w", int64(tmdbID), err)
	}
	if resp == nil || resp.Title == "" {
		return fmt.Errorf("moviedetail stub: tmdb %d empty payload: %w", int64(tmdbID), ports.ErrNotFound)
	}
	var poster, backdrop *string
	if resp.PosterPath != "" {
		p := resp.PosterPath
		poster = &p
	}
	if resp.BackdropPath != "" {
		b := resp.BackdropPath
		backdrop = &b
	}
	if _, serr := a.writer.EnsureMovieStub(ctx, tmdbID, lang, resp.Title, resp.OriginalTitle, resp.OriginalLanguage, poster, backdrop); serr != nil {
		return fmt.Errorf("moviedetail stub: ensure %d: %w", int64(tmdbID), serr)
	}
	if a.log != nil {
		a.log.InfoContext(ctx, "moviedetail.stub_created_on_view",
			slog.Int64("tmdb_id", int64(tmdbID)),
			slog.String("lang", lang),
			slog.String("title", resp.Title))
	}
	return nil
}
