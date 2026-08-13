package edge

import (
	"log/slog"
	"os"
	"testing"

	catalogrest "github.com/alexmorbo/seasonfill/internal/catalog/rest"
	"github.com/alexmorbo/seasonfill/internal/config"
)

// newServerForTest builds a Server with auth enabled and nil deps —
// docs_test.go reads engine.Routes() only; handlers are never invoked.
func newServerForTest(t *testing.T, apiKey string) *Server {
	t.Helper()
	cfg := config.HTTPConfig{
		Bind: "127.0.0.1:0",
		Auth: config.AuthConfig{Enabled: true, APIKey: apiKey},
	}
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	admin := &stubAdminRepo{}
	return NewServer(cfg, nil, nil, nil, nil, nil, nil, nil, nil,
		admin, nil, nil,
		catalogrest.InstanceRegistry{},
		nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, // cooldown, grab, rescan, instanceCRUD, instanceProbe, runtimeConfig, qbitSettings, externalServices, oidcUC, webhookReconciler, webhookStatusCache
		nil, nil, nil, nil, nil, nil, nil, nil, // seriesCacheRepo, counterRepo, healthRepo (I-1a), gapRepo (I-2a), statsRepo (I-4), smartListsRepo (I-3), collectionsRepo (I-5), calendarRepo (S2)
		nil, nil, nil, nil, // watchdogRollupHandler, watchdogBlacklistHandler, watchdogSeasonsHandler, webhooksAggregateHandler
		nil,      // mediaHandler (Story 214 F-1)
		nil,      // mediaPending (Story 352, nil-OK)
		nil, nil, // seriesSeasonHandler (215 G-1) + peopleHandler (217 H-2)
		nil, // peopleHandler (Story 217 H-2)
		nil, // seriesTorrentsHandler (Story 222 A-4)
		nil, // torrentActionHandler (ADR-0013 Q2)
		nil, // timezoneHandler (Story 301)
		nil, // meHandler (Story 485 N-7a)
		nil, // sharedAuthRuntime (Story 485 N-7a)
		nil, // globalSeriesHandler (Story 491 N-1a)
		nil, // globalOverviewHandler (Story 529)
		nil, // globalRecommendationsHandler (Story 530)
		nil, // globalRatingsHandler (W18-7a)
		nil, // globalLibraryHandler (Story 577 E-1-B2)
		nil, // monitorSeasonHandler (ADR-0012 S1)
		nil, // seasonsHandler (Story 582 E-1 B3c)
		nil, // resolveHandler (BE-3 card-unification)
		nil, // discoveryHandler (Story 507 N-2f)
		nil, // discoverHandler (Story 509 N-2h)
		nil, // movieDiscoverHandler (Ф6-R-4a L3-1)
		nil, // rowConfigHandler (ADR-0017 Ф5 D-1)
		nil, // blocklistHandler (ADR-0017 Ф5 S3)
		nil, // instanceMetadataHandler (Story 519 N-4b)
		nil, // addToSonarrHandler (Story 520 N-4c)
		nil, // movieDetailHandler (Ф6-R-6a)
		nil, // addToRadarrHandler (Ф6-R-6a)
		nil, // movieCalendarHandler (Ф6-R-6a)
		nil, // movieCollectionsHandler (Ф6-R-6a)
		nil, // etagFreshness (Story 578 E-1-B5) — nil-OK pass-through
		nil, // seriesTitleLocalizer (Story E-1-B7) — nil-OK pass-through
		nil, // seriesMediaLocalizer (Story 584b) — nil-OK pass-through
		nil, // followHandler (ADR-0015 Ф3 C1) — nil-OK, routes omitted
		nil, // icsEpochRepo (ADR-0015 Ф3 S3) — nil-OK, routes omitted
		nil, // notificationAgentsHandler (ADR-0016 Ф4 N1) — nil-OK, routes omitted
		nil, // radarrConfigLookup (Ф6-R-6b Gap 2a) — nil-OK, sonarr-only list
		nil, // movieLibraryHandler (Ф6-R-6b) — nil-OK, /movies route omitted
		nil, // requestHandler (Ф8-U-2) — nil-OK, /requests routes omitted
		nil, // usersHandler (Ф8-U-6b) — nil-OK, /admin/users routes omitted
		nil, // insightsCalendarResolver (Ф0.1) — nil-OK, raw poster paths flow through
		logger)
}
