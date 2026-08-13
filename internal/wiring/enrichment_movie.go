package wiring

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"time"

	"github.com/alexmorbo/seasonfill/cmd/server/adapters"
	catalogpersistence "github.com/alexmorbo/seasonfill/internal/catalog/persistence"
	appenrich "github.com/alexmorbo/seasonfill/internal/enrichment/app"
	enrichdomain "github.com/alexmorbo/seasonfill/internal/enrichment/domain/enrichment"
	enrichpersistence "github.com/alexmorbo/seasonfill/internal/enrichment/persistence"
	"github.com/alexmorbo/seasonfill/internal/observability"
	"github.com/alexmorbo/seasonfill/internal/shared/clients/tmdb"
	"github.com/alexmorbo/seasonfill/internal/shared/media"
)

// enrichment_movie.go wires the movie enrichment vertical (Ф6-R-4a L3-2): the
// movie TMDB hydration worker, the SEPARATE movie refresh scheduler (own budget/
// ticker), and the /movie/changes firehose poller (generic ChangesPoller reused
// with movie deps + a dedicated cursor row). Nothing here edits TV wiring —
// mirror-only adapters bridge the runtime TMDB holder onto the movie ports.

// MovieEnrichmentBundle groups the movie background loops. server.go's LATE BIND
// ZONE owns the two lifecycle.Go goroutines (movie-refresh-scheduler +
// movie-changes-poller); each field is nil-safe to skip when construction failed.
type MovieEnrichmentBundle struct {
	// RefreshScheduler — SEPARATE budget/ticker from the series RefreshScheduler.
	// A movie tick never dequeues series candidates (isolation guarantee).
	RefreshScheduler *appenrich.MovieRefreshScheduler
	// ChangesPoller — the GENERIC *ChangesPoller wired with movie deps (movie
	// changes lister + MovieRepository.MarkChangedByTMDBIDs + the dedicated
	// movie_changes_state cursor). Zero TV edits. nil when construction errored.
	ChangesPoller *appenrich.ChangesPoller
}

// MovieEnrichmentDeps is the construction surface for BuildMovieEnrichment.
type MovieEnrichmentDeps struct {
	Persistence *PersistenceBundle
	// TMDBHolder is the runtime-swappable TMDB client holder (shared with the
	// series enrichment stack). The movie worker + changes lister load it per
	// call so a runtime key swap is picked up without a rebuild.
	TMDBHolder *adapters.TMDBClientHolder
	// Resolver — optional shared *media.Resolver; the worker mints media_assets
	// pending rows for poster/backdrop as a pre-warm side-effect. Nil-OK.
	Resolver *media.Resolver
	// OMDbHolder — runtime-swappable OMDb client holder (shared with the series
	// OMDb worker; same daily quota / API key). Nil-OK: when nil OR empty the
	// movie OMDb follow-up is disabled and movie hydration is TMDB-only. Ф6-R-4a
	// L3-3.
	OMDbHolder *adapters.OMDbClientHolder
	Log        *slog.Logger
}

// BuildMovieEnrichment constructs the movie worker + scheduler + changes poller.
// The movie refresh BatchSize is env-tunable (SEASONFILL_MOVIE_REFRESH_BATCH,
// default 50) mirroring the series family; the ticker intervals are owned by
// server.go's RunForever / RunChanges calls (SEASONFILL_MOVIE_REFRESH_INTERVAL /
// SEASONFILL_MOVIE_CHANGES_INTERVAL). Returns an error only on a genuine
// misconfiguration (worker/scheduler construction) — the changes poller degrades
// to nil (logged) rather than failing the whole bundle.
func BuildMovieEnrichment(deps MovieEnrichmentDeps) (*MovieEnrichmentBundle, error) {
	switch {
	case deps.Persistence == nil:
		return nil, fmt.Errorf("BuildMovieEnrichment: Persistence required")
	case deps.TMDBHolder == nil:
		return nil, fmt.Errorf("BuildMovieEnrichment: TMDBHolder required")
	case deps.Log == nil:
		return nil, fmt.Errorf("BuildMovieEnrichment: Log required")
	}

	movies := enrichpersistence.NewMovieRepository(deps.Persistence.DB)
	i18n := enrichpersistence.NewMovieI18nSeeder(deps.Persistence.DB)
	cursor := enrichpersistence.NewMovieChangesStateRepository(deps.Persistence.DB)
	movieCollections := enrichpersistence.NewMovieCollectionsRepository(deps.Persistence.DB)

	// Nil-OK: only set the port when the concrete resolver is non-nil so a nil
	// *media.Resolver never becomes a non-nil interface wrapping a nil pointer.
	var resolver appenrich.MediaResolver
	if deps.Resolver != nil {
		resolver = deps.Resolver
	}

	// Ф6-R-4a L3-3: movie OMDb ratings follow-up worker. Only wired when the OMDb
	// holder exists (OMDb configured at boot / enabled at runtime). Reuses the
	// runtime-swappable holder's Get getter (same seam the series OMDb worker
	// uses) so a key swap is picked up without a rebuild. nil handler ⇒ movie
	// hydration is TMDB-only.
	var omdbHandler appenrich.MovieOMDbHandler
	if deps.OMDbHolder != nil {
		omdbWorker, oerr := appenrich.NewMovieOMDbWorker(appenrich.MovieOMDbWorkerDeps{
			Client: deps.OMDbHolder.Get,
			Movies: movies,
			Logger: deps.Log,
		})
		if oerr != nil {
			deps.Log.WarnContext(context.Background(), "enrichment.movie_omdb.disabled",
				slog.String("error", oerr.Error()))
		} else {
			omdbHandler = omdbWorker
		}
	}

	// Ф6-R-5: collection populate step. Constructed unconditionally (all deps are
	// non-nil here); a construction error degrades to a nil populator (movie
	// hydration stays collection-free) rather than failing the whole bundle.
	var collectionPopulator appenrich.MovieCollectionPopulator
	collectionWorker, cwErr := appenrich.NewMovieCollectionWorker(appenrich.MovieCollectionWorkerDeps{
		TMDB:        movieCollectionTMDBFromHolder{holder: deps.TMDBHolder},
		Collections: movieCollections,
		Movies:      movies,
		BaseLang:    tmdb.DefaultLanguage,
		Logger:      deps.Log,
	})
	if cwErr != nil {
		deps.Log.WarnContext(context.Background(), "enrichment.movie_collection.disabled",
			slog.String("error", cwErr.Error()))
	} else {
		collectionPopulator = collectionWorker
	}

	peopleRepo := enrichpersistence.NewPeopleRepository(deps.Persistence.DB)
	personCredits := PersonCreditsRepoAdapter{Inner: enrichpersistence.NewPersonCreditsRepository(deps.Persistence.DB)}
	tx := catalogpersistence.NewGormTransactor(deps.Persistence.DB)

	genresRepo := enrichpersistence.NewGenresRepository(deps.Persistence.DB)
	genresI18n := enrichpersistence.NewGenresI18nRepository(deps.Persistence.DB)
	keywordsRepo := enrichpersistence.NewKeywordsRepository(deps.Persistence.DB)
	keywordsI18n := enrichpersistence.NewKeywordsI18nRepository(deps.Persistence.DB)
	companiesRepo := enrichpersistence.NewCompaniesRepository(deps.Persistence.DB)

	worker, err := appenrich.NewMovieWorker(appenrich.MovieWorkerDeps{
		TMDB:          movieTMDBFromHolder{holder: deps.TMDBHolder},
		Movies:        movies,
		I18n:          i18n,
		Resolver:      resolver,
		OMDb:          omdbHandler,
		Collections:   collectionPopulator,
		People:        peopleRepo,
		PersonCredits: personCredits,
		Tx:            tx,
		Genres:        GenresRepoAdapter{Main: genresRepo, I18n: genresI18n},
		Keywords:      KeywordsRepoAdapter{Main: keywordsRepo, I18n: keywordsI18n},
		Companies:     companiesRepo,
		BaseLang:      tmdb.DefaultLanguage,
		Logger:        deps.Log,
	})
	if err != nil {
		return nil, fmt.Errorf("wire movie worker: %w", err)
	}

	batchSize := 50
	if v := os.Getenv("SEASONFILL_MOVIE_REFRESH_BATCH"); v != "" {
		if n, perr := strconv.Atoi(v); perr == nil && n > 0 {
			batchSize = n
		} else {
			deps.Log.WarnContext(context.Background(), "enrichment.movie_refresh.batch.invalid_env",
				slog.String("value", v), slog.Int("default", batchSize))
		}
	}

	scheduler, err := appenrich.NewMovieRefreshScheduler(appenrich.MovieRefreshSchedulerDeps{
		Picker:    movieRefreshPickerAdapter{inner: movies},
		Worker:    worker,
		BatchSize: batchSize,
		Metrics:   observability.NewMovieRefreshMetrics(),
		Logger:    deps.Log,
	})
	if err != nil {
		return nil, fmt.Errorf("wire movie refresh scheduler: %w", err)
	}

	// /movie/changes poller — the GENERIC ChangesPoller wired with movie deps.
	// Marker: *MovieRepository satisfies appenrich.ChangedSeriesMarker directly.
	// CursorStore: *MovieChangesStateRepository (dedicated movie_changes_state
	// row → no /tv/changes cursor collision). ClientReady mirrors the series
	// ShouldSweep holder-gate. On construction error we log + leave nil.
	var changesPoller *appenrich.ChangesPoller
	cp, cerr := appenrich.NewChangesPoller(appenrich.ChangesPollerDeps{
		Lister:      movieChangesListerFromHolder{holder: deps.TMDBHolder},
		Marker:      movies,
		CursorStore: cursor,
		Metrics:     observability.NewMovieChangesMetrics(),
		ClientReady: func() bool { return deps.TMDBHolder.Load() != nil },
		Logger:      deps.Log,
	})
	if cerr != nil {
		deps.Log.WarnContext(context.Background(), "enrichment.movie_changes_poller.disabled",
			slog.String("error", cerr.Error()))
	} else {
		changesPoller = cp
	}

	return &MovieEnrichmentBundle{
		RefreshScheduler: scheduler,
		ChangesPoller:    changesPoller,
	}, nil
}

// ---- wiring-local port adapters -------------------------------------------

// movieTMDBFromHolder adapts the runtime-swappable TMDB holder to
// appenrich.MovieTMDBClient (GetMovie). Load() per call keeps it swap-safe;
// when the holder is empty it returns ErrTMDBClientNotReady (the scheduler's
// worker error path logs + counts it).
type movieTMDBFromHolder struct {
	holder *adapters.TMDBClientHolder
}

func (a movieTMDBFromHolder) GetMovie(ctx context.Context, id int64, language string) (*tmdb.MovieResponse, error) {
	c := a.holder.Load()
	if c == nil {
		return nil, adapters.ErrTMDBClientNotReady
	}
	return c.GetMovie(ctx, id, language)
}

// movieCollectionTMDBFromHolder adapts the runtime-swappable TMDB holder to
// appenrich.CollectionTMDBClient (GetCollection). Load() per call keeps it
// swap-safe (mirror of movieTMDBFromHolder). Ф6-R-5.
type movieCollectionTMDBFromHolder struct {
	holder *adapters.TMDBClientHolder
}

func (a movieCollectionTMDBFromHolder) GetCollection(ctx context.Context, id int64, language string) (*tmdb.CollectionResponse, error) {
	c := a.holder.Load()
	if c == nil {
		return nil, adapters.ErrTMDBClientNotReady
	}
	return c.GetCollection(ctx, id, language)
}

// movieChangesListerFromHolder adapts the TMDB holder to the GENERIC
// appenrich.TVChangesLister port — its GetTVChangesPage method delegates to the
// movie firehose (c.GetMovieChangesPage). This is the "adapter satisfying the
// changes-lister iface" that lets the movie poller reuse the generic
// ChangesPoller with ZERO TV edits (no renamed port).
type movieChangesListerFromHolder struct {
	holder *adapters.TMDBClientHolder
}

func (a movieChangesListerFromHolder) GetTVChangesPage(ctx context.Context, start, end time.Time, page int) (tmdb.ChangedIDsPage, error) {
	c := a.holder.Load()
	if c == nil {
		return tmdb.ChangedIDsPage{}, adapters.ErrTMDBClientNotReady
	}
	return c.GetMovieChangesPage(ctx, start, end, page)
}

// movieRefreshPickerAdapter wraps *MovieRepository.PickMovieRefreshCandidates to
// satisfy appenrich.MovieRefreshPicker, mapping the persistence DTO
// (domain.MovieID) to the app-port shape (int64). Mirror of refreshPickerAdapter.
type movieRefreshPickerAdapter struct {
	inner *enrichpersistence.MovieRepository
}

func (a movieRefreshPickerAdapter) PickMovieRefreshCandidates(
	ctx context.Context,
	now time.Time,
	ttl enrichdomain.RefreshTTL,
	limit int,
) ([]appenrich.MovieRefreshCandidate, error) {
	rows, err := a.inner.PickMovieRefreshCandidates(ctx, now, ttl, limit)
	if err != nil {
		return nil, err
	}
	out := make([]appenrich.MovieRefreshCandidate, 0, len(rows))
	for _, r := range rows {
		out = append(out, appenrich.MovieRefreshCandidate{
			MovieID: int64(r.MovieID),
			Tier:    r.Tier,
		})
	}
	return out, nil
}

// compile-time assertions that the movie repo satisfies the generic poller's
// Marker/CursorStore seams (mirror of the series ports_assert checks).
var (
	_ appenrich.ChangedSeriesMarker      = (*enrichpersistence.MovieRepository)(nil)
	_ appenrich.ChangesCursorStore       = (*enrichpersistence.MovieChangesStateRepository)(nil)
	_ appenrich.MovieCanonRepo           = (*enrichpersistence.MovieRepository)(nil)
	_ appenrich.MovieI18nWriter          = (*enrichpersistence.MovieI18nSeeder)(nil)
	_ appenrich.MovieOMDbRepo            = (*enrichpersistence.MovieRepository)(nil)
	_ appenrich.MovieOMDbHandler         = (*appenrich.MovieOMDbWorker)(nil)
	_ appenrich.MovieCollectionUpserter  = (*enrichpersistence.MovieCollectionsRepository)(nil)
	_ appenrich.MovieCollectionPopulator = (*appenrich.MovieCollectionWorker)(nil)
	_ appenrich.PeopleRepo               = (*enrichpersistence.PeopleRepository)(nil)
	_ appenrich.Transactor               = (*catalogpersistence.GormTransactor)(nil)
	_ appenrich.MovieGenresWriter        = GenresRepoAdapter{}
	_ appenrich.MovieKeywordsWriter      = KeywordsRepoAdapter{}
	_ appenrich.MovieCompaniesWriter     = (*enrichpersistence.CompaniesRepository)(nil)
)
