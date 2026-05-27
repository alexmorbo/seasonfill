//go:build integration

package main

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/internal/observability"
	"github.com/alexmorbo/seasonfill/internal/runtime"
)

// TestReload_E2E_PublishFiresAllSubscribers boots the full server
// against an in-memory SQLite DB, publishes a fresh snapshot on the
// bus, and asserts every per-component success counter is non-zero
// while every error counter stays at zero.
func TestReload_E2E_PublishFiresAllSubscribers(t *testing.T) {
	t.Setenv("SEASONFILL_DATABASE_DRIVER", "sqlite")
	t.Setenv("SEASONFILL_DATABASE_SQLITE_PATH", t.TempDir()+"/test.db")
	t.Setenv("SEASONFILL_API_KEY", "test-api-key-32-bytes-padding-aaaa")
	t.Setenv("SEASONFILL_WEB_USER", "admin")
	t.Setenv("SEASONFILL_WEB_PASSWORD", "test-password-12chars")
	t.Setenv("SEASONFILL_HTTP_BIND", "127.0.0.1:0")
	t.Setenv("SEASONFILL_LOG_LEVEL", "warn")

	bus, stop := bootForTest(t)
	defer stop()

	// Boot publish has already landed by the time bootForTest returns
	// (the barrier in startSubscribers + the boot Publish guarantee it).
	require.True(t, allSubscribersGreen(t),
		"all 6 subscribers must have applied the boot snapshot before runForTest exposed the bus")

	// Publish a synthetic snapshot and confirm counters increment AGAIN.
	prev := scrapeReloadCounters(t)
	bus.Publish(context.Background(), runtime.Snapshot{
		Cron:            runtime.CronSnapshot{Enabled: true, Schedule: "0 */6 * * *", Jitter: time.Minute},
		GlobalRateLimit: runtime.RateLimitSnapshot{RPM: 30, Burst: 10},
		Auth:            runtime.AuthSnapshot{SessionTTL: 12 * time.Hour, TrustedProxies: []string{"127.0.0.1"}},
	})
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		now := scrapeReloadCounters(t)
		if everyCounterAdvanced(prev, now) {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	now := scrapeReloadCounters(t)
	assert.True(t, everyCounterAdvanced(prev, now), "publish must fire every subscriber: prev=%v now=%v", prev, now)
	// No errors anywhere.
	for component := range now {
		assert.Equal(t, int64(0), scrapeErrorCounter(t, component), "no errors on %q", component)
	}
}

// TestReload_E2E_GracefulShutdown verifies that cancelling the
// rootCtx (the standin for SIGTERM) drains every subscriber
// goroutine within 5s.
func TestReload_E2E_GracefulShutdown(t *testing.T) {
	t.Setenv("SEASONFILL_DATABASE_DRIVER", "sqlite")
	t.Setenv("SEASONFILL_DATABASE_SQLITE_PATH", t.TempDir()+"/test.db")
	t.Setenv("SEASONFILL_API_KEY", "test-api-key-32-bytes-padding-aaaa")
	t.Setenv("SEASONFILL_WEB_USER", "admin")
	t.Setenv("SEASONFILL_WEB_PASSWORD", "test-password-12chars")
	t.Setenv("SEASONFILL_HTTP_BIND", "127.0.0.1:0")

	_, stop := bootForTest(t)
	start := time.Now()
	stop()
	elapsed := time.Since(start)
	assert.Less(t, elapsed, 5*time.Second, "shutdown must drain subscribers in <5s")
}

// --- helpers ---

func allSubscribersGreen(t *testing.T) bool {
	t.Helper()
	for _, c := range []string{"scheduler", "sonarrClients",
		"globalRateLimiter", "authMiddleware"} {
		if scrapeCounter(t, c) == 0 {
			return false
		}
	}
	return true
}

func scrapeReloadCounters(t *testing.T) map[string]int64 {
	t.Helper()
	out := map[string]int64{}
	for _, c := range []string{"scheduler", "sonarrClients",
		"globalRateLimiter", "authMiddleware"} {
		out[c] = scrapeCounter(t, c)
	}
	return out
}

func everyCounterAdvanced(prev, now map[string]int64) bool {
	for k, p := range prev {
		if now[k] <= p {
			return false
		}
	}
	return true
}

func scrapeCounter(t *testing.T, component string) int64 {
	t.Helper()
	var buf bytes.Buffer
	observability.WritePrometheus(&buf)
	prefix := `seasonfill_reload_total{component="` + component + `"}`
	return parseFirstMatch(t, buf.String(), prefix)
}

func scrapeErrorCounter(t *testing.T, component string) int64 {
	t.Helper()
	var buf bytes.Buffer
	observability.WritePrometheus(&buf)
	prefix := `seasonfill_reload_errors_total{component="` + component + `"}`
	return parseFirstMatch(t, buf.String(), prefix)
}

func parseFirstMatch(t *testing.T, scrape, prefix string) int64 {
	t.Helper()
	for _, line := range strings.Split(scrape, "\n") {
		if strings.HasPrefix(line, prefix) {
			parts := strings.Fields(line)
			if len(parts) < 2 {
				continue
			}
			return parseInt(t, parts[1])
		}
	}
	return 0
}

func parseInt(t *testing.T, s string) int64 {
	t.Helper()
	var n int64
	for _, ch := range s {
		if ch < '0' || ch > '9' {
			break
		}
		n = n*10 + int64(ch-'0')
	}
	return n
}

// bootForTest spawns runForTest in a goroutine, waits for the bus
// to be wired, and returns the live bus + a stop closure. The stop
// closure cancels the context and waits for runForTest to return.
func bootForTest(t *testing.T) (*runtime.Bus, func()) {
	t.Helper()
	var (
		busRef *runtime.Bus
		mu     sync.Mutex
	)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		err := runForTest(ctx, func(b *runtime.Bus) {
			mu.Lock()
			busRef = b
			mu.Unlock()
		})
		if err != nil && err != context.Canceled {
			t.Errorf("runForTest: %v", err)
		}
	}()

	// Wait for bus to be wired.
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		b := busRef
		mu.Unlock()
		if b != nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	mu.Lock()
	bus := busRef
	mu.Unlock()
	require.NotNil(t, bus, "runForTest failed to expose bus within 10s")

	return bus, func() {
		cancel()
		<-done
	}
}

// silenceLogger returns a slog.Logger that discards output. Used by
// tests that don't need log output to avoid polluting test output.
func silenceLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(bytes.NewBuffer(nil), &slog.HandlerOptions{Level: slog.LevelError}))
}

// bootForTestWithContext is like bootForTest but captures TestContext.
// Requires testContextHook and TestContext from testcontext_hook.go.
func bootForTestWithContext(t *testing.T) (*TestContext, func()) {
	t.Helper()
	var (
		tcRef *TestContext
		mu    sync.Mutex
	)
	testContextHook = func(tc *TestContext) {
		mu.Lock()
		tcRef = tc
		mu.Unlock()
	}
	t.Cleanup(func() { testContextHook = nil })

	_, stop := bootForTest(t)

	mu.Lock()
	tc := tcRef
	mu.Unlock()
	require.NotNil(t, tc, "testContextHook was not called within bootForTest")
	return tc, stop
}

// TestReload_E2E_SchedulerScheduleUpdated asserts that after publishing a
// snapshot with a different cron schedule, the SchedulerSubscriber's live
// scheduler reflects the new schedule.
func TestReload_E2E_SchedulerScheduleUpdated(t *testing.T) {
	t.Setenv("SEASONFILL_DATABASE_DRIVER", "sqlite")
	t.Setenv("SEASONFILL_DATABASE_SQLITE_PATH", t.TempDir()+"/test.db")
	t.Setenv("SEASONFILL_API_KEY", "test-api-key-32-bytes-padding-aaaa")
	t.Setenv("SEASONFILL_WEB_USER", "admin")
	t.Setenv("SEASONFILL_WEB_PASSWORD", "test-password-12chars")
	t.Setenv("SEASONFILL_HTTP_BIND", "127.0.0.1:0")
	t.Setenv("SEASONFILL_LOG_LEVEL", "warn")

	tc, stop := bootForTestWithContext(t)
	defer stop()

	tc.Bus.Publish(t.Context(), runtime.Snapshot{
		Cron: runtime.CronSnapshot{Enabled: true, Schedule: "*/5 * * * *", Jitter: 0},
		Auth: runtime.AuthSnapshot{SessionTTL: 12 * time.Hour},
	})

	require.Eventually(t, func() bool {
		cur := tc.SubSched.Current()
		return cur != nil && cur.Schedule() == "*/5 * * * *"
	}, 2*time.Second, 50*time.Millisecond, "scheduler must adopt new schedule")
}

// TestReload_E2E_AuthTTLUpdated asserts that after publishing a snapshot with
// a different SessionTTL, the auth runtime pointer reflects the new value.
func TestReload_E2E_AuthTTLUpdated(t *testing.T) {
	t.Setenv("SEASONFILL_DATABASE_DRIVER", "sqlite")
	t.Setenv("SEASONFILL_DATABASE_SQLITE_PATH", t.TempDir()+"/test.db")
	t.Setenv("SEASONFILL_API_KEY", "test-api-key-32-bytes-padding-aaaa")
	t.Setenv("SEASONFILL_WEB_USER", "admin")
	t.Setenv("SEASONFILL_WEB_PASSWORD", "test-password-12chars")
	t.Setenv("SEASONFILL_HTTP_BIND", "127.0.0.1:0")
	t.Setenv("SEASONFILL_LOG_LEVEL", "warn")

	tc, stop := bootForTestWithContext(t)
	defer stop()

	tc.Bus.Publish(t.Context(), runtime.Snapshot{
		Cron: runtime.CronSnapshot{Enabled: false},
		Auth: runtime.AuthSnapshot{SessionTTL: 7 * time.Hour, TrustedProxies: []string{}},
	})

	require.Eventually(t, func() bool {
		ptr := tc.AuthRuntimePtr.Load()
		return ptr != nil && ptr.SessionTTL == 7*time.Hour
	}, 2*time.Second, 50*time.Millisecond, "auth runtime must adopt new session TTL")
}

// TestReload_E2E_GlobalLimiterUpdated asserts that after publishing a snapshot
// with a non-zero RPM, the global limiter pointer is non-nil and points to a
// freshly created limiter.
func TestReload_E2E_GlobalLimiterUpdated(t *testing.T) {
	t.Setenv("SEASONFILL_DATABASE_DRIVER", "sqlite")
	t.Setenv("SEASONFILL_DATABASE_SQLITE_PATH", t.TempDir()+"/test.db")
	t.Setenv("SEASONFILL_API_KEY", "test-api-key-32-bytes-padding-aaaa")
	t.Setenv("SEASONFILL_WEB_USER", "admin")
	t.Setenv("SEASONFILL_WEB_PASSWORD", "test-password-12chars")
	t.Setenv("SEASONFILL_HTTP_BIND", "127.0.0.1:0")
	t.Setenv("SEASONFILL_LOG_LEVEL", "warn")

	tc, stop := bootForTestWithContext(t)
	defer stop()

	prev := tc.GlobalLimPtr.Load()
	tc.Bus.Publish(t.Context(), runtime.Snapshot{
		Cron:            runtime.CronSnapshot{Enabled: false},
		Auth:            runtime.AuthSnapshot{SessionTTL: 12 * time.Hour},
		GlobalRateLimit: runtime.RateLimitSnapshot{RPM: 60, Burst: 20},
	})

	require.Eventually(t, func() bool {
		next := tc.GlobalLimPtr.Load()
		return next != nil && next != prev
	}, 2*time.Second, 50*time.Millisecond, "global limiter pointer must be replaced")
}

// TestReload_E2E_HolderInstance_DryRunToggle asserts that toggling the
// per-instance DryRun field propagates through buildScanFanout into the
// instanceMapHolder so scan/grab picks up the updated value on reload.
func TestReload_E2E_HolderInstance_DryRunToggle(t *testing.T) {
	t.Setenv("SEASONFILL_DATABASE_DRIVER", "sqlite")
	t.Setenv("SEASONFILL_DATABASE_SQLITE_PATH", t.TempDir()+"/test.db")
	t.Setenv("SEASONFILL_API_KEY", "test-api-key-32-bytes-padding-aaaa")
	t.Setenv("SEASONFILL_WEB_USER", "admin")
	t.Setenv("SEASONFILL_WEB_PASSWORD", "test-password-12chars")
	t.Setenv("SEASONFILL_HTTP_BIND", "127.0.0.1:0")
	t.Setenv("SEASONFILL_LOG_LEVEL", "warn")

	tc, stop := bootForTestWithContext(t)
	defer stop()

	dryRunTrue := true
	tc.Bus.Publish(t.Context(), runtime.Snapshot{
		Cron: runtime.CronSnapshot{Enabled: false},
		Auth: runtime.AuthSnapshot{SessionTTL: 12 * time.Hour},
		Instances: []runtime.InstanceSnapshot{
			{Name: "inst-dr", URL: "http://sonarr:8989", APIKey: "k1", DryRun: &dryRunTrue},
		},
	})
	require.Eventually(t, func() bool {
		m := tc.HolderSnapshot()
		inst, ok := m["inst-dr"]
		return ok && inst.Config.DryRun != nil && *inst.Config.DryRun
	}, 2*time.Second, 50*time.Millisecond, "holder must reflect DryRun=true after first publish")

	dryRunFalse := false
	tc.Bus.Publish(t.Context(), runtime.Snapshot{
		Cron: runtime.CronSnapshot{Enabled: false},
		Auth: runtime.AuthSnapshot{SessionTTL: 12 * time.Hour},
		Instances: []runtime.InstanceSnapshot{
			{Name: "inst-dr", URL: "http://sonarr:8989", APIKey: "k1", DryRun: &dryRunFalse},
		},
	})
	require.Eventually(t, func() bool {
		m := tc.HolderSnapshot()
		inst, ok := m["inst-dr"]
		return ok && inst.Config.DryRun != nil && !*inst.Config.DryRun
	}, 2*time.Second, 50*time.Millisecond, "holder must reflect DryRun=false after toggle — scan must not see stale dry-run")
}

// TestReload_E2E_HolderInstance_LimitsToggle asserts that changing
// MaxGrabsPerScan propagates through buildScanFanout into the holder.
func TestReload_E2E_HolderInstance_LimitsToggle(t *testing.T) {
	t.Setenv("SEASONFILL_DATABASE_DRIVER", "sqlite")
	t.Setenv("SEASONFILL_DATABASE_SQLITE_PATH", t.TempDir()+"/test.db")
	t.Setenv("SEASONFILL_API_KEY", "test-api-key-32-bytes-padding-aaaa")
	t.Setenv("SEASONFILL_WEB_USER", "admin")
	t.Setenv("SEASONFILL_WEB_PASSWORD", "test-password-12chars")
	t.Setenv("SEASONFILL_HTTP_BIND", "127.0.0.1:0")
	t.Setenv("SEASONFILL_LOG_LEVEL", "warn")

	tc, stop := bootForTestWithContext(t)
	defer stop()

	tc.Bus.Publish(t.Context(), runtime.Snapshot{
		Cron: runtime.CronSnapshot{Enabled: false},
		Auth: runtime.AuthSnapshot{SessionTTL: 12 * time.Hour},
		Instances: []runtime.InstanceSnapshot{
			{Name: "inst-lim", URL: "http://sonarr:8989", APIKey: "k1",
				Limits: runtime.LimitsSnapshot{MaxGrabsPerScan: 5, ScanMaxSeries: 50}},
		},
	})
	require.Eventually(t, func() bool {
		m := tc.HolderSnapshot()
		inst, ok := m["inst-lim"]
		return ok && inst.Config.Limits.MaxGrabsPerScan == 5
	}, 2*time.Second, 50*time.Millisecond, "holder must reflect MaxGrabsPerScan=5 after first publish")

	tc.Bus.Publish(t.Context(), runtime.Snapshot{
		Cron: runtime.CronSnapshot{Enabled: false},
		Auth: runtime.AuthSnapshot{SessionTTL: 12 * time.Hour},
		Instances: []runtime.InstanceSnapshot{
			{Name: "inst-lim", URL: "http://sonarr:8989", APIKey: "k1",
				Limits: runtime.LimitsSnapshot{MaxGrabsPerScan: 20, ScanMaxSeries: 100}},
		},
	})
	require.Eventually(t, func() bool {
		m := tc.HolderSnapshot()
		inst, ok := m["inst-lim"]
		return ok && inst.Config.Limits.MaxGrabsPerScan == 20
	}, 2*time.Second, 50*time.Millisecond, "holder must reflect updated MaxGrabsPerScan=20 after reload")
}

// TestReload_E2E_HolderInstance_GUIDCooldownToggle — 032e. Asserts that
// changing Cooldown.GUIDAfterFailedImport via publish propagates through
// the OnApplied fan-out into the instanceMapHolder, which is the live
// source the webhook UC reads on every Process call. The webhook
// closure built in main.go is `holder.load()[name].Config.Cooldown
// .GUIDAfterFailedImport` — so a holder reflecting the new value is
// the necessary and sufficient invariant for reload-aware lookup.
func TestReload_E2E_HolderInstance_GUIDCooldownToggle(t *testing.T) {
	t.Setenv("SEASONFILL_DATABASE_DRIVER", "sqlite")
	t.Setenv("SEASONFILL_DATABASE_SQLITE_PATH", t.TempDir()+"/test.db")
	t.Setenv("SEASONFILL_API_KEY", "test-api-key-32-bytes-padding-aaaa")
	t.Setenv("SEASONFILL_WEB_USER", "admin")
	t.Setenv("SEASONFILL_WEB_PASSWORD", "test-password-12chars")
	t.Setenv("SEASONFILL_HTTP_BIND", "127.0.0.1:0")
	t.Setenv("SEASONFILL_LOG_LEVEL", "warn")

	tc, stop := bootForTestWithContext(t)
	defer stop()

	tc.Bus.Publish(t.Context(), runtime.Snapshot{
		Cron: runtime.CronSnapshot{Enabled: false},
		Auth: runtime.AuthSnapshot{SessionTTL: 12 * time.Hour},
		Instances: []runtime.InstanceSnapshot{
			{Name: "inst-cd", URL: "http://sonarr:8989", APIKey: "k1",
				Cooldown: runtime.CooldownSnapshot{GUIDAfterFailedImport: 24 * time.Hour}},
		},
	})
	require.Eventually(t, func() bool {
		m := tc.HolderSnapshot()
		inst, ok := m["inst-cd"]
		return ok && inst.Config.Cooldown.GUIDAfterFailedImport == 24*time.Hour
	}, 2*time.Second, 50*time.Millisecond,
		"holder must reflect GUIDAfterFailedImport=24h after first publish")

	tc.Bus.Publish(t.Context(), runtime.Snapshot{
		Cron: runtime.CronSnapshot{Enabled: false},
		Auth: runtime.AuthSnapshot{SessionTTL: 12 * time.Hour},
		Instances: []runtime.InstanceSnapshot{
			{Name: "inst-cd", URL: "http://sonarr:8989", APIKey: "k1",
				Cooldown: runtime.CooldownSnapshot{GUIDAfterFailedImport: 96 * time.Hour}},
		},
	})
	require.Eventually(t, func() bool {
		m := tc.HolderSnapshot()
		inst, ok := m["inst-cd"]
		return ok && inst.Config.Cooldown.GUIDAfterFailedImport == 96*time.Hour
	}, 2*time.Second, 50*time.Millisecond,
		"holder must reflect updated GUIDAfterFailedImport=96h after reload — webhook UC lookup must NOT keep the boot value")
}

// TestReload_E2E_ClientsViewUpdated asserts that after publishing a snapshot
// with named instances, the SonarrClientsSubscriber view reflects them.
// Uses two minimal (URL-only, no real network) instance snapshots.
func TestReload_E2E_ClientsViewUpdated(t *testing.T) {
	t.Setenv("SEASONFILL_DATABASE_DRIVER", "sqlite")
	t.Setenv("SEASONFILL_DATABASE_SQLITE_PATH", t.TempDir()+"/test.db")
	t.Setenv("SEASONFILL_API_KEY", "test-api-key-32-bytes-padding-aaaa")
	t.Setenv("SEASONFILL_WEB_USER", "admin")
	t.Setenv("SEASONFILL_WEB_PASSWORD", "test-password-12chars")
	t.Setenv("SEASONFILL_HTTP_BIND", "127.0.0.1:0")
	t.Setenv("SEASONFILL_LOG_LEVEL", "warn")

	tc, stop := bootForTestWithContext(t)
	defer stop()

	tc.Bus.Publish(t.Context(), runtime.Snapshot{
		Cron: runtime.CronSnapshot{Enabled: false},
		Auth: runtime.AuthSnapshot{SessionTTL: 12 * time.Hour},
		Instances: []runtime.InstanceSnapshot{
			{Name: "inst-a", URL: "http://sonarr-a:8989", APIKey: "k1"},
			{Name: "inst-b", URL: "http://sonarr-b:8989", APIKey: "k2"},
		},
	})

	require.Eventually(t, func() bool {
		return len(tc.ClientsView().All()) == 2
	}, 2*time.Second, 50*time.Millisecond, "clients view must have 2 instances")

	view := tc.ClientsView()
	_, okA := view.ByName("inst-a")
	_, okB := view.ByName("inst-b")
	assert.True(t, okA, "inst-a must be present in clients view")
	assert.True(t, okB, "inst-b must be present in clients view")
}

// TestReload_E2E_HolderSnapshot_SecretRotation asserts that publishing a
// snapshot with a rotated API key for the same instance name results in
// holder.load() reflecting the newly-built client, not the stale one.
// This is the regression contract for the secret-rotation cache bug.
func TestReload_E2E_HolderSnapshot_SecretRotation(t *testing.T) {
	t.Setenv("SEASONFILL_DATABASE_DRIVER", "sqlite")
	t.Setenv("SEASONFILL_DATABASE_SQLITE_PATH", t.TempDir()+"/test.db")
	t.Setenv("SEASONFILL_API_KEY", "test-api-key-32-bytes-padding-aaaa")
	t.Setenv("SEASONFILL_WEB_USER", "admin")
	t.Setenv("SEASONFILL_WEB_PASSWORD", "test-password-12chars")
	t.Setenv("SEASONFILL_HTTP_BIND", "127.0.0.1:0")
	t.Setenv("SEASONFILL_LOG_LEVEL", "warn")

	tc, stop := bootForTestWithContext(t)
	defer stop()

	// Publish initial snapshot with API key "A"
	tc.Bus.Publish(t.Context(), runtime.Snapshot{
		Cron: runtime.CronSnapshot{Enabled: false},
		Auth: runtime.AuthSnapshot{SessionTTL: 12 * time.Hour},
		Instances: []runtime.InstanceSnapshot{
			{Name: "Sonarr", URL: "http://sonarr:8989", APIKey: "A"},
		},
	})

	require.Eventually(t, func() bool {
		return len(tc.ClientsView().All()) == 1
	}, 2*time.Second, 50*time.Millisecond, "clients view must have 1 instance after first publish")

	// Verify holder reflects the initial client with key "A"
	holderA := tc.HolderSnapshot()
	instA, ok := holderA["Sonarr"]
	require.True(t, ok, "holder must contain Sonarr instance after first publish")
	assert.Equal(t, "A", instA.Config.APIKey, "initial holder must reflect APIKey A")

	// Publish second snapshot with same name but rotated key "B"
	tc.Bus.Publish(t.Context(), runtime.Snapshot{
		Cron: runtime.CronSnapshot{Enabled: false},
		Auth: runtime.AuthSnapshot{SessionTTL: 12 * time.Hour},
		Instances: []runtime.InstanceSnapshot{
			{Name: "Sonarr", URL: "http://sonarr:8989", APIKey: "B"},
		},
	})

	require.Eventually(t, func() bool {
		holderB := tc.HolderSnapshot()
		instB, ok := holderB["Sonarr"]
		return ok && instB.Config.APIKey == "B"
	}, 2*time.Second, 50*time.Millisecond, "holder must reflect rotated APIKey B after secret rotation")

	// Assert holder now reflects the rotated key, not the stale one
	holderB := tc.HolderSnapshot()
	instB, ok := holderB["Sonarr"]
	require.True(t, ok)
	assert.Equal(t, "B", instB.Config.APIKey,
		"holder must reflect the newly-rotated API key, not the stale one")
}

// TestReload_E2E_HealthRegistryReflectsLatestInstancesAfterCRUD is the
// regression contract for the CRUD recreation bug: after a DELETE
// followed by a POST with the same instance name, the health registry
// (which the /instances HTTP handler enumerates) MUST reflect the
// freshly added instance — not the empty registry that the old
// healthRegistry subscriber left behind when its goroutine raced
// ahead of sonarrClients.apply.
//
// The same test also covers the holder bundling (follow-up #143):
// after the delete-publish the holder MUST be empty, and after the
// add-publish it MUST contain the freshly added instance.
//
// Failure mode on the UNPATCHED code: after the second publish the
// CheckerSnapshot stays empty because the standalone healthRegistry
// subscriber called ReplaceClients with a stale (empty) lister view.
func TestReload_E2E_HealthRegistryReflectsLatestInstancesAfterCRUD(t *testing.T) {
	t.Setenv("SEASONFILL_DATABASE_DRIVER", "sqlite")
	t.Setenv("SEASONFILL_DATABASE_SQLITE_PATH", t.TempDir()+"/test.db")
	t.Setenv("SEASONFILL_API_KEY", "test-api-key-32-bytes-padding-aaaa")
	t.Setenv("SEASONFILL_WEB_USER", "admin")
	t.Setenv("SEASONFILL_WEB_PASSWORD", "test-password-12chars")
	t.Setenv("SEASONFILL_HTTP_BIND", "127.0.0.1:0")
	t.Setenv("SEASONFILL_LOG_LEVEL", "warn")

	tc, stop := bootForTestWithContext(t)
	defer stop()

	// Phase 1: publish with one instance "alpha".
	tc.Bus.Publish(t.Context(), runtime.Snapshot{
		Cron: runtime.CronSnapshot{Enabled: false},
		Auth: runtime.AuthSnapshot{SessionTTL: 12 * time.Hour},
		Instances: []runtime.InstanceSnapshot{
			{Name: "alpha", URL: "http://sonarr-alpha:8989", APIKey: "k1"},
		},
	})
	require.Eventually(t, func() bool {
		snap := tc.CheckerSnapshot()
		if len(snap) != 1 {
			return false
		}
		return snap[0].Name == "alpha"
	}, 2*time.Second, 25*time.Millisecond,
		"checker registry must contain alpha after the first publish")
	require.Eventually(t, func() bool {
		_, ok := tc.HolderSnapshot()["alpha"]
		return ok && len(tc.HolderSnapshot()) == 1
	}, 2*time.Second, 25*time.Millisecond,
		"holder must contain alpha after the first publish")

	// Phase 2: publish with NO instances (simulates DELETE /instances/alpha).
	// Both checker and holder must observe the empty set.
	tc.Bus.Publish(t.Context(), runtime.Snapshot{
		Cron:      runtime.CronSnapshot{Enabled: false},
		Auth:      runtime.AuthSnapshot{SessionTTL: 12 * time.Hour},
		Instances: nil,
	})
	require.Eventually(t, func() bool {
		return len(tc.CheckerSnapshot()) == 0
	}, 2*time.Second, 25*time.Millisecond,
		"checker registry must be empty after a delete-publish — proves the registry tracks the live set, not a sticky union")
	require.Eventually(t, func() bool {
		return len(tc.HolderSnapshot()) == 0
	}, 2*time.Second, 25*time.Millisecond,
		"holder must be empty after a delete-publish — follow-up #143")

	// Phase 3: publish with TWO instances [alpha, beta] (simulates a
	// POST /instances reading the new full set). After ONE publish
	// cycle the registry AND the holder must both reflect both names.
	// On the unpatched code this fails because healthRegistry won the
	// scheduler race against sonarrClients in phase 2 and called
	// ReplaceClients([], []) — the only way out was a pod restart.
	tc.Bus.Publish(t.Context(), runtime.Snapshot{
		Cron: runtime.CronSnapshot{Enabled: false},
		Auth: runtime.AuthSnapshot{SessionTTL: 12 * time.Hour},
		Instances: []runtime.InstanceSnapshot{
			{Name: "alpha", URL: "http://sonarr-alpha:8989", APIKey: "k1"},
			{Name: "beta", URL: "http://sonarr-beta:8989", APIKey: "k2"},
		},
	})
	require.Eventually(t, func() bool {
		snap := tc.CheckerSnapshot()
		if len(snap) != 2 {
			return false
		}
		got := map[string]bool{}
		for _, s := range snap {
			got[s.Name] = true
		}
		return got["alpha"] && got["beta"]
	}, 2*time.Second, 25*time.Millisecond,
		"checker registry must contain BOTH alpha and beta after the recreate-publish — the race the fix closes")

	require.Eventually(t, func() bool {
		m := tc.HolderSnapshot()
		_, a := m["alpha"]
		_, b := m["beta"]
		return a && b && len(m) == 2
	}, 2*time.Second, 25*time.Millisecond,
		"holder must contain both names after the recreate-publish")

	// Phase 4: after a publish, the per-instance LastCheckAt MUST become
	// non-zero within a short bounded wait — proves the OnApplied closure
	// spawned an immediate preflight rather than waiting for the next
	// 30s ticker. The fake URLs resolve to DNS failures, so MarkUnavailable
	// (not MarkAvailable) is what populates LastCheckAt; the assertion is
	// on non-zeroness, not on Health == Available.
	//
	// Failure mode on the UNPATCHED code: snap[i].LastCheckAt stays the
	// zero time.Time for the entire test (the periodic ticker is 30s; the
	// test budget is 2s). Post-fix: every entry has LastCheckAt set within
	// well under a second on a healthy CI host.
	require.Eventually(t, func() bool {
		snap := tc.CheckerSnapshot()
		if len(snap) != 2 {
			return false
		}
		for _, s := range snap {
			if s.LastCheckAt.IsZero() {
				return false
			}
		}
		return true
	}, 2*time.Second, 25*time.Millisecond,
		"every checker entry must have a non-zero LastCheckAt after the publish — proves the immediate preflight ran")
}
