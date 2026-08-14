package edge

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	auth "github.com/alexmorbo/seasonfill/internal/admin/app"
	adminrest "github.com/alexmorbo/seasonfill/internal/admin/rest"
	"github.com/alexmorbo/seasonfill/internal/admin/rest/healthcheck"
	"github.com/alexmorbo/seasonfill/internal/catalog/app/calendar"
	"github.com/alexmorbo/seasonfill/internal/catalog/app/collections"
	"github.com/alexmorbo/seasonfill/internal/catalog/app/gaps"
	health "github.com/alexmorbo/seasonfill/internal/catalog/app/health"
	"github.com/alexmorbo/seasonfill/internal/catalog/app/icsfeed"
	apprescan "github.com/alexmorbo/seasonfill/internal/catalog/app/rescan"
	"github.com/alexmorbo/seasonfill/internal/catalog/app/scan"
	"github.com/alexmorbo/seasonfill/internal/catalog/app/smartlists"
	"github.com/alexmorbo/seasonfill/internal/catalog/app/stats"
	"github.com/alexmorbo/seasonfill/internal/catalog/app/webhookinstall"
	catalogrest "github.com/alexmorbo/seasonfill/internal/catalog/rest"
	"github.com/alexmorbo/seasonfill/internal/config"
	discoveryrest "github.com/alexmorbo/seasonfill/internal/discovery/rest"
	enrichrest "github.com/alexmorbo/seasonfill/internal/enrichment/rest"
	followrest "github.com/alexmorbo/seasonfill/internal/follow/rest"
	appgrab "github.com/alexmorbo/seasonfill/internal/grab/app"
	grabrest "github.com/alexmorbo/seasonfill/internal/grab/rest"
	mediaproxyrest "github.com/alexmorbo/seasonfill/internal/mediaproxy/rest"
	moviedetailrest "github.com/alexmorbo/seasonfill/internal/moviedetail/rest"
	notificationrest "github.com/alexmorbo/seasonfill/internal/notification/rest"
	requestrest "github.com/alexmorbo/seasonfill/internal/request/rest"
	"github.com/alexmorbo/seasonfill/internal/runtime"
	"github.com/alexmorbo/seasonfill/internal/runtime/crypto"
	seriesdetailrest "github.com/alexmorbo/seasonfill/internal/seriesdetail/rest"
	"github.com/alexmorbo/seasonfill/internal/shared/clients/sonarr"
	ports "github.com/alexmorbo/seasonfill/internal/shared/dataports"
	"github.com/alexmorbo/seasonfill/internal/shared/http/handlers"
	"github.com/alexmorbo/seasonfill/internal/shared/http/middleware"
	"github.com/alexmorbo/seasonfill/internal/shared/media"
	torrentactionrest "github.com/alexmorbo/seasonfill/internal/torrentaction/rest"
	watchdogrest "github.com/alexmorbo/seasonfill/internal/watchdog/rest"
)

type Server struct {
	cfg         config.HTTPConfig
	server      *http.Server
	engine      *gin.Engine
	authHandler *adminrest.AuthHandler
	logger      *slog.Logger
}

func NewServer(
	cfg config.HTTPConfig,
	scanUC *scan.UseCase,
	webhookInbox ports.WebhookInboxRepository,
	webhookTxr ports.Transactor,
	webhookPoke func(),
	checker *healthcheck.Checker,
	scanRepo ports.ScanRepository,
	decisionRepo ports.DecisionRepository,
	grabRepo ports.GrabRepository,
	adminRepo ports.UserRepository,
	loginLimiter *auth.IPLimiter,
	webhookLimiter *auth.IPLimiter,
	instanceReg catalogrest.InstanceRegistry,
	cooldownRepo ports.CooldownRepository,
	grabUC *appgrab.UseCase,
	rescanUC *apprescan.UseCase,
	instanceCRUD *catalogrest.InstanceCRUDHandler,
	instanceProbe *catalogrest.InstanceProbeHandler,
	runtimeConfigHandler *catalogrest.RuntimeConfigHandler,
	qbitSettings *handlers.QbitSettingsHandler,
	externalServices *enrichrest.ExternalServicesHandler,
	oidcUC *auth.OIDCLoginUseCase,
	webhookReconciler *webhookinstall.Reconciler,
	webhookStatusCache *webhookinstall.StatusCache,
	seriesCacheRepo ports.SeriesCacheRepository,
	counterRepo ports.CounterRepository,
	healthRepo ports.HealthRepository,
	gapRepo ports.GapRepository,
	statsRepo ports.StatsRepository,
	smartListsRepo ports.SmartListsRepository,
	collectionsRepo ports.CollectionsRepository, // Story I-5 — curated collections
	calendarRepo ports.CalendarRepository, // ADR-0015 Ф3 S2 — read-only release-calendar query repo
	watchdogRollupHandler *watchdogrest.WatchdogRollupHandler,
	watchdogBlacklistHandler *watchdogrest.WatchdogBlacklistHandler,
	watchdogSeasonsHandler *watchdogrest.WatchdogSeasonsHandler,
	webhooksAggregateHandler *catalogrest.WebhooksAggregateHandler,
	mediaHandler *mediaproxyrest.MediaHandler,
	mediaPending adminrest.CatalogMediaPendingWriter,
	peopleHandler *enrichrest.PeopleHandler,
	seriesTorrentsHandler *seriesdetailrest.SeriesTorrentsHandler,
	torrentActionHandler *torrentactionrest.Handler, // ADR-0013 Q2
	timezoneHandler *adminrest.TimezoneHandler,
	meHandler *adminrest.MeHandler,
	sharedAuthRuntime *middleware.AuthRuntimePointer,
	globalSeriesHandler *seriesdetailrest.GlobalSeriesHandler,
	globalCastHandler *seriesdetailrest.GlobalSeriesCastHandler, // story 535
	globalSeasonHandler *seriesdetailrest.GlobalSeriesSeasonHandler, // TMDB-only season fallback
	globalOverviewHandler *seriesdetailrest.GlobalSeriesOverviewHandler, // story 529
	globalRecommendationsHandler *seriesdetailrest.GlobalSeriesRecommendationsHandler, // story 530
	globalRatingsHandler *seriesdetailrest.GlobalSeriesRatingsHandler, // W18-7a /ratings SWR
	globalLibraryHandler *seriesdetailrest.GlobalSeriesLibraryHandler, // story 577 E-1-B2
	monitorSeasonHandler *seriesdetailrest.MonitorSeasonHandler, // ADR-0012 S1 season monitor
	seasonsHandler *seriesdetailrest.SeasonsHandler, // story 582 E-1 B3c
	resolveHandler *seriesdetailrest.ResolveHandler, // BE-3 card-unification
	discoveryHandler *discoveryrest.DiscoveryHandler,
	discoverHandler *discoveryrest.DiscoverHandler, // story 509 N-2h
	movieDiscoverHandler *discoveryrest.MovieDiscoverHandler, // Ф6-R-4a L3-1

	rowConfigHandler *discoveryrest.RowConfigHandler, // ADR-0017 Ф5 D-1
	blocklistHandler *discoveryrest.BlocklistHandler, // ADR-0017 Ф5 S3
	instanceMetadataHandler *adminrest.InstanceMetadataHandler, // story 519 N-4b
	addToSonarrHandler *discoveryrest.AddToSonarrHandler, // story 520 N-4c
	movieDetailHandler *moviedetailrest.Handler, // Ф6-R-6a
	addToRadarrHandler *discoveryrest.AddToRadarrHandler, // Ф6-R-6a
	movieCalendarHandler *catalogrest.MovieCalendarHandler, // Ф6-R-6a
	movieCollectionsHandler *catalogrest.MovieCollectionsHandler, // Ф6-R-6a
	// Story 578 / E-1-B5 — per-section freshness reader for the ETag
	// middleware. nil-OK: when nil the middleware is a pass-through, so
	// minimal/test wirings keep working with zero behaviour change.
	etagFreshness SectionSyncedAtReader,
	// Story E-1-B7 — optional series-title localizer for the global
	// series-cache list (?lang=). nil-OK: pass-through, canon titles.
	seriesTitleLocalizer catalogrest.SeriesTextLocalizer,
	// Story 584b — optional per-language poster localizer for the global
	// series-cache list (?lang=). nil-OK: pass-through, canon poster_hash.
	seriesMediaLocalizer catalogrest.SeriesMediaLocalizer,
	// ADR-0015 Ф3 C1 — follow/watchlist handler. nil-OK: the /follow routes
	// are omitted when the handler is absent (minimal/test wirings).
	followHandler *followrest.FollowHandler,
	// ADR-0015 Ф3 S3 — ics_epoch read/bump for the ICS calendar feed.
	// nil-OK (together with calendarRepo): the /calendar.ics routes are
	// omitted when either is absent (minimal/test wirings).
	icsEpochRepo ports.ICSEpochRepository,
	// ADR-0016 Ф4 N1 — notification agents CRUD/test handler. nil-OK: the
	// /notification-agents routes are omitted when the handler is absent
	// (minimal/test wirings).
	notificationAgentsHandler *notificationrest.AgentsHandler,
	// Ф6-R-6b Gap 2a — reload-aware radarr instance map so GET
	// /admin/instances renders radarr instances (type + url + health)
	// alongside sonarr. nil-OK: list stays sonarr-only (minimal/test wirings).
	radarrConfigLookup catalogrest.RadarrConfigLookup,
	// Ф6-R-6b — global movie library list (GET /api/v1/movies). nil-OK: the
	// route is omitted when the handler is absent (minimal/test wirings).
	movieLibraryHandler *catalogrest.MovieLibraryHandler,
	// Ф8-U-2 — request-workflow handler. nil-OK: the /requests routes are
	// omitted when the handler is absent (minimal/test wirings).
	requestHandler *requestrest.RequestHandler,
	// Ф8-U-6b — admin user-management handler. nil-OK: the /admin/users
	// routes are omitted when the handler is absent (minimal/test wirings).
	usersHandler *adminrest.UsersHandler,
	// Ф1.4 — one-shot movie re-enrichment backfill trigger (audit F-Ф1-07).
	// nil-OK: the /admin/movies/reenrich route is omitted when absent
	// (minimal/test wirings).
	movieReenrichHandler *enrichrest.MovieReenrichHandler,
	// Ф0.1 — shared media resolver for the insights /calendar poster paths
	// (mirrors the movie calendar). nil-OK: raw TMDB paths flow through.
	insightsCalendarResolver *media.Resolver,
	// Ф2.1 — movie cast sub-endpoint. nil-OK: the /movies/:tmdb_id/cast route is
	// omitted when the handler is absent (minimal/test wirings).
	movieCastHandler *moviedetailrest.MovieCastHandler,
	// Ф2.2 — movie overview sub-endpoint. nil-OK: the /movies/:tmdb_id/overview
	// route is omitted when the handler is absent (minimal/test wirings).
	movieOverviewHandler *moviedetailrest.MovieOverviewHandler,
	// Ф2.3 — movie ratings sub-endpoint. nil-OK: the /movies/:tmdb_id/ratings
	// route is omitted when the handler is absent (minimal/test wirings).
	movieRatingsHandler *moviedetailrest.MovieRatingsHandler,
	// Ф2.4 — movie recommendations sub-endpoint. nil-OK: the
	// /movies/:tmdb_id/recommendations route is omitted when absent.
	movieRecommendationsHandler *moviedetailrest.MovieRecommendationsHandler,
	// Ф2.1 — per-section movie freshness reader for the movie ETag middleware.
	// nil-OK: the middleware is a pass-through when nil.
	movieEtagFreshness SectionSyncedAtReader,
	logger *slog.Logger,
) *Server {
	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.Use(gin.Recovery())
	r.Use(middleware.RequestLoggerMiddleware(logger))
	r.Use(middleware.MetricsMiddleware())
	r.Use(middleware.ErrorResponseMiddleware(logger))

	// HIGH-S2: bound which proxies' X-Forwarded-For we honor. Default
	// (set by config.Defaults) is ["127.0.0.1", "::1"] — only localhost
	// trusted. Empty list disables XFF entirely; SetTrustedProxies(nil)
	// makes c.ClientIP() fall back to RemoteAddr. The login + webhook
	// rate limits rely on this for accurate keying.
	if err := r.SetTrustedProxies(cfg.Auth.TrustedProxies); err != nil {
		logger.Warn("http.trusted_proxies invalid — falling back to RemoteAddr",
			slog.String("error", err.Error()))
		_ = r.SetTrustedProxies(nil)
	}

	healthHandler := adminrest.NewHealthHandler(checker)
	scanHandler := catalogrest.NewScanHandler(scanUC, logger)
	// Singleton episodes cache shared by the Missing handler. Lives
	// for the life of the process — like the poster cache, the cap +
	// TTL are package-level constants (see internal/runtime/snapshot.go).
	// 5-min TTL means operator-driven /missing polls reflect new
	// imports within the next refetch; 32 MiB byte cap holds ~200k
	// episodes worth of metadata.
	episodesCache := sonarr.NewLRUEpisodesCache(
		runtime.EpisodesCacheMaxBytes, runtime.EpisodesCacheTTL)
	instancesHandler := catalogrest.NewInstancesHandler(checker, instanceReg, logger).
		WithSeriesCache(seriesCacheRepo).
		WithEpisodesCache(episodesCache).
		WithMediaPending(mediaPending).
		WithLocalizer(seriesTitleLocalizer).
		WithMediaLocalizer(seriesMediaLocalizer).
		WithRadarrHolder(radarrConfigLookup). // Ф6-R-6b Gap 2a
		WithAdminResolver(adminRepo)          // F-09 role-aware trim
	if runtimeConfigHandler != nil {
		runtimeConfigHandler.WithAdminResolver(adminRepo) // F-09 role-aware trim
	}
	// Story 491 / N-1a — global catalog handler over the per-instance one.
	globalCatalogHandler := catalogrest.NewGlobalCatalogHandler(instancesHandler, logger)
	// Story 492 / N-1b — global series-scoped wrappers + global grab
	// episode-files. Constructed as thin delegates over the per-instance
	// handlers; nil-OK pattern mirrors the per-instance variants so the
	// route is omitted (not 5xx-stubbed) when the inner is absent.
	// Story 535 — globalCastHandler now built in wiring.BuildSeriesDetail
	// so it shares scope with tmdbFallbackUC; passed in as a param. The
	// global season handler moved to wiring for the same reason (TMDB-only
	// season fallback needs tmdbFallbackUC).
	var globalTorrentsHandler *seriesdetailrest.GlobalSeriesTorrentsHandler
	if seriesTorrentsHandler != nil {
		globalTorrentsHandler = seriesdetailrest.NewGlobalSeriesTorrentsHandler(seriesTorrentsHandler, seriesCacheRepo, logger)
	}
	globalSeasonEpisodesHandler := catalogrest.NewGlobalSeasonEpisodesHandler(instancesHandler, seriesCacheRepo, logger)
	globalGrabEpisodeFilesHandler := grabrest.NewGlobalGrabEpisodeFilesHandler(grabRepo, instanceReg, logger)
	auditHandler := handlers.NewAuditHandler(scanRepo, decisionRepo, grabRepo, logger).
		WithSeriesCache(seriesCacheRepo).
		WithMediaPending(mediaPending).
		WithLocalizer(seriesTitleLocalizer)
	webhookHandler := catalogrest.NewWebhookHandler(webhookInbox, webhookTxr, webhookPoke, instanceReg, logger)
	// Ф6-R-4b: Radarr Connect webhook handler — enqueues into the SAME durable
	// inbox as the Sonarr handler; the drainer routes the row to the radarr
	// map+process by arr_instance.type. Reuses the identical inbox/txr/poke/reg.
	radarrWebhookHandler := catalogrest.NewRadarrWebhookHandler(webhookInbox, webhookTxr, webhookPoke, instanceReg, logger)
	grabHandler := grabrest.NewGrabHandler(decisionRepo, grabRepo, cooldownRepo, grabUC, instanceReg, logger)

	r.GET("/healthz", healthHandler.Live)
	r.GET("/readyz", healthHandler.Ready)
	r.GET("/metrics", adminrest.MetricsHandler())

	api := r.Group("/api/v1")

	var serverAuthHandler *adminrest.AuthHandler
	if cfg.Auth.Enabled {
		sessionKey, err := crypto.DeriveSessionHMACKey(cfg.Auth.APIKey)
		if err != nil {
			panic("http.NewServer: derive session HMAC key: " + err.Error())
		}
		// M1: stricter limiter for /auth/password — 3 attempts / 15min,
		// per ClientIP. Independent from the login limiter so a brute-
		// forcer with a stolen cookie can't exhaust BOTH paths.
		passwordLimiter := auth.NewIPLimiter(auth.PasswordChangeLimit(), 3)
		// Story 485 (N-7a) + F-03 (AUDIT2-S3): if a shared AuthRuntime pointer
		// was supplied, seed its boot defaults (SessionTTL + SecureCookie +
		// SessionEpoch) here so the AuthHandler, MeHandler, and cookie verifier
		// observe the same initial values BEFORE the reload subscriber publishes
		// the first snapshot. Without the TTL seed, Login would issue a cookie
		// with max-age=0 on the first request; without the epoch seed, a pre-bump
		// stale cookie (Epoch < live) would validate against a default-zero epoch
		// for the whole boot window.
		if sharedAuthRuntime != nil {
			sharedAuthRuntime.Store(&middleware.AuthRuntime{
				SessionTTL:   cfg.Auth.SessionTTL,
				SecureCookie: cfg.Auth.SecureCookie,
				SessionEpoch: cfg.Auth.SessionEpoch,
			})
		}
		authHandler := adminrest.NewAuthHandler(
			cfg.Auth.APIKey, adminRepo, cfg.Auth.SessionTTL,
			cfg.Auth.SecureCookie, loginLimiter, logger,
			adminrest.WithPasswordLimiter(passwordLimiter),
			adminrest.WithAuthRuntimePointer(sharedAuthRuntime),
		)
		// Hold a reference so the reload subscriber can pull the
		// shared AuthRuntime pointer out at startup.
		serverAuthHandler = authHandler
		api.POST("/auth/login", authHandler.Login)
		// Public bootstrap endpoint — MUST be registered before the
		// guarded group so it inherits NO RequireAuth middleware.
		// Reads from the same AuthRuntime atomic the dispatcher uses.
		authConfigHandler := adminrest.NewAuthConfigHandler(authHandler.AuthRuntime())
		api.GET("/auth/config", authConfigHandler.Get)

		oidcHandler := adminrest.NewOIDCHandler(
			oidcUC, authHandler.AuthRuntime(), sessionKey,
			cfg.Auth.SessionTTL, cfg.Auth.SecureCookie, logger,
		)
		api.GET("/auth/oidc/start", oidcHandler.Start)
		api.GET("/auth/oidc/callback", oidcHandler.Callback)

		// Ф8-U-3 — Jellyfin username/password auth source. PUBLIC route
		// (no RequireAuth). The usecase needs only the users repo; the
		// jellyfin.Client is built per request by the handler from the live
		// AuthRuntime base URL (SEASONFILL_JELLYFIN_BASE_URL, threaded via the
		// auth reload subscriber). Empty base URL => 503 at request time.
		jellyfinUC := auth.NewJellyfinLoginUseCase(adminRepo).WithLogger(logger)
		jellyfinHandler := adminrest.NewJellyfinHandler(
			jellyfinUC, authHandler.AuthRuntime(), sessionKey,
			cfg.Auth.SessionTTL, cfg.Auth.SecureCookie, logger,
		)
		api.POST("/auth/jellyfin/login", jellyfinHandler.Login)

		guarded := api.Group("")
		guarded.Use(middleware.RequireAuthWithRuntime(
			cfg.Auth.APIKey, sessionKey, authHandler.AuthRuntime(),
			adminRepo, loginLimiter,
		))
		// Ф8-U-1 RBAC — per-route permission guards. Constructed once,
		// reused across routes (stateless). role=='admin' and the
		// "api-key" automation principal short-circuit, so these are
		// no-ops for the current single admin user + all X-Api-Key
		// callers. Only mutating routes are gated; GET reads stay open to
		// any authenticated principal.
		permAdmin := middleware.RequirePermission(adminRepo,
			middleware.PermManageUsers, middleware.PermManageRequests)
		permRequest := middleware.RequirePermission(adminRepo, middleware.PermRequest)
		permManageUsers := middleware.RequirePermission(adminRepo, middleware.PermManageUsers)
		// Ф8-U-2 — dedicated manage_requests bucket (permAdmin bundles
		// ManageUsers|ManageRequests; approve/deny want the SPECIFIC perm).
		permManageRequests := middleware.RequirePermission(adminRepo, middleware.PermManageRequests)
		guarded.GET("/auth/session", authHandler.Session)
		guarded.DELETE("/auth/session", authHandler.Logout)
		guarded.POST("/auth/password", authHandler.PasswordChange)
		guarded.POST("/scan", permAdmin, scanHandler.Trigger)
		// Story 492 / N-1b — per-instance series-scoped routes DELETED.
		// `/missing` (no replacement; FE drops the consumer), per-
		// instance counters, the series-cache list / networks facet,
		// the series-detail document, refresh trigger, season detail,
		// cast, torrents, and the season-episodes upstream fetch all
		// move to the global namespace (`/series/...`) or are dropped
		// entirely. The per-instance handler STRUCTS stay alive —
		// they're reached via the global wrappers' c.Params splice —
		// only the route registrations drop. The `/instances` list
		// endpoint also moves under `/admin/instances` per PRD §4828.
		// The aggregate `/counters` (global cross-instance) stays.
		countersHandler := catalogrest.NewCountersHandler(instanceReg, counterRepo, logger)
		// Story I-1a — catalog health dashboard (read-only pulse). Usecase
		// owns the staleness TTL policy + deferred rate-limit envelope; repo
		// is the dialect-portable query set. Named distinctly from the
		// adminrest healthcheck handler above (different concept).
		insightsHealthHandler := catalogrest.NewHealthHandler(health.NewUseCase(healthRepo), logger)
		// Story I-2a — library gap detector (read-only). Usecase owns the
		// wall clock (already-aired boundary); repo is the dialect-portable
		// aired-only gap query set.
		insightsGapsHandler := catalogrest.NewGapsHandler(gaps.NewUseCase(gapRepo), logger)
		// Story I-4 — library statistics (read-only). Usecase fans out per
		// instance; repo is the dialect-portable aggregation set.
		insightsStatsHandler := catalogrest.NewStatsHandler(stats.NewUseCase(statsRepo), logger)
		// Story I-3 — smart lists (read-only). Usecase fans out per instance
		// and builds the three fixed shelves; repo is the dialect-portable
		// shelf query set.
		insightsListsHandler := catalogrest.NewSmartListsHandler(smartlists.NewUseCase(smartListsRepo), logger)
		// Story I-5 — curated collections (read-only). Usecase fans out per instance
		// and evaluates the static curated seed; repo is the dialect-portable
		// keyword-match query set.
		insightsCollectionsHandler := catalogrest.NewCollectionsHandler(collections.NewUseCase(collectionsRepo), logger)
		// ADR-0015 Ф3 S2 — release calendar (read-only). Usecase owns the
		// clock (window default + upcoming boundary); repo is the dialect-
		// portable windowed episode read.
		insightsCalendarHandler := catalogrest.NewCalendarHandler(calendar.NewUseCase(calendarRepo), insightsCalendarResolver, logger)
		// Story 217 (H-2) — person detail page. Top-level resource —
		// `/people` is instance-independent. Nil-OK pattern matches
		// seriesCastHandler.
		if peopleHandler != nil {
			guarded.GET("/people/:tmdbId", peopleHandler.Get)
		}
		// Story 491 / N-1a — global series surface. Routes resolved by
		// canonical series.id rather than per-instance Sonarr id.
		// Register `/series/networks` BEFORE `/series/:id` for clarity
		// (gin radix tree handles static-before-param anyway, but
		// declaration order matches reader expectations).
		guarded.GET("/series/networks", globalCatalogHandler.Networks)
		// BE-3 (card-unification) — resolve-or-create by tmdb_id. The
		// literal `/series/resolve` is registered BEFORE `/series/:id`;
		// gin's radix tree gives static segments precedence over the `:id`
		// param at the same position (same coexistence as `/series/networks`
		// above), so "resolve" is never captured as an :id. nil-OK: the
		// route is omitted when the handler is absent (minimal/test wirings).
		if resolveHandler != nil {
			guarded.GET("/series/resolve", resolveHandler.Resolve)
		}
		guarded.GET("/series", globalCatalogHandler.List)
		// Story 578 / E-1-B5 — weak-ETag / Cache-Control on the
		// enrichment-backed canon-detail GETs. Built once, shared across
		// routes (stateless). gin runs it before each handler; on a
		// 304 / fail-open path it either aborts or c.Next()s untouched.
		// Deliberately NOT wired onto POST /regrab (mutating), /torrents
		// or /library (per-instance *Arr state, no enrichment stamp).
		etagMW := ETagMiddleware("id", etagFreshness, logger)
		if globalSeriesHandler != nil {
			guarded.GET("/series/:id", etagMW, globalSeriesHandler.Get)
			guarded.POST("/series/:id/regrab", permRequest, globalSeriesHandler.Regrab)
		}
		// Story 492 / N-1b — global series-scoped surfaces.
		if globalCastHandler != nil {
			guarded.GET("/series/:id/cast", etagMW, globalCastHandler.Get)
		}
		// Story 529 — decomposition 1/3: /series/:id/overview split.
		if globalOverviewHandler != nil {
			guarded.GET("/series/:id/overview", etagMW, globalOverviewHandler.Get)
		}
		// Story 530 — decomposition 2/3: /series/:id/recommendations split.
		if globalRecommendationsHandler != nil {
			guarded.GET("/series/:id/recommendations", etagMW, globalRecommendationsHandler.Get)
		}
		if globalSeasonHandler != nil {
			guarded.GET("/series/:id/season/:n", etagMW, globalSeasonHandler.Get)
		}
		guarded.GET("/series/:id/seasons/:season/episodes", etagMW, globalSeasonEpisodesHandler.Get)
		if globalTorrentsHandler != nil {
			guarded.GET("/series/:id/torrents", globalTorrentsHandler.Get)
		}
		// Story 577 / E-1-B2 — per-instance Sonarr library-state endpoint.
		if globalLibraryHandler != nil {
			guarded.GET("/series/:id/library", globalLibraryHandler.Get)
		}
		// ADR-0012 S1 — per-instance season monitor + search. nil-OK: the
		// route is omitted when the handler is absent (minimal/test wirings).
		if monitorSeasonHandler != nil {
			guarded.POST("/instances/:name/series/:id/seasons/:season/monitor", permRequest, monitorSeasonHandler.Post)
		}
		// ADR-0015 Ф3 C1 — follow/watchlist. nil-OK: routes omitted when the
		// handler is absent (minimal/test wirings).
		if followHandler != nil {
			guarded.POST("/follow", followHandler.Post)
			guarded.DELETE("/follow/:series_id", followHandler.Delete)
			guarded.GET("/follow", followHandler.List)
		}
		// Ф8-U-2 — request-workflow. GET is authenticated (own vs all scoping is
		// handler-side, driven by the caller's manage_requests/admin). approve/deny
		// are gated by manage_requests via permManageRequests. nil-OK: routes
		// omitted for minimal/test wirings.
		if requestHandler != nil {
			guarded.GET("/requests", requestHandler.List)
			guarded.POST("/requests/:id/approve", permManageRequests, requestHandler.Approve)
			guarded.POST("/requests/:id/deny", permManageRequests, requestHandler.Deny)
		}
		// ADR-0013 Q2 — instance-scoped torrent actions (F-16: actions ONLY
		// on the instance path). nil-OK: the routes are omitted when the
		// handler is absent (minimal/test wirings).
		if torrentActionHandler != nil {
			guarded.POST("/instances/:name/torrents/:hash/pause", permAdmin, torrentActionHandler.Pause)
			guarded.POST("/instances/:name/torrents/:hash/resume", permAdmin, torrentActionHandler.Resume)
			guarded.POST("/instances/:name/torrents/:hash/recheck", permAdmin, torrentActionHandler.Recheck)
		}
		// Story 582 / E-1 B3c — canon list-of-seasons (posters + counts).
		if seasonsHandler != nil {
			guarded.GET("/series/:id/seasons", seasonsHandler.Get)
		}
		// W18-7a — unified lazy ratings (SWR): TMDB ★ + OMDb/IMDb, per-source
		// freshness. Canon-keyed (series.id); no instance splice; no ETag
		// (poll-driven + write-triggering, mirrors /torrents + /library exclusion).
		if globalRatingsHandler != nil {
			guarded.GET("/series/:id/ratings", globalRatingsHandler.Get)
		}
		guarded.GET("/grabs/:id/episode-files", globalGrabEpisodeFilesHandler.List)
		// F-1 (Story 214): content-addressed media proxy. Serves the
		// canonical TMDB image variants pre-warmed by the series
		// enrichment worker. mediaHandler is nil-OK — when wiring is
		// disabled (tests / minimal boot) the route is omitted.
		//
		// HEAD is registered alongside GET so probes (curl -I, browser
		// prefetch, CDN warmup, monitoring) don't fall through to the
		// default Gin 404. The handler's c.Data writes the same headers
		// for HEAD — Gin's writer suppresses the body automatically.
		//
		// W16-2: registered on the PUBLIC `api` group (NOT `guarded`) so
		// unauthenticated <img> tags, incognito sessions, and CDN warmers
		// get the bytes (200/304) instead of a 401. The URLs are opaque
		// sha256 content-addressed image bytes (no PII, no enumeration),
		// and the handler reads no auth/session context — safe to expose.
		if mediaHandler != nil {
			api.GET("/media/:hash", mediaHandler.Serve)
			api.HEAD("/media/:hash", mediaHandler.Serve)
		}
		qbitDiscoverHandler := handlers.NewQbitDiscoverHandler(instanceReg, logger)
		guarded.GET("/instances/:name/discover/qbit", qbitDiscoverHandler.Discover)
		webhookInstallHandler := catalogrest.NewWebhookInstallHandler(webhookReconciler, webhookStatusCache, logger)
		guarded.POST("/instances/:name/webhook/install", permAdmin, reconcileContextMiddleware(), webhookInstallHandler.Install)
		webhookStatusHandler := catalogrest.NewWebhookStatusHandler(webhookReconciler, logger)
		guarded.GET("/instances/:name/webhook/status", reconcileContextMiddleware(), webhookStatusHandler.Status)
		// Story 492 / N-1b — admin instance management moves under
		// `/admin/instances/...`. Atomic flip (no co-registration) — the
		// FE story 493 swaps every call site in the same PR.
		guarded.GET("/admin/instances", instancesHandler.List)
		guarded.GET("/admin/instances/:name", instanceCRUD.Get)
		guarded.POST("/admin/instances", permAdmin, reconcileContextMiddleware(), instanceCRUD.Create)
		guarded.PUT("/admin/instances/:name", permAdmin, reconcileContextMiddleware(), instanceCRUD.Update)
		guarded.DELETE("/admin/instances/:name", permAdmin, reconcileContextMiddleware(), instanceCRUD.Delete)
		guarded.POST("/admin/instances/test",
			permAdmin,
			probeRateLimit(loginLimiter),
			instanceProbe.Test,
		)
		// ADR-0009 S6 — stateless Add-to-Sonarr defaults metadata probe. Same
		// guarded/admin group + rate limit as /test; builds a transient Sonarr
		// client from the posted url+api_key (no instance row required).
		guarded.POST("/admin/instances/metadata",
			permAdmin,
			probeRateLimit(loginLimiter),
			instanceProbe.Metadata,
		)
		// Ф8-U-6c — per-user notification agents CRUD + Test. Auth-only (any
		// authenticated user); owner-scoping in the repo is the security
		// boundary. nil-OK: routes omitted when the handler is absent
		// (minimal/test wirings).
		if notificationAgentsHandler != nil {
			guarded.GET("/notification-agents", notificationAgentsHandler.List)
			guarded.GET("/notification-agents/:id", notificationAgentsHandler.Get)
			guarded.POST("/notification-agents", notificationAgentsHandler.Create)
			guarded.PUT("/notification-agents/:id", notificationAgentsHandler.Update)
			guarded.DELETE("/notification-agents/:id", notificationAgentsHandler.Delete)
			guarded.POST("/notification-agents/:id/test", notificationAgentsHandler.Test)
		}
		// Ф8-U-6b — admin user-management. All routes behind the manage_users
		// guard (role='admin' + api-key short-circuit). nil-OK: omitted when
		// the handler is absent (minimal/test wirings).
		if usersHandler != nil {
			guarded.GET("/admin/users", permManageUsers, usersHandler.List)
			guarded.PATCH("/admin/users/:id", permManageUsers, usersHandler.Patch)
			guarded.DELETE("/admin/users/:id", permManageUsers, usersHandler.Delete)
		}
		// Ф1.4 — one-shot movie re-enrichment backfill (audit F-Ф1-07). Behind
		// permAdmin (role='admin' + api-key short-circuit), mirroring POST /scan.
		// nil-OK: omitted when the handler is absent (minimal/test wirings).
		if movieReenrichHandler != nil {
			guarded.POST("/admin/movies/reenrich", permAdmin, movieReenrichHandler.Trigger)
		}
		// Story 507 (N-2f) — curated discovery read endpoints.
		// Nil-OK pattern: when wiring did not construct the handler
		// (TMDB disabled at boot or test wiring) the routes are
		// omitted rather than 5xx-stubbed.
		if discoveryHandler != nil {
			guarded.GET("/discovery/trending", discoveryHandler.Trending)
			guarded.GET("/discovery/popular", discoveryHandler.Popular)
			guarded.GET("/discovery/genre/:id", discoveryHandler.ByGenre)
			guarded.GET("/discovery/network/:id", discoveryHandler.ByNetwork)
			guarded.GET("/discovery/keyword/:id", discoveryHandler.ByKeyword)
			guarded.GET("/discovery/genres", discoveryHandler.PickerGenres)
			guarded.GET("/discovery/networks", discoveryHandler.PickerNetworks)
			// Story 508 (N-2g) — local LIKE + TMDB fallback search.
			guarded.GET("/discovery/search", discoveryHandler.Search)
		}
		if discoverHandler != nil {
			// Story 509 (N-2h) — ad-hoc TMDB Discover passthrough with LRU
			// + background fetcher Pattern B (PRD §5.1.2).
			guarded.GET("/discovery/discover", discoverHandler.Handle)
		}
		if movieDiscoverHandler != nil {
			// Ф6-R-4a L3-1 — movie discovery surface (TMDB-driven).
			guarded.GET("/discovery/movie/discover", movieDiscoverHandler.Discover)
			guarded.GET("/discovery/movie/trending", movieDiscoverHandler.Trending)
			guarded.GET("/discovery/movie/popular", movieDiscoverHandler.Popular)
			guarded.GET("/discovery/movie/search", movieDiscoverHandler.Search)
		}
		// ADR-0017 Ф5 D-1/S2 — customisable rail config (read + write).
		if rowConfigHandler != nil {
			guarded.GET("/discovery/rows", rowConfigHandler.Handle)
			guarded.PUT("/discovery/rows", permAdmin, rowConfigHandler.Save)     // S2 D-3
			guarded.DELETE("/discovery/rows", permAdmin, rowConfigHandler.Reset) // S2 D-3
		}
		// ADR-0017 Ф5 S3 — discovery blocklist (global hide-list) +
		// keyword-search proxy. Distinct first path segment after
		// /discovery/ (blocklist vs keyword-search) → no httprouter
		// wildcard conflict with /discovery/keyword/:id.
		if blocklistHandler != nil {
			guarded.POST("/discovery/blocklist", blocklistHandler.Create)
			guarded.GET("/discovery/blocklist", blocklistHandler.List)
			guarded.DELETE("/discovery/blocklist/:id", blocklistHandler.Delete)
			guarded.GET("/discovery/keyword-search", blocklistHandler.KeywordSearch)
		}
		// Story 520 (N-4c) — POST add-to-sonarr. Nil-OK pattern: when
		// wiring did not construct the handler (test bootstrap) the
		// route is omitted rather than 5xx-stubbed.
		if addToSonarrHandler != nil {
			guarded.POST("/discovery/add-to-sonarr", permRequest, addToSonarrHandler.Handle)
		}
		// Ф6-R-6a — movie vertical: add-to-radarr (nil-OK, matches add-to-sonarr).
		if addToRadarrHandler != nil {
			guarded.POST("/discovery/add-to-radarr", permRequest, addToRadarrHandler.Handle)
		}
		// Ф6-R-6b — global movie library list. Distinct path segment from the
		// /movies/:tmdb_id wildcard so ordering is irrelevant; grouped with the
		// other /movies routes for readability.
		if movieLibraryHandler != nil {
			guarded.GET("/movies", movieLibraryHandler.List)
		}
		// Ф6-R-6a — movie release calendar. Register the static /movies/calendar
		// BEFORE the /movies/:tmdb_id wildcard (static-before-wildcard).
		if movieCalendarHandler != nil {
			guarded.GET("/movies/calendar", movieCalendarHandler.Get)
		}
		// Ф6-R-6a — movie detail aggregate. Static /movies/* siblings (e.g.
		// /movies/calendar above) register before this wildcard. Nil-OK: the
		// route is omitted for minimal/test wirings.
		if movieDetailHandler != nil {
			guarded.GET("/movies/:tmdb_id", movieDetailHandler.Get)
		}
		// Ф2.1 — movie cast sub-endpoint (decomposition 1/N). Deeper static
		// segment under the :tmdb_id param — coexists with /movies/:tmdb_id in
		// gin's radix tree exactly as /series/:id/cast does with /series/:id.
		// First live wiring of the Ф2.0 movie ETag adapter (cast section →
		// enrichment_cast_synced_at); movieEtagFreshness nil-OK → pass-through.
		if movieCastHandler != nil {
			movieEtagMW := ETagMiddleware("tmdb_id", movieEtagFreshness, logger)
			guarded.GET("/movies/:tmdb_id/cast", movieEtagMW, movieCastHandler.Get)
		}
		// Ф2.2 — movie overview sub-endpoint. Reuses the movie ETag adapter
		// (overview section → enrichment_text_synced_at); edge.extractSection
		// maps the /overview suffix to sectionOverview automatically.
		if movieOverviewHandler != nil {
			movieOverviewEtagMW := ETagMiddleware("tmdb_id", movieEtagFreshness, logger)
			guarded.GET("/movies/:tmdb_id/overview", movieOverviewEtagMW, movieOverviewHandler.Get)
		}
		// Ф2.3 — movie ratings sub-endpoint. Ratings live on the canon row (not
		// localized, not per-instance, not stamp-cacheable), so — exactly like
		// /series/:id/ratings — this route carries NO ETag middleware.
		if movieRatingsHandler != nil {
			guarded.GET("/movies/:tmdb_id/ratings", movieRatingsHandler.Get)
		}
		// Ф2.4 — movie recommendations sub-endpoint. ETag-cached off the recs
		// section stamp: edge.extractSection maps the /recommendations suffix to
		// sectionRecs, and the movie adapter maps "recs" → enrichment_recs_synced_at.
		if movieRecommendationsHandler != nil {
			movieRecsEtagMW := ETagMiddleware("tmdb_id", movieEtagFreshness, logger)
			guarded.GET("/movies/:tmdb_id/recommendations", movieRecsEtagMW, movieRecommendationsHandler.Get)
		}
		// Ф6-R-6a — TMDB franchise collections.
		if movieCollectionsHandler != nil {
			guarded.GET("/collections/:tmdb_collection_id", movieCollectionsHandler.Get)
			guarded.POST("/collections/:tmdb_collection_id/add-all-missing", permRequest, movieCollectionsHandler.AddAllMissing)
			guarded.PUT("/collections/:tmdb_collection_id/monitor", permRequest, movieCollectionsHandler.Monitor)
		}
		if qbitSettings != nil {
			guarded.GET("/instances/:name/qbit/settings", qbitSettings.Get)
			guarded.PUT("/instances/:name/qbit/settings", permAdmin, qbitSettings.Upsert)
			guarded.DELETE("/instances/:name/qbit/settings", permAdmin, qbitSettings.Delete)
		}
		// Story 519 (N-4b) — per-instance metadata cache surface for the
		// AddToSonarrModal pickers (quality profiles + root folders) +
		// operator-driven cache invalidation. Nil-OK pattern mirrors
		// qbitSettings so test wiring can omit the routes.
		if instanceMetadataHandler != nil {
			guarded.GET("/instances/:name/quality-profiles", instanceMetadataHandler.GetQualityProfiles)
			guarded.GET("/instances/:name/root-folders", instanceMetadataHandler.GetRootFolders)
			guarded.POST("/instances/:name/refresh-metadata", permAdmin, instanceMetadataHandler.RefreshMetadata)
			// Story 524 N-4 per-season picker — uncached lookup proxy.
			guarded.GET("/instances/:name/sonarr-lookup", instanceMetadataHandler.SonarrLookup)
		}
		if externalServices != nil {
			guarded.GET("/external-services", externalServices.List)
			guarded.PUT("/external-services/:service", permAdmin, externalServices.Upsert)
			guarded.POST("/external-services/:service/test", permAdmin, externalServices.Test)
		}
		guarded.GET("/instances/:name/watchdog/rollups", watchdogRollupHandler.One)
		guarded.GET("/watchdog/rollups", watchdogRollupHandler.All)
		guarded.GET("/instances/:name/watchdog/blacklist", watchdogBlacklistHandler.List)
		guarded.DELETE("/instances/:name/watchdog/blacklist/:series/:season", permAdmin, watchdogBlacklistHandler.Delete)
		if watchdogSeasonsHandler != nil {
			guarded.GET("/watchdog/seasons", watchdogSeasonsHandler.List)
			guarded.GET("/watchdog/series/:instance/:id", watchdogSeasonsHandler.Series)
		}
		guarded.GET("/webhooks/status", webhooksAggregateHandler.Status)
		guarded.GET("/scans", auditHandler.ListScans)
		guarded.GET("/scans/:id", auditHandler.GetScan)
		guarded.GET("/decisions", auditHandler.ListDecisions)
		guarded.GET("/decisions/:id", auditHandler.GetDecision)
		guarded.GET("/grabs", auditHandler.ListGrabs)
		guarded.GET("/counters", countersHandler.Aggregate)
		guarded.GET("/insights/health", insightsHealthHandler.Get)
		guarded.GET("/insights/gaps", insightsGapsHandler.Get)
		guarded.GET("/insights/stats", insightsStatsHandler.Get)
		guarded.GET("/insights/lists", insightsListsHandler.Get)
		guarded.GET("/insights/collections", insightsCollectionsHandler.Get)
		guarded.GET("/calendar", insightsCalendarHandler.Get)
		// ADR-0015 Ф3 S3 (F-14) — ICS subscription feed. Public
		// token-authenticated consume + guarded mint/revoke. Built only
		// when BOTH the calendar repo and the ics-epoch repo are wired
		// (nil-OK: routes omitted in minimal/test wirings). The ICS
		// signing key is HKDF-derived with a DISTINCT info label so an
		// ICS token can never validate as a session cookie.
		if calendarRepo != nil && icsEpochRepo != nil {
			icsKey, kerr := crypto.DeriveICSTokenKey(cfg.Auth.APIKey)
			if kerr != nil {
				panic("http.NewServer: derive ICS token key: " + kerr.Error())
			}
			icsHandler := catalogrest.NewICSHandler(
				icsfeed.NewUseCase(calendar.NewUseCase(calendarRepo), icsEpochRepo, icsKey),
				logger,
			)
			// PUBLIC — registered on `api` (NO RequireAuth). ICS clients
			// send no cookies/headers; the signed token IS the credential.
			api.GET("/calendar.ics", icsHandler.Consume)
			// Guarded — operator mints/revokes from an authenticated session.
			guarded.GET("/calendar.ics/token", icsHandler.Mint)
			guarded.POST("/calendar.ics/revoke", icsHandler.Revoke)
		}
		// Story 492 / N-1b — per-instance grab episode-files moved to
		// the global namespace (`/grabs/:id/episode-files`); see route
		// registration in the N-1b block above. The per-instance
		// handler struct stays in `internal/grab/rest/grab_episode_files.go`
		// for its own test coverage but is no longer reached via any
		// HTTP route.
		guarded.POST("/decisions/:id/grab", permAdmin, grabHandler.ByDecision)
		rescanHandler := watchdogrest.NewRescanHandler(rescanUC, logger)
		guarded.POST("/decisions/:id/rescan", permAdmin, rescanHandler.ByDecision)
		guarded.POST("/scans/:id/cancel", permAdmin, scanHandler.Cancel)
		guarded.GET("/config/runtime", runtimeConfigHandler.Get)
		guarded.PUT("/config/runtime", permAdmin, runtimeConfigHandler.Update)
		if timezoneHandler != nil {
			guarded.GET("/settings/timezone", timezoneHandler.Get)
			guarded.PATCH("/settings/timezone", permAdmin, timezoneHandler.Patch)
		}

		// Story 485 (N-7a) — current-user profile + settings patch +
		// change-password. Nil-OK pattern mirrors timezoneHandler: when
		// the wirer skipped construction (test / minimal boot) the routes
		// are omitted rather than 5xx-stubbed.
		//
		// /me/change-password reuses the SAME per-IP passwordLimiter the
		// legacy /auth/password sits behind via a tiny adapter (gin doesn't
		// expose the parent group's middleware as a slice).
		if meHandler != nil {
			guarded.GET("/me", meHandler.Get)
			guarded.PATCH("/me/settings", meHandler.UpdateSettings)
			guarded.POST("/me/change-password",
				passwordLimiterMiddleware(passwordLimiter),
				meHandler.ChangePassword,
			)
		}

		oidcTestHandler := adminrest.NewOIDCTestHandler(authHandler.AuthRuntime(), logger)
		guarded.POST("/auth/oidc/test", permAdmin, oidcTestHandler.Test)

		// Webhook on the shared auth surface + per-instance rate limit.
		wh := api.Group("/webhook/sonarr/:instance_name")
		// Webhook is mode-invariant AND local-bypass-invariant per
		// D-3 / AC-8 — X-Api-Key only. RequireAuthWebhook pins the
		// local-bypass branch off so a local IP can NEVER POST a
		// webhook without a valid X-Api-Key. Mode dispatch still
		// runs but in practice Sonarr always sends the key.
		wh.Use(middleware.RequireAuthWebhook(
			cfg.Auth.APIKey, sessionKey, authHandler.AuthRuntime(),
			adminRepo, loginLimiter,
		))
		if webhookLimiter != nil {
			wh.Use(webhookRateLimit(webhookLimiter))
		}
		wh.POST("", webhookHandler.Handle)

		// Ф6-R-4b: Radarr Connect webhook — same durable inbox, same auth
		// surface + per-instance rate limit as the Sonarr webhook.
		rwh := api.Group("/webhook/radarr/:instance_name")
		rwh.Use(middleware.RequireAuthWebhook(
			cfg.Auth.APIKey, sessionKey, authHandler.AuthRuntime(),
			adminRepo, loginLimiter,
		))
		if webhookLimiter != nil {
			rwh.Use(webhookRateLimit(webhookLimiter))
		}
		rwh.POST("", radarrWebhookHandler.Handle)
	} else {
		// HIGH-S1: if an operator flips auth.enabled=false they get an
		// unusable service (only /healthz, /readyz, /metrics). Make the
		// state loud at startup so it's not mistaken for "API broken".
		// auth.enabled=false is documented as a testing-only mode; the
		// production config keeps it true.
		logger.Warn("auth disabled — only /healthz, /readyz, /metrics exposed; API routes NOT registered",
			slog.String("hint", "set http.auth.enabled=true to expose /api/v1/*"))
	}

	srv := &http.Server{
		Addr:         cfg.Bind,
		Handler:      r,
		ReadTimeout:  cfg.ReadTimeout,
		WriteTimeout: cfg.WriteTimeout,
		IdleTimeout:  cfg.IdleTimeout,
	}
	return &Server{cfg: cfg, server: srv, engine: r, authHandler: serverAuthHandler, logger: logger}
}

// passwordLimiterMiddleware adapts an *auth.IPLimiter into a Gin
// middleware that mirrors the inline 429 envelope used by the legacy
// PasswordChange handler. Pulled out so /me/change-password reuses the
// SAME limiter instance constructed in NewServer rather than allocating
// a parallel one.
//
// The limiter is nil-OK so test wiring that omits cfg.Auth.Enabled gets
// a pass-through middleware. Story 485 (N-7a).
func passwordLimiterMiddleware(lim *auth.IPLimiter) gin.HandlerFunc {
	return func(c *gin.Context) {
		if lim != nil && !lim.Allow(c.ClientIP()) {
			c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{
				"error": "rate limit exceeded", "code": "RATE_LIMITED",
			})
			return
		}
		c.Next()
	}
}

// probeRateLimit reuses the login limiter so a brute-forcer can't
// turn POST /instances/test into a side-channel oracle on internal
// URLs. Keyed on ClientIP (same as Login).
func probeRateLimit(lim *auth.IPLimiter) gin.HandlerFunc {
	return func(c *gin.Context) {
		if lim == nil {
			c.Next()
			return
		}
		if !lim.Allow(c.ClientIP()) {
			c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{
				"error": "rate limit exceeded", "code": "RATE_LIMITED",
			})
			return
		}
		c.Next()
	}
}

// webhookRateLimit keys on :instance_name. IP-keyed would be wrong
// here — Sonarr always comes from the same IP, but per-instance keeps
// one rogue instance from starving the others.
func webhookRateLimit(lim *auth.IPLimiter) gin.HandlerFunc {
	return func(c *gin.Context) {
		if !lim.Allow(c.Param("instance_name")) {
			c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{"error": "rate limit exceeded"})
			return
		}
		c.Next()
	}
}

func (s *Server) Start() error {
	s.logger.Info("starting http server", slog.String("addr", s.cfg.Bind))
	if err := s.server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

func (s *Server) Shutdown(ctx context.Context) error {
	timeout := s.cfg.ShutdownTimeout
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	sctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	return s.server.Shutdown(sctx)
}

// Engine returns the underlying gin engine. The reload
// authMiddleware subscriber calls SetTrustedProxies on the engine
// when `trusted_proxies` changes; no other caller should reach for
// this — every legitimate handler is registered at construction.
func (s *Server) Engine() *gin.Engine {
	return s.engine
}

// AuthHandler returns the handler if auth is enabled, or nil
// otherwise. Used by the reload subscriber to obtain the shared
// AuthRuntime pointer for in-process TTL swaps.
func (s *Server) AuthHandler() *adminrest.AuthHandler {
	return s.authHandler
}

// reconcileContextMiddleware extracts the seasonfill public URL from
// X-Forwarded-Proto + X-Forwarded-Host (falling back to Request.Host
// + TLS state) and stashes it on the request context under the key
// webhookinstall.PublicURLFromContext reads. This is the bridge that
// lets the reconciler — which lives in the application layer — get
// the per-request public URL without depending on gin.Context.
//
// The CRUD path (POST/PUT /instances) needs the same hook so the sync
// reconcile inside instance.UseCase has a public URL to derive from.
// Apply via guarded.Use(...) instead of per-route for the CRUD group
// — see the wiring change below.
func reconcileContextMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		host := strings.TrimSpace(c.GetHeader("X-Forwarded-Host"))
		if host == "" {
			host = strings.TrimSpace(c.Request.Host)
		}
		scheme := strings.TrimSpace(c.GetHeader("X-Forwarded-Proto"))
		if scheme == "" {
			if c.Request.TLS != nil {
				scheme = "https"
			} else {
				scheme = "http"
			}
		}
		if host != "" {
			ctx := context.WithValue(c.Request.Context(),
				webhookinstall.RequestPublicURLKey{},
				scheme+"://"+host)
			c.Request = c.Request.WithContext(ctx)
		}
		c.Next()
	}
}
