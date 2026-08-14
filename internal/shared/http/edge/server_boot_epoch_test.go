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
		nil,                            // webhookInbox
		nil,                            // webhookTxr
		nil,                            // webhookPoke
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
		nil,                            // healthRepo (Story I-1a)
		nil,                            // gapRepo (Story I-2a)
		nil,                            // statsRepo (Story I-4)
		nil,                            // smartListsRepo (Story I-3)
		nil,                            // collectionsRepo (Story I-5)
		nil,                            // calendarRepo (ADR-0015 Ф3 S2)
		nil,                            // watchdogRollupHandler
		nil,                            // watchdogBlacklistHandler
		nil,                            // watchdogSeasonsHandler
		nil,                            // webhooksAggregateHandler
		nil,                            // mediaHandler
		nil,                            // mediaPending
		nil,                            // peopleHandler
		nil,                            // seriesTorrentsHandler
		nil,                            // torrentActionHandler (ADR-0013 Q2)
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
		nil,                            // monitorSeasonHandler
		nil,                            // seasonsHandler
		nil,                            // resolveHandler
		nil,                            // discoveryHandler
		nil,                            // discoverHandler
		nil,                            // movieDiscoverHandler (Ф6-R-4a L3-1)
		nil,                            // rowConfigHandler (ADR-0017 Ф5 D-1)
		nil,                            // blocklistHandler (ADR-0017 Ф5 S3)
		nil,                            // instanceMetadataHandler
		nil,                            // addToSonarrHandler
		nil,                            // movieDetailHandler (Ф6-R-6a)
		nil,                            // addToRadarrHandler (Ф6-R-6a)
		nil,                            // movieCalendarHandler (Ф6-R-6a)
		nil,                            // movieCollectionsHandler (Ф6-R-6a)
		nil,                            // etagFreshness
		nil,                            // seriesTitleLocalizer
		nil,                            // seriesMediaLocalizer
		nil,                            // followHandler (ADR-0015 Ф3 C1)
		nil,                            // icsEpochRepo (ADR-0015 Ф3 S3)
		nil,                            // notificationAgentsHandler (ADR-0016 Ф4 N1)
		nil,                            // radarrConfigLookup (Ф6-R-6b Gap 2a)
		nil,                            // movieLibraryHandler (Ф6-R-6b)
		nil,                            // requestHandler (Ф8-U-2)
		nil,                            // usersHandler (Ф8-U-6b)
		nil,                            // movieReenrichHandler (Ф1.4)
		nil,                            // insightsCalendarResolver (Ф0.1)
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
