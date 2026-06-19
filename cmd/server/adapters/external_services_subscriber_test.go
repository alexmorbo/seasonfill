package adapters

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	appextsvc "github.com/alexmorbo/seasonfill/application/externalservices"
	apports "github.com/alexmorbo/seasonfill/application/ports"
	infraextsvc "github.com/alexmorbo/seasonfill/infrastructure/externalservices"
)

// TestExternalServicesSubscriber_RegisterListener_FiresAfterApply
// verifies the Story 352 listener fan-out: a registered listener fires
// for the correct service AND only that service.
func TestExternalServicesSubscriber_RegisterListener_FiresAfterApply(t *testing.T) {
	t.Parallel()
	logger := slog.New(slog.NewJSONHandler(discardWriter{}, &slog.HandlerOptions{Level: slog.LevelDebug}))
	sub := NewExternalServicesSubscriber(nil, logger)

	var (
		omdbCalls atomic.Int32
		tmdbCalls atomic.Int32
		mu        sync.Mutex
		omdbSeen  infraextsvc.Settings
	)
	sub.RegisterListener(infraextsvc.ServiceOMDB, func(ctx context.Context, s infraextsvc.Settings) {
		omdbCalls.Add(1)
		mu.Lock()
		omdbSeen = s
		mu.Unlock()
	})
	sub.RegisterListener(infraextsvc.ServiceTMDB, func(ctx context.Context, s infraextsvc.Settings) {
		tmdbCalls.Add(1)
	})

	// Simulate an apply by directly calling the unexported method via
	// the bus-less Publish/test seam: prime current with hand-built
	// values then re-publish.
	sub.SetCurrentForTest(map[infraextsvc.Service]infraextsvc.Settings{
		infraextsvc.ServiceOMDB: {Service: infraextsvc.ServiceOMDB, Enabled: true, APIKey: "k_omdb"},
		infraextsvc.ServiceTMDB: {Service: infraextsvc.ServiceTMDB, Enabled: true, APIKey: "k_tmdb"},
	})
	sub.FanOutForTest(context.Background())

	assert.Equal(t, int32(1), omdbCalls.Load())
	assert.Equal(t, int32(1), tmdbCalls.Load())
	mu.Lock()
	assert.Equal(t, "k_omdb", omdbSeen.APIKey)
	mu.Unlock()
}

// TestExternalServicesSubscriber_RegisterListener_NilFnIsDropped
// guards against a nil callback panicking the apply path.
func TestExternalServicesSubscriber_RegisterListener_NilFnIsDropped(t *testing.T) {
	t.Parallel()
	logger := slog.New(slog.NewJSONHandler(discardWriter{}, &slog.HandlerOptions{Level: slog.LevelDebug}))
	sub := NewExternalServicesSubscriber(nil, logger)
	// Should be a no-op.
	sub.RegisterListener(infraextsvc.ServiceOMDB, nil)
	sub.SetCurrentForTest(map[infraextsvc.Service]infraextsvc.Settings{
		infraextsvc.ServiceOMDB: {Service: infraextsvc.ServiceOMDB},
	})
	// Just check no panic.
	sub.FanOutForTest(context.Background())
}

// stubExtSvcRepo is a minimal Repository implementation for the
// log-assertion test below. Returns ErrNotFound for every service so
// the use case's EffectiveSettingsWithSource path exercises the env
// fallback branch unconditionally.
type stubExtSvcRepo struct{}

func (stubExtSvcRepo) Get(_ context.Context, _ infraextsvc.Service) (infraextsvc.Settings, error) {
	return infraextsvc.Settings{}, apports.ErrNotFound
}
func (stubExtSvcRepo) List(_ context.Context) ([]infraextsvc.Settings, error) {
	return nil, nil
}
func (stubExtSvcRepo) Upsert(_ context.Context, _ infraextsvc.Settings) error { return nil }
func (stubExtSvcRepo) MarkTest(_ context.Context, _ infraextsvc.Service, _ time.Time, _ infraextsvc.Outcome, _ string) error {
	return nil
}

// stubExtSvcTester satisfies appextsvc.Tester for the use case
// constructor. Never invoked from apply path.
type stubExtSvcTester struct{}

func (stubExtSvcTester) Test(context.Context, infraextsvc.Settings) (infraextsvc.Outcome, string, time.Duration) {
	return infraextsvc.OutcomeOK, "", 0
}

// TestExternalServicesSubscriber_ApplyLogsSource asserts that
// apply() emits one extsvc.source INFO record per service with the
// resolved FieldSource label for every field (FIX-007 task #503).
// Fresh-install env-fallback path: env supplies TMDB token + OMDb
// token; TVDB stays empty.
func TestExternalServicesSubscriber_ApplyLogsSource(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo}))

	env := func(name string) string {
		switch name {
		case "SEASONFILL_TMDB_TOKEN":
			return "tmdb-env"
		case "SEASONFILL_OMDB_TOKEN":
			return "omdb-env"
		}
		return ""
	}
	uc := appextsvc.NewUseCase(stubExtSvcRepo{}, env, stubExtSvcTester{}, nil, logger)

	sub := NewExternalServicesSubscriber(nil, logger)
	sub.SetUseCase(uc)

	sub.Publish(context.Background())

	out := buf.String()
	// One extsvc.source record per service.
	for _, svc := range []string{"tmdb", "omdb", "tvdb"} {
		needle := `"msg":"extsvc.source","service":"` + svc + `"`
		if !strings.Contains(out, needle) {
			t.Fatalf("missing extsvc.source for %s; log=%s", svc, out)
		}
	}
	// TMDB + OMDb api_key must report env source; TVDB reports none.
	if !strings.Contains(out, `"service":"tmdb","enabled":true,"api_key":"env"`) {
		t.Fatalf("expected tmdb api_key=env in log; log=%s", out)
	}
	if !strings.Contains(out, `"service":"omdb","enabled":true,"api_key":"env"`) {
		t.Fatalf("expected omdb api_key=env in log; log=%s", out)
	}
	if !strings.Contains(out, `"service":"tvdb","enabled":false,"api_key":""`) {
		t.Fatalf("expected tvdb api_key='' (none) in log; log=%s", out)
	}
	// Plaintext token must never appear in the log.
	if strings.Contains(out, "tmdb-env") || strings.Contains(out, "omdb-env") {
		t.Fatalf("plaintext token leaked into log; log=%s", out)
	}
}
