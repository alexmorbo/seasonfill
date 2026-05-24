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
	for _, c := range []string{"scheduler", "sonarrClients", "healthRegistry",
		"scanInstances", "globalRateLimiter", "authMiddleware"} {
		if scrapeCounter(t, c) == 0 {
			return false
		}
	}
	return true
}

func scrapeReloadCounters(t *testing.T) map[string]int64 {
	t.Helper()
	out := map[string]int64{}
	for _, c := range []string{"scheduler", "sonarrClients", "healthRegistry",
		"scanInstances", "globalRateLimiter", "authMiddleware"} {
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
