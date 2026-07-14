package edge

import (
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	catalogrest "github.com/alexmorbo/seasonfill/internal/catalog/rest"
	"github.com/alexmorbo/seasonfill/internal/config"
	"github.com/alexmorbo/seasonfill/internal/runtime/crypto"
	"github.com/alexmorbo/seasonfill/internal/shared/http/middleware"
)

// buildServerWithEpoch mirrors newServerForTest but injects a real shared
// AuthRuntime pointer and a boot SessionEpoch so the F-03 seed is exercised.
func buildServerWithEpoch(t *testing.T, apiKey string, epoch int64, ptr *middleware.AuthRuntimePointer) *Server {
	t.Helper()
	cfg := config.HTTPConfig{
		Bind: "127.0.0.1:0",
		Auth: config.AuthConfig{
			Enabled:      true,
			APIKey:       apiKey,
			SessionTTL:   time.Hour,
			SessionEpoch: epoch,
		},
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelError}))
	admin := &stubAdminRepo{}
	// Arg list mirrors the real NewServer signature; only cfg, adminRepo,
	// instanceReg, sharedAuthRuntime (ptr) and logger are non-nil so the
	// F-03 seed path fires.
	return NewServer(
		cfg,                            // cfg
		nil,                            // scanUC
		nil,                            // webhookUC
		nil,                            // checker
		nil,                            // scanRepo
		nil,                            // decisionRepo
		nil,                            // grabRepo
		admin,                          // adminRepo
		nil,                            // loginLimiter
		nil,                            // webhookLimiter
		catalogrest.InstanceRegistry{}, // instanceReg
		nil,                            // cooldownRepo
		nil,                            // grabUC
		nil,                            // rescanUC
		nil,                            // instanceCRUD
		nil,                            // instanceProbe
		nil,                            // runtimeConfigHandler
		nil,                            // qbitSettings
		nil,                            // externalServices
		nil,                            // oidcUC
		nil,                            // webhookReconciler
		nil,                            // webhookStatusCache
		nil,                            // seriesCacheRepo
		nil,                            // counterRepo
		nil,                            // watchdogRollupHandler
		nil,                            // watchdogBlacklistHandler
		nil,                            // watchdogSeasonsHandler
		nil,                            // webhooksAggregateHandler
		nil,                            // mediaHandler
		nil,                            // mediaPending
		nil,                            // peopleHandler
		nil,                            // seriesTorrentsHandler
		nil,                            // timezoneHandler
		nil,                            // meHandler
		ptr,                            // sharedAuthRuntime — real pointer so the F-03 seed fires
		nil,                            // globalSeriesHandler
		nil,                            // globalCastHandler
		nil,                            // globalSeasonHandler
		nil,                            // globalOverviewHandler
		nil,                            // globalRecommendationsHandler
		nil,                            // globalRatingsHandler
		nil,                            // globalLibraryHandler
		nil,                            // seasonsHandler
		nil,                            // resolveHandler
		nil,                            // discoveryHandler
		nil,                            // discoverHandler
		nil,                            // instanceMetadataHandler
		nil,                            // addToSonarrHandler
		nil,                            // etagFreshness
		nil,                            // seriesTitleLocalizer
		nil,                            // seriesMediaLocalizer
		logger,
	)
}

// TestNewServer_SeedsBootSessionEpoch proves the shared AuthRuntime is seeded
// with the boot app_config epoch (not 0) at server-build time, and that a
// pre-bump epoch-0 cookie is rejected by VerifySession against that live epoch.
func TestNewServer_SeedsBootSessionEpoch(t *testing.T) {
	t.Parallel()
	const apiKey = "secret"
	const bootEpoch int64 = 5

	ptr := &middleware.AuthRuntimePointer{}
	srv := buildServerWithEpoch(t, apiKey, bootEpoch, ptr)
	require.NotNil(t, srv)

	rt := ptr.Load()
	require.NotNil(t, rt, "seed must store an AuthRuntime")
	require.Equal(t, bootEpoch, rt.SessionEpoch,
		"boot seed must carry the app_config epoch, not the default 0")

	// A pre-bump cookie minted under epoch 0 must be rejected against the
	// seeded live epoch — this is the boot-window hole F-03 closes.
	sessionKey, err := crypto.DeriveSessionHMACKey(apiKey)
	require.NoError(t, err)
	staleTok, err := middleware.SignSession(sessionKey, "admin", time.Now().Add(time.Hour), 0)
	require.NoError(t, err)

	_, verr := middleware.VerifySession(sessionKey, staleTok, time.Now(), rt.SessionEpoch)
	require.ErrorIs(t, verr, middleware.ErrSessionEpoch,
		"epoch-0 cookie must be rejected at boot when the live epoch is 5")

	// Sanity: a cookie minted at the CURRENT epoch still validates.
	freshTok, err := middleware.SignSession(sessionKey, "admin", time.Now().Add(time.Hour), bootEpoch)
	require.NoError(t, err)
	_, verr = middleware.VerifySession(sessionKey, freshTok, time.Now(), rt.SessionEpoch)
	require.NoError(t, verr, "a cookie minted at the seeded epoch must validate")
}
