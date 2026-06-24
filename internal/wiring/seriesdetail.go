package wiring

import (
	"fmt"
	"log/slog"

	"github.com/alexmorbo/seasonfill/cmd/server/adapters"
	catalogpersistence "github.com/alexmorbo/seasonfill/internal/catalog/persistence"
	apppeople "github.com/alexmorbo/seasonfill/internal/enrichment/app/people"
	enrichpersistence "github.com/alexmorbo/seasonfill/internal/enrichment/persistence"
	enrichrest "github.com/alexmorbo/seasonfill/internal/enrichment/rest"
	"github.com/alexmorbo/seasonfill/internal/enrichment/rest/seriesrefresh"
	seriesdetail "github.com/alexmorbo/seasonfill/internal/seriesdetail/app"
	seriesdetailrest "github.com/alexmorbo/seasonfill/internal/seriesdetail/rest"
	"github.com/alexmorbo/seasonfill/internal/shared/clients/sonarr"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
	"github.com/alexmorbo/seasonfill/internal/shared/media"
	sharedports "github.com/alexmorbo/seasonfill/internal/shared/ports"
)

// seriesdetail.go owns the wiring for the seriesdetail bounded
// context: the Story 215 G-1 composer + handlers, the Story 216 H-1
// cast composer, the Story 217 H-2 people UC, and the Story 218 E-2
// series-refresh trigger. The MediaResolver constructed here is
// late-bound from server.go's LATE BIND ZONE (Story 316) and the
// PersonEnqueuerHolder is filled with the dispatcher once enrichment
// boots.

// SeriesDetailBundle groups the Story 215 (G-1) / 216 (H-1) / 217 (H-2) /
// 218 (E-2) series-detail components constructed at boot. Returned by
// BuildSeriesDetail. Threaded into:
//
//   - httpserver.NewServer (DetailHandler, SeasonHandler, CastHandler,
//     PeopleHandler, RefreshHandler) — the HTTP wirer remains in
//     server.go for now.
//   - server.go's LATE BIND ZONE block calls:
//   - MediaResolver.SetSideEffects(MediaEnqueuer, MediaOnDemand) after
//     wireEnrichment returns (Story 316 — the media pipeline doesn't
//     exist when the resolver is constructed).
//   - PersonEnqueuerHolder.Set(enrichBundle.Dispatcher) so both the
//     H-2 people use case AND the E-2 refresh path pick up the real
//     dispatcher (Story 217 / 218).
//
// Field-level invariants:
//
//   - MediaResolver is constructed WITHOUT enrichment side effects.
//     mediaBundle.AssetsRepo (nil-OK) satisfies the widened
//     media.HashLookupPort (HashForSourceURL + EnsurePending — story 320).
//     server.go's LATE BIND ZONE injects the enqueuer + on-demand fetcher
//     once enrichBundle is ready. (Story 526: resolver type moved to
//     internal/shared/media.)
//
//   - Composer + CastComposer share the same MediaResolver instance, so
//     the late-bound side effects propagate to both pipelines at once.
//
//   - The SonarrFor closure on the Composer captures sonarrBundle.Holder
//     so it observes the live instance map after every reload publish
//     (same pattern as torrentsync.go + regrab.go).
//
//   - PersonEnqueuerHolder is the shared late-binding dispatcher holder
//     used by BOTH PeopleUC (H-2) AND SeriesRefreshUC (E-2). Exposed on
//     the bundle so server.go can call Set(dispatcher) after enrichment
//     is wired. Until then the holder no-ops, so the use cases continue
//     to return 200 + degraded for stub persons on cold boot.
//
//   - 17 repositories are constructed locally off persistence.DB
//     (stateless GORM wrappers — same pattern as the pre-340 inline
//     body, which built its own instances even though scan.go +
//     enrichment had their own copies of the same set).
type SeriesDetailBundle struct {
	MediaResolver        *media.Resolver
	Composer             *seriesdetail.Composer
	CastComposer         *seriesdetail.CastComposer
	PeopleUC             *apppeople.UseCase
	SeriesRefreshUC      *seriesrefresh.UseCase
	DetailHandler        *seriesdetailrest.SeriesDetailHandler
	SeasonHandler        *seriesdetailrest.SeriesSeasonHandler
	CastHandler          *seriesdetailrest.SeriesCastHandler
	PeopleHandler        *enrichrest.PeopleHandler
	RefreshHandler       *enrichrest.SeriesRefreshHandler
	PersonEnqueuerHolder *adapters.PersonEnqueuerHolder
	// Story 491 / N-1a — global series surface.
	GlobalComposerUC    *seriesdetail.GlobalComposerUseCase
	TMDBFallbackUC      *seriesdetail.TMDBFallbackUseCase
	GlobalSeriesHandler *seriesdetailrest.GlobalSeriesHandler
}

// BuildSeriesDetail wires the Story 215 / 216 / 217 / 218 series-detail
// stack. Construction order mirrors the pre-340 inline body verbatim:
//
//  1. MediaResolver (sans side effects — late-bound in server.go).
//  2. 17 local repository handles (stateless GORM wrappers off db).
//  3. Composer (the detail/season pipeline) — captures SonarrFor closure
//     over sonarrBundle.Holder.
//  4. DetailHandler + SeasonHandler over the Composer.
//  5. PersonCreditsRepository + CastComposer (cast & crew).
//  6. CastHandler over the CastComposer.
//  7. PersonEnqueuerHolder (late-binding shared between H-2 and E-2).
//  8. PeopleUC over the holder + adapters.
//  9. PeopleHandler over PeopleUC.
//  10. SeriesRefreshUC over the holder + refresh adapters.
//  11. SeriesRefreshHandler over SeriesRefreshUC.
//
// Inputs:
//   - persistence: bedrock DB. All 17 repos are constructed off
//     persistence.DB (stateless GORM wrappers).
//   - sonarrBundle: Holder for the SonarrFor closure (composer port).
//   - mediaBundle: AssetsRepo for the MediaResolver lookup fallback.
//     A nil AssetsRepo inside the bundle is supported — the resolver
//     falls back to its embedded nop path and the frontend renders
//     monograms.
//   - log: shared logger.
//
// Only fallible step: seriesrefresh.New (validates Dispatcher !=
// nil — the holder is non-nil, so this never trips in production but
// the error is wrapped with the pre-340 message verbatim for parity).
func BuildSeriesDetail(
	persistence *PersistenceBundle,
	sonarrBundle *SonarrBundle,
	mediaBundle *MediaBundle,
	unifiedResolve bool,
	log *slog.Logger,
) (*SeriesDetailBundle, error) {
	db := persistence.DB
	holder := sonarrBundle.Holder

	// F-4b-6: single domain logger wrapped once per §6.5. The
	// seriesdetail bounded context anchors on the "composer" slot in
	// AllowedDomains. seriesrefresh re-uses the same slot per the
	// Story 397 sub-context bullet — the refresh trigger is the
	// write-side mirror of the composer (it re-enqueues the same data
	// sources the composer reads). All four composer-owned components
	// (MediaResolver, Composer, CastComposer, SeriesRefreshUC) take
	// composerLog. HTTP handlers + apppeople.UseCase (a SEPARATE
	// bounded context — H-2 person detail) stay on bare log.
	composerLog := sharedports.DomainLogger(log, "composer")

	// Story 312 + Story 320: media resolver for the seriesdetail composer.
	// nil-OK `mediaAssetsRepo` falls back to a nop resolver inside
	// media.NewResolver → every wire field stays nil and the frontend
	// renders monograms. *MediaAssetsRepository satisfies the widened
	// media.HashLookupPort (HashForSourceURL + EnsurePending) by virtue
	// of the new EnsurePending method (story 320). Story 526 — the
	// resolver type moved to internal/shared/media so discovery + other
	// contexts can share the same hash-translation surface.
	var mediaHashLookup media.HashLookupPort
	if mediaBundle != nil && mediaBundle.AssetsRepo != nil {
		mediaHashLookup = mediaBundle.AssetsRepo
	}
	// Story 316: enqueuer + fetcher are late-bound via SetSideEffects
	// after wireEnrichment returns — the media pipeline doesn't exist
	// yet at this point in boot.
	mediaResolver := media.NewResolver(mediaHashLookup, nil, nil, composerLog)
	// Story 347 — uniform always-emit-hash contract. Default-on; env
	// kill-switch (SEASONFILL_MEDIA_UNIFIED_RESOLVE=false) flips back
	// to legacy nil-on-miss without a redeploy.
	mediaResolver.SetUnifiedResolve(unifiedResolve)

	// Story 215 (G-1) — series detail composer + handlers. The repos
	// are stateless GORM wrappers around `db`, so re-constructing
	// them here is free; the enrichment block in server.go re-uses
	// its own instances of the same set for the worker pipeline.
	sdSeriesRepo := enrichpersistence.NewSeriesRepository(db)
	sdSeriesCacheRepo := catalogpersistence.NewSeriesCacheRepository(db, sdSeriesRepo)
	sdSeriesTextsRepo := enrichpersistence.NewSeriesTextsRepository(db)
	sdSeasonsRepo := enrichpersistence.NewSeasonsRepository(db)
	sdEpisodesRepo := enrichpersistence.NewEpisodesRepository(db)
	sdEpisodeStatesRepo := catalogpersistence.NewEpisodeStatesRepository(db)
	sdSeasonStatsRepo := catalogpersistence.NewSeasonStatsRepository(db)
	sdEpisodeTextsRepo := enrichpersistence.NewEpisodeTextsRepository(db)
	sdPeopleRepo := enrichpersistence.NewPeopleRepository(db)
	sdGenresRepo := enrichpersistence.NewGenresRepository(db)
	sdKeywordsRepo := enrichpersistence.NewKeywordsRepository(db)
	sdNetworksRepo := enrichpersistence.NewNetworksRepository(db)
	sdCompaniesRepo := enrichpersistence.NewCompaniesRepository(db)
	sdVideosRepo := enrichpersistence.NewVideosRepository(db)
	sdContentRatingsRepo := enrichpersistence.NewContentRatingsRepository(db)
	sdExternalIDsRepo := enrichpersistence.NewExternalIDsRepository(db)
	sdRecommendationsRepo := enrichpersistence.NewRecommendationsRepository(db)
	// 464b: real EnrichmentFreshnessPort backed by the live
	// EnrichmentErrorsRepository + canon series.enrichment_*_synced_at
	// columns. Replaces the legacy SyncLogStub the composer used to read
	// from during the 464a kernel cutover.
	sdEnrichmentErrorsRepo := enrichpersistence.NewEnrichmentErrorsRepository(db)
	sdFreshness := seriesdetail.NewEnrichmentFreshnessAdapter(sdSeriesRepo, sdEnrichmentErrorsRepo)

	// D-7 (468a) — the SeriesPeoplePort surface is now backed by
	// person_credits via SeriesPeopleFromPersonCredits. Constructed
	// here so the Composer, CastComposer, AND SeriesRefreshUC all
	// share the same adapter instance (stateless — re-using it is a
	// micro-optimisation that keeps wire shapes obviously identical
	// across the three readers).
	sdPersonCreditsRepo := enrichpersistence.NewPersonCreditsRepository(db)
	sdSeriesPeopleAdapter := adapters.NewSeriesPeopleFromPersonCredits(sdPersonCreditsRepo, sdSeriesRepo)

	composer := seriesdetail.NewComposer(seriesdetail.Deps{
		SeriesCache:       sdSeriesCacheRepo,
		SeriesCacheLookup: sdSeriesCacheRepo,
		Series:            sdSeriesRepo,
		SeriesTexts:       sdSeriesTextsRepo,
		Seasons:           sdSeasonsRepo,
		Episodes:          sdEpisodesRepo,
		EpisodeStates:     sdEpisodeStatesRepo,
		SeasonStats:       sdSeasonStatsRepo,
		EpisodeTexts:      sdEpisodeTextsRepo,
		SeriesPeople:      sdSeriesPeopleAdapter,
		People:            sdPeopleRepo,
		Genres:            sdGenresRepo,
		Keywords:          sdKeywordsRepo,
		Networks:          sdNetworksRepo,
		Companies:         sdCompaniesRepo,
		Videos:            sdVideosRepo,
		ContentRatings:    sdContentRatingsRepo,
		ExternalIDs:       sdExternalIDsRepo,
		Recommendations:   sdRecommendationsRepo,
		Freshness:         sdFreshness,
		SonarrFor: func(name domain.InstanceName) (seriesdetail.SonarrQueueLister, bool) {
			h := holder.Load()
			if h == nil {
				return nil, false
			}
			inst, ok := h[string(name)]
			if !ok || inst.Client == nil {
				return nil, false
			}
			concrete, ok := inst.Client.(*sonarr.Client)
			if !ok {
				return nil, false
			}
			return concrete, true
		},
		Logger:        composerLog,
		MediaResolver: mediaResolver,
	})
	detailHandler := seriesdetailrest.NewSeriesDetailHandler(composer, log)
	seasonHandler := seriesdetailrest.NewSeriesSeasonHandler(composer, log)

	// Story 216 (H-1) — full cast & crew composer. Reuses the 215
	// repos (series_cache + series + people) plus the new
	// EpisodesRepository.CountBySeries method and a thin adapter
	// projecting enrichpersistence.PersonCredit → composer-local
	// PersonCreditRef. D-7 (468a): SeriesPeople surface is backed by
	// the SeriesPeopleFromPersonCredits adapter constructed above,
	// shared with the Composer + SeriesRefreshUC.
	castComposer := seriesdetail.NewCastComposer(seriesdetail.CastDeps{
		SeriesCache:       sdSeriesCacheRepo,
		SeriesCacheLookup: sdSeriesCacheRepo,
		Series:            sdSeriesRepo,
		SeriesPeople:      sdSeriesPeopleAdapter,
		People:            sdPeopleRepo,
		PersonCredits:     adapters.NewPersonCreditsAdapter(sdPersonCreditsRepo),
		EpisodesCount:     sdEpisodesRepo,
		Logger:            composerLog,
		MediaResolver:     mediaResolver,
	})
	castHandler := seriesdetailrest.NewSeriesCastHandler(castComposer, log)

	// Story 217 (H-2) — person detail use case. Adapter wraps
	// PeopleRepository so the application port distinguishes the
	// bio-skipping GetByTMDBID path (hot, used for the tmdb→id
	// resolution) from the bio-resolving GetWithBio path (cold,
	// used after id is known) — same repository, two narrow
	// methods. The Enqueuer is a late-binding holder; the real
	// dispatcher is wired in after wireEnrichment returns (the
	// holder's inner is nil-OK and the use case logs a warn line
	// when stub persons land before the dispatcher is up).
	peopleEnqueuerHolder := adapters.NewPersonEnqueuerHolder()
	peopleUC := apppeople.NewUseCase(apppeople.Deps{
		People:        adapters.NewPeopleReaderAdapter(sdPeopleRepo),
		PersonCredits: adapters.NewPersonCreditsReaderAdapter(sdPersonCreditsRepo),
		SeriesByTMDB:  sdSeriesRepo,
		SeriesCache:   sdSeriesCacheRepo,
		Enqueuer:      peopleEnqueuerHolder,
		MediaResolver: mediaResolver,
		// F-4b-8: people UC composes person-detail responses under the
		// seriesdetail composer pipe — anchors on the "composer" slot.
		Logger: composerLog,
	})
	peopleHandler := enrichrest.NewPeopleHandler(peopleUC, log)

	// Story 218 (E-2) — series refresh trigger. Reuses the
	// peopleEnqueuerHolder so the same late-binding dispatcher
	// satisfies both the H-2 use case AND the refresh path.
	seriesRefreshUC, err := seriesrefresh.New(seriesrefresh.Deps{
		SeriesCache:  sdSeriesCacheRepo,
		Series:       adapters.NewSeriesRefreshSeriesAdapter(sdSeriesRepo),
		SeriesPeople: adapters.NewSeriesRefreshCastAdapter(sdSeriesPeopleAdapter),
		Dispatcher:   peopleEnqueuerHolder,
		Logger:       composerLog,
	})
	if err != nil {
		return nil, fmt.Errorf("seriesrefresh use case: %w", err)
	}
	seriesRefreshHandler := enrichrest.NewSeriesRefreshHandler(seriesRefreshUC, log)

	// Story 491 / N-1a — global series composer + handler. The
	// TMDBFallback reads from the same canon series repo as the per-
	// instance composer; the MediaResolver is shared (same pointer) so
	// late-bind side effects apply identically.
	tmdbFallbackUC, err := seriesdetail.NewTMDBFallbackUseCase(seriesdetail.TMDBFallbackDeps{
		Series:        sdSeriesRepo,
		MediaResolver: mediaResolver,
		Logger:        composerLog,
	})
	if err != nil {
		return nil, fmt.Errorf("tmdb fallback use case: %w", err)
	}
	globalComposerUC, err := seriesdetail.NewGlobalComposerUseCase(seriesdetail.GlobalComposerDeps{
		CacheLookup:  sdSeriesCacheRepo,
		Composer:     composer,
		TMDBFallback: tmdbFallbackUC,
		Logger:       composerLog,
	})
	if err != nil {
		return nil, fmt.Errorf("global composer use case: %w", err)
	}
	globalSeriesHandler := seriesdetailrest.NewGlobalSeriesHandler(
		globalComposerUC,
		sdSeriesCacheRepo,
		seriesRefreshUC,
		log,
	)

	return &SeriesDetailBundle{
		MediaResolver:        mediaResolver,
		Composer:             composer,
		CastComposer:         castComposer,
		PeopleUC:             peopleUC,
		SeriesRefreshUC:      seriesRefreshUC,
		DetailHandler:        detailHandler,
		SeasonHandler:        seasonHandler,
		CastHandler:          castHandler,
		PeopleHandler:        peopleHandler,
		RefreshHandler:       seriesRefreshHandler,
		PersonEnqueuerHolder: peopleEnqueuerHolder,
		GlobalComposerUC:     globalComposerUC,
		TMDBFallbackUC:       tmdbFallbackUC,
		GlobalSeriesHandler:  globalSeriesHandler,
	}, nil
}
