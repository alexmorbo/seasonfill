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
	return NewServer(cfg, nil, nil, nil, nil, nil, nil,
		admin, nil, nil,
		catalogrest.InstanceRegistry{},
		nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, // cooldown, grab, rescan, instanceCRUD, instanceProbe, runtimeConfig, qbitSettings, externalServices, oidcUC, webhookReconciler, webhookStatusCache
		nil, nil, // seriesCacheRepo, counterRepo
		nil, nil, nil, nil, // watchdogRollupHandler, watchdogBlacklistHandler, watchdogSeasonsHandler, webhooksAggregateHandler
		nil,      // mediaHandler (Story 214 F-1)
		nil,      // mediaPending (Story 352, nil-OK)
		nil, nil, // seriesSeasonHandler (215 G-1) + peopleHandler (217 H-2)
		nil, // peopleHandler (Story 217 H-2)
		nil, // seriesRefreshHandler (Story 218 E-2)
		nil, // seriesTorrentsHandler (Story 222 A-4)
		nil, // timezoneHandler (Story 301)
		nil, // meHandler (Story 485 N-7a)
		nil, // sharedAuthRuntime (Story 485 N-7a)
		nil, // globalSeriesHandler (Story 491 N-1a)
		nil, // globalOverviewHandler (Story 529)
		nil, // globalRecommendationsHandler (Story 530)
		nil, // globalLibraryHandler (Story 577 E-1-B2)
		nil, // seasonsHandler (Story 582 E-1 B3c)
		nil, // discoveryHandler (Story 507 N-2f)
		nil, // discoverHandler (Story 509 N-2h)
		nil, // instanceMetadataHandler (Story 519 N-4b)
		nil, // addToSonarrHandler (Story 520 N-4c)
		nil, // etagFreshness (Story 578 E-1-B5) — nil-OK pass-through
		nil, // seriesTitleLocalizer (Story E-1-B7) — nil-OK pass-through
		nil, // seriesMediaLocalizer (Story 584b) — nil-OK pass-through
		logger)
}
