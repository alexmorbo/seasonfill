package wiring

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"time"

	"github.com/alexmorbo/seasonfill/application/instance"
	"github.com/alexmorbo/seasonfill/application/ports"
	"github.com/alexmorbo/seasonfill/application/seriesdetail"
	"github.com/alexmorbo/seasonfill/application/seriesrefresh"
	"github.com/alexmorbo/seasonfill/cmd/server/adapters"
	"github.com/alexmorbo/seasonfill/infrastructure/database/repositories"
	"github.com/alexmorbo/seasonfill/infrastructure/sonarr"
	httpserver "github.com/alexmorbo/seasonfill/interface/http"
	handlers "github.com/alexmorbo/seasonfill/interface/http/handlers"
	authapp "github.com/alexmorbo/seasonfill/internal/admin/app"
	infraoidc "github.com/alexmorbo/seasonfill/internal/admin/infrastructure/oidc"
	adminpersistence "github.com/alexmorbo/seasonfill/internal/admin/persistence"
	"github.com/alexmorbo/seasonfill/internal/config"
	apppeople "github.com/alexmorbo/seasonfill/internal/enrichment/app/people"
	enrichpersistence "github.com/alexmorbo/seasonfill/internal/enrichment/persistence"
	"github.com/alexmorbo/seasonfill/internal/runtime"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
	sharedports "github.com/alexmorbo/seasonfill/internal/shared/ports"
)

// AuthBundle holds the auth-domain collaborators wired by BuildAuth.
// All five handles are passed-through to httpserver.NewServer (AdminRepo,
// LoginLimiter, WebhookLimiter, OIDCUC) and to the reload OIDC provider
// subscriber (OIDCCache) constructed in server.go's Run flow.
//
// AdminRepo  — admin_users CRUD, also the AdminUserRepository port the
//
//	OIDC login use case verifies against on group-ACL match.
//
// OIDCCache  — shared provider/discovery cache; the reload subscriber
//
//	invalidates it on issuer change.
//
// OIDCUC     — Authorization Code + PKCE use case, stateless beyond the
//
//	provider cache.
//
// LoginLimiter / WebhookLimiter — IP-keyed token bucket limiters with
//
//	the standard LoginLimit() / WebhookLimit() rates.
type AuthBundle struct {
	AdminRepo      *adminpersistence.AdminUserRepository
	OIDCCache      *infraoidc.ProviderCache
	OIDCUC         *authapp.OIDCLoginUseCase
	LoginLimiter   *authapp.IPLimiter
	WebhookLimiter *authapp.IPLimiter
}

// BuildAuth constructs the admin user repo, the OIDC provider cache,
// the OIDC login use case, the login + webhook IP limiters, and runs
// the admin password bootstrap (first-run seed; idempotent across
// restarts).
//
// The `bus` parameter is reserved — admin bootstrap does not currently
// publish runtime events. Keeping it in the signature matches the other
// wirers (BuildRuntimeConfig) so future auth events have a take-up path.
//
// The ctx parameter is reserved for future use. The current body uses a
// background context for the Bootstrap call to mirror the pre-refactor
// behaviour in Server.New: the seed must complete even if the parent
// ctx already carries a deadline applied by an outer test harness.
// Plumbing the parent ctx here would change the cancel semantics. Same
// pattern as BuildPersistence / BuildRuntimeConfig.
func BuildAuth(
	ctx context.Context,
	persistence *PersistenceBundle,
	bootCfg *config.Bootstrap,
	bus *runtime.Bus,
	log *slog.Logger,
) (*AuthBundle, error) {
	_ = ctx
	_ = bus
	bgCtx := context.Background()

	adminRepo := adminpersistence.NewAdminUserRepository(persistence.DB)
	oidcCache := infraoidc.NewProviderCache()
	oidcUC := authapp.NewOIDCLoginUseCase(oidcCache, adminRepo)

	// F-4b-8: bootstrap admin seeder emits auth-domain records
	// (admin-user creation, password-reset bootstrap).
	authLog := sharedports.DomainLogger(log, "auth")
	if err := authapp.Bootstrap(bgCtx, adminRepo, authapp.BootstrapConfig{
		WebUser:         bootCfg.Auth.WebUser,
		WebPassword:     bootCfg.Auth.WebPassword,
		WebPasswordHash: bootCfg.Auth.WebPasswordHash,
	}, authLog); err != nil {
		return nil, fmt.Errorf("auth bootstrap: %w", err)
	}

	loginLimiter := authapp.NewIPLimiter(authapp.LoginLimit(), 5)
	webhookLimiter := authapp.NewIPLimiter(authapp.WebhookLimit(), 60)

	return &AuthBundle{
		AdminRepo:      adminRepo,
		OIDCCache:      oidcCache,
		OIDCUC:         oidcUC,
		LoginLimiter:   loginLimiter,
		WebhookLimiter: webhookLimiter,
	}, nil
}

// InstanceBundle groups the instance-domain components constructed at boot.
// Returned by BuildInstance. Threaded into httpserver.NewServer (CRUDHandler,
// ProbeHandler) — the HTTP wirer remains in server.go for now.
//
// Field-level invariants:
//
//   - UC owns the WithWebhookReconciler + WithWebhookStatusCache chained
//     setters from the webhook bundle (story 335). The adapter is the
//     pre-baked WebhookBundle.ReconcilerAdapter — same pointer identity
//     as everywhere else.
//
//   - CRUDHandler wraps UC for the /api/v1/instances CRUD routes.
//
//   - ProbeHandler is the stateless POST /api/v1/instances/test handler;
//     it holds its own *http.Client (tuned for probe: 5s dial + TLS +
//     response-header timeouts, 64 KiB response-header cap, redirects
//     short-circuited with ErrUseLastResponse so probe assertions can
//     inspect the original status / Location).
//
//   - ProbeClient is exposed on the bundle for symmetry / tests; the
//     handler owns the only production reference.
type InstanceBundle struct {
	UC           *instance.UseCase
	CRUDHandler  *handlers.InstanceCRUDHandler
	ProbeHandler *handlers.InstanceProbeHandler
	ProbeClient  *http.Client
}

// BuildInstance wires the instance.UseCase + CRUD handler + Probe handler
// + probe HTTP client.
//
// Construction order mirrors the pre-336 inline body in server.go verbatim:
//
//  1. instance.New(instanceRepo, runtimeRepo, cipher, bus, log) chained
//     through WithWebhookReconciler(webhook.ReconcilerAdapter) +
//     WithWebhookStatusCache(webhook.StatusCache).
//  2. handlers.NewInstanceCRUDHandler(uc, log).
//  3. *http.Client tuned for probe (5s dial + TLS + response-header
//     timeouts, 64 KiB response-header cap, short-circuited redirects).
//  4. handlers.NewInstanceProbeHandler(probeClient, log).
//
// No error path — every step is in-memory construction. The signature
// returns error for symmetry with the other Build* wirers.
func BuildInstance(
	persistence *PersistenceBundle,
	webhook *WebhookBundle,
	bus *runtime.Bus,
	log *slog.Logger,
) (*InstanceBundle, error) {
	// F-4b-8: instance CRUD UC is the operator-facing admin surface for
	// Sonarr instance management — operator-driven mutations belong to
	// the "admin" slot.
	adminLog := sharedports.DomainLogger(log, "admin")
	uc := instance.New(
		persistence.InstanceRepo,
		persistence.RuntimeRepo,
		persistence.Cipher,
		bus,
		adminLog,
	).
		WithWebhookReconciler(webhook.ReconcilerAdapter).
		WithWebhookStatusCache(webhook.StatusCache)

	crudHandler := handlers.NewInstanceCRUDHandler(uc, log)

	probeClient := &http.Client{
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
		Transport: &http.Transport{
			DialContext:            (&net.Dialer{Timeout: 5 * time.Second}).DialContext,
			TLSHandshakeTimeout:    5 * time.Second,
			ResponseHeaderTimeout:  5 * time.Second,
			MaxResponseHeaderBytes: 64 << 10,
		},
	}
	probeHandler := handlers.NewInstanceProbeHandler(probeClient, log)

	return &InstanceBundle{
		UC:           uc,
		CRUDHandler:  crudHandler,
		ProbeHandler: probeHandler,
		ProbeClient:  probeClient,
	}, nil
}

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
//     MediaHashLookupPort (HashForSourceURL + EnsurePending — story 320).
//     server.go's LATE BIND ZONE injects the enqueuer + on-demand fetcher
//     once enrichBundle is ready.
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
	MediaResolver        *seriesdetail.MediaResolver
	Composer             *seriesdetail.Composer
	CastComposer         *seriesdetail.CastComposer
	PeopleUC             *apppeople.UseCase
	SeriesRefreshUC      *seriesrefresh.UseCase
	DetailHandler        *handlers.SeriesDetailHandler
	SeasonHandler        *handlers.SeriesSeasonHandler
	CastHandler          *handlers.SeriesCastHandler
	PeopleHandler        *handlers.PeopleHandler
	RefreshHandler       *handlers.SeriesRefreshHandler
	PersonEnqueuerHolder *adapters.PersonEnqueuerHolder
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
	// NewMediaResolver → every wire field stays nil and the frontend
	// renders monograms. *MediaAssetsRepository satisfies the widened
	// MediaHashLookupPort (HashForSourceURL + EnsurePending) by virtue
	// of the new EnsurePending method (story 320).
	var mediaHashLookup seriesdetail.MediaHashLookupPort
	if mediaBundle != nil && mediaBundle.AssetsRepo != nil {
		mediaHashLookup = mediaBundle.AssetsRepo
	}
	// Story 316: enqueuer + fetcher are late-bound via SetSideEffects
	// after wireEnrichment returns — the media pipeline doesn't exist
	// yet at this point in boot.
	mediaResolver := seriesdetail.NewMediaResolver(mediaHashLookup, nil, nil, composerLog)
	// Story 347 — uniform always-emit-hash contract. Default-on; env
	// kill-switch (SEASONFILL_MEDIA_UNIFIED_RESOLVE=false) flips back
	// to legacy nil-on-miss without a redeploy.
	mediaResolver.SetUnifiedResolve(unifiedResolve)

	// Story 215 (G-1) — series detail composer + handlers. The repos
	// are stateless GORM wrappers around `db`, so re-constructing
	// them here is free; the enrichment block in server.go re-uses
	// its own instances of the same set for the worker pipeline.
	sdSeriesRepo := enrichpersistence.NewSeriesRepository(db)
	sdSeriesCacheRepo := repositories.NewSeriesCacheRepository(db, sdSeriesRepo)
	sdSeriesTextsRepo := repositories.NewSeriesTextsRepository(db)
	sdSeasonsRepo := enrichpersistence.NewSeasonsRepository(db)
	sdEpisodesRepo := enrichpersistence.NewEpisodesRepository(db)
	sdEpisodeStatesRepo := repositories.NewEpisodeStatesRepository(db)
	sdSeasonStatsRepo := repositories.NewSeasonStatsRepository(db)
	sdEpisodeTextsRepo := repositories.NewEpisodeTextsRepository(db)
	sdSeriesPeopleRepo := enrichpersistence.NewSeriesPeopleRepository(db)
	sdPeopleRepo := enrichpersistence.NewPeopleRepository(db)
	sdGenresRepo := repositories.NewGenresRepository(db)
	sdKeywordsRepo := repositories.NewKeywordsRepository(db)
	sdNetworksRepo := repositories.NewNetworksRepository(db)
	sdCompaniesRepo := repositories.NewCompaniesRepository(db)
	sdVideosRepo := repositories.NewVideosRepository(db)
	sdContentRatingsRepo := repositories.NewContentRatingsRepository(db)
	sdExternalIDsRepo := repositories.NewExternalIDsRepository(db)
	sdRecommendationsRepo := repositories.NewRecommendationsRepository(db)
	sdSyncLogRepo := repositories.NewSyncLogRepository(db)

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
		SeriesPeople:      sdSeriesPeopleRepo,
		People:            sdPeopleRepo,
		Genres:            sdGenresRepo,
		Keywords:          sdKeywordsRepo,
		Networks:          sdNetworksRepo,
		Companies:         sdCompaniesRepo,
		Videos:            sdVideosRepo,
		ContentRatings:    sdContentRatingsRepo,
		ExternalIDs:       sdExternalIDsRepo,
		Recommendations:   sdRecommendationsRepo,
		SyncLog:           sdSyncLogRepo,
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
	detailHandler := handlers.NewSeriesDetailHandler(composer, log)
	seasonHandler := handlers.NewSeriesSeasonHandler(composer, log)

	// Story 216 (H-1) — full cast & crew composer. Reuses the 215
	// repos (series_cache + series + series_people + people) plus
	// the new EpisodesRepository.CountBySeries method and a thin
	// adapter projecting repositories.PersonCredit → composer-local
	// PersonCreditRef.
	sdPersonCreditsRepo := enrichpersistence.NewPersonCreditsRepository(db)
	castComposer := seriesdetail.NewCastComposer(seriesdetail.CastDeps{
		SeriesCache:       sdSeriesCacheRepo,
		SeriesCacheLookup: sdSeriesCacheRepo,
		Series:            sdSeriesRepo,
		SeriesPeople:      sdSeriesPeopleRepo,
		People:            sdPeopleRepo,
		PersonCredits:     adapters.NewPersonCreditsAdapter(sdPersonCreditsRepo),
		EpisodesCount:     sdEpisodesRepo,
		Logger:            composerLog,
		MediaResolver:     mediaResolver,
	})
	castHandler := handlers.NewSeriesCastHandler(castComposer, log)

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
		SyncLog:       sdSyncLogRepo,
		Enqueuer:      peopleEnqueuerHolder,
		MediaResolver: mediaResolver,
		// F-4b-8: people UC composes person-detail responses under the
		// seriesdetail composer pipe — anchors on the "composer" slot.
		Logger: composerLog,
	})
	peopleHandler := handlers.NewPeopleHandler(peopleUC, log)

	// Story 218 (E-2) — series refresh trigger. Reuses the
	// peopleEnqueuerHolder so the same late-binding dispatcher
	// satisfies both the H-2 use case AND the refresh path.
	seriesRefreshUC, err := seriesrefresh.New(seriesrefresh.Deps{
		SeriesCache:  sdSeriesCacheRepo,
		Series:       adapters.NewSeriesRefreshSeriesAdapter(sdSeriesRepo),
		SeriesPeople: adapters.NewSeriesRefreshCastAdapter(sdSeriesPeopleRepo),
		Dispatcher:   peopleEnqueuerHolder,
		Logger:       composerLog,
	})
	if err != nil {
		return nil, fmt.Errorf("seriesrefresh use case: %w", err)
	}
	seriesRefreshHandler := handlers.NewSeriesRefreshHandler(seriesRefreshUC, log)

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
	}, nil
}

// BuildHTTPServer wraps the 37-arg httpserver.NewServer invocation that
// previously lived inline in cmd/server/server.go (B-11 step 20 / story
// 342).
//
// Positional arg ORDER is the immutable contract here: the underlying
// httpserver.NewServer signature is the canonical positional list and
// THIS wrapper does NOT reorder, group, or drop. Every argument is
// read straight off a bundle (or one of the two named locals) and
// forwarded in the same slot.
//
// seriesCacheRepo + counterRepo come in as explicit parameters because
// they are not (yet) members of any existing bundle — they are
// constructed inline in server.go (alongside seriesRepo) and used both
// by the HTTP server and by the enrichment block below it. Pushing
// them onto a bundle is out of scope for B-11 step 20.
//
// The LATE BIND ZONE in server.go runs AFTER this wirer is called — the
// handlers passed in are pointer-typed, so mutations applied to
// mediaBundle.Handler (SetOnDemandFetcher) and
// seriesDetailBundle.MediaResolver (SetSideEffects) after this
// constructor returns are visible at request time via gin's per-handler
// dispatch. The pre-342 layout called httpserver.NewServer at the same
// position, so this wirer preserves that ordering verbatim.
func BuildHTTPServer(
	persistence *PersistenceBundle,
	runtimecfg *RuntimeConfigBundle,
	auth *AuthBundle,
	sonarrBundle *SonarrBundle,
	watchdogBundle *WatchdogBundle,
	scanBundle *ScanBundle,
	webhookBundle *WebhookBundle,
	instanceBundle *InstanceBundle,
	regrabBundle *RegrabBundle,
	torrentsyncBundle *TorrentsyncBundle,
	extSvcBundle *ExtSvcBundle,
	mediaBundle *MediaBundle,
	seriesDetailBundle *SeriesDetailBundle,
	seriesCacheRepo ports.SeriesCacheRepository,
	counterRepo ports.CounterRepository,
	log *slog.Logger,
) *httpserver.Server {
	return httpserver.NewServer(
		runtimecfg.ServeConfig.HTTP,
		scanBundle.ScanUC,
		webhookBundle.WebhookUC,
		watchdogBundle.Checker,
		scanBundle.ScanRepo,
		scanBundle.DecisionRepo,
		scanBundle.GrabRepo,
		auth.AdminRepo,
		auth.LoginLimiter,
		auth.WebhookLimiter,
		sonarrBundle.InstanceReg,
		scanBundle.CooldownRepo,
		scanBundle.GrabUC,
		scanBundle.RescanUC,
		instanceBundle.CRUDHandler,
		instanceBundle.ProbeHandler,
		runtimecfg.Handler,
		regrabBundle.QbitSettingsHandler,
		extSvcBundle.Handler,
		auth.OIDCUC,
		webhookBundle.Reconciler,
		webhookBundle.StatusCache,
		seriesCacheRepo,
		counterRepo,
		regrabBundle.WatchdogRollupHandler,
		regrabBundle.WatchdogBlacklistHandler,
		regrabBundle.WatchdogSeasonsHandler,
		regrabBundle.WebhooksAggregateHandler,
		mediaBundle.Handler,
		mediaBundle.AssetsRepo,
		seriesDetailBundle.DetailHandler,
		seriesDetailBundle.SeasonHandler,
		seriesDetailBundle.CastHandler,
		seriesDetailBundle.PeopleHandler,
		seriesDetailBundle.RefreshHandler,
		torrentsyncBundle.SeriesTorrentsHandler,
		persistence.TimezoneHandler,
		log,
	)
}
