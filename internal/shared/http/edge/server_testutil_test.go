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
		nil,           // mediaHandler (Story 214 F-1)
		nil,           // mediaPending (Story 352, nil-OK)
		nil, nil, nil, // seriesDetailHandler + seriesSeasonHandler (215 G-1) + seriesCastHandler (216 H-1)
		nil, // peopleHandler (Story 217 H-2)
		nil, // seriesRefreshHandler (Story 218 E-2)
		nil, // seriesTorrentsHandler (Story 222 A-4)
		nil, // timezoneHandler (Story 301)
		nil, // meHandler (Story 485 N-7a)
		nil, // sharedAuthRuntime (Story 485 N-7a)
		nil, // globalSeriesHandler (Story 491 N-1a)
		nil, // discoveryHandler (Story 507 N-2f)
		nil, // discoverHandler (Story 509 N-2h)
		nil, // instanceMetadataHandler (Story 519 N-4b)
		nil, // addToSonarrHandler (Story 520 N-4c)
		logger)
}
