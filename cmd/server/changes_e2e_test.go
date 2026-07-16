//go:build integration

package main

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/cmd/server/loops"
	"github.com/alexmorbo/seasonfill/internal/observability"
)

func changesE2EEnv(t *testing.T) {
	t.Helper()
	t.Setenv("SEASONFILL_DATABASE_DRIVER", "sqlite")
	t.Setenv("SEASONFILL_DATABASE_SQLITE_PATH", t.TempDir()+"/test.db")
	t.Setenv("SEASONFILL_API_KEY", "test-api-key-32-bytes-padding-aaaa")
	t.Setenv("SEASONFILL_WEB_USER", "admin")
	t.Setenv("SEASONFILL_WEB_PASSWORD", "test-password-12chars")
	t.Setenv("SEASONFILL_HTTP_BIND", "127.0.0.1:0")
	t.Setenv("SEASONFILL_LOG_LEVEL", "warn")
}

// scrapeChangesPollTotal sums seasonfill_tmdb_changes_poll_total across all
// result labels (0 when the metric was never emitted).
func scrapeChangesPollTotal(t *testing.T) int64 {
	t.Helper()
	var buf bytes.Buffer
	observability.WritePrometheus(&buf)
	var sum int64
	for _, line := range strings.Split(buf.String(), "\n") {
		if strings.HasPrefix(line, "seasonfill_tmdb_changes_poll_total") {
			parts := strings.Fields(line)
			if len(parts) >= 2 {
				sum += parseInt(t, parts[len(parts)-1])
			}
		}
	}
	return sum
}

func scrapeChangesResult(t *testing.T, result string) int64 {
	t.Helper()
	var buf bytes.Buffer
	observability.WritePrometheus(&buf)
	prefix := `seasonfill_tmdb_changes_poll_total{result="` + result + `"}`
	return parseFirstMatch(t, buf.String(), prefix)
}

// G4 zero-regression headline: with SEASONFILL_TMDB_CHANGES_ENABLED unset
// (default false) the poller loop is NEVER registered — poll_total does not
// advance even after a window that comfortably exceeds the (shrunk) startup
// delay. The delay is shrunk so that IF the loop were wrongly registered it
// WOULD tick inside the wait window and fail this assertion.
func TestChanges_E2E_DisabledByDefault(t *testing.T) {
	changesE2EEnv(t)
	// ENABLED deliberately unset.
	defer loops.SetChangesStartupDelayForTest(20 * time.Millisecond)()

	before := scrapeChangesPollTotal(t)
	_, stop := bootForTest(t)
	defer stop()

	time.Sleep(500 * time.Millisecond) // 25x the shrunk startup delay
	after := scrapeChangesPollTotal(t)
	assert.Equal(t, before, after,
		"poll_total must not advance when SEASONFILL_TMDB_CHANGES_ENABLED is unset (G4)")
}

// ENABLED=true + no TMDB key: the loop registers, ticks, and the ClientReady
// holder-gate emits skipped_no_client — no crash. Direct contrast with the
// disabled case above.
func TestChanges_E2E_EnabledNoKey(t *testing.T) {
	changesE2EEnv(t)
	t.Setenv("SEASONFILL_TMDB_CHANGES_ENABLED", "true")
	// No TMDB key configured → holder empty → ClientReady()==false.
	defer loops.SetChangesStartupDelayForTest(20 * time.Millisecond)()

	before := scrapeChangesResult(t, "skipped_no_client")
	_, stop := bootForTest(t)
	defer stop()

	require.Eventually(t, func() bool {
		return scrapeChangesResult(t, "skipped_no_client") > before
	}, 5*time.Second, 50*time.Millisecond,
		"enabled poller with no TMDB key must emit poll_total{result=skipped_no_client}")
}

// Graceful shutdown: with the poller enabled, cancelling rootCtx (SIGTERM
// standin) drains the loop within 5s — no goroutine leak, no panic on cancel.
func TestChanges_E2E_GracefulShutdown(t *testing.T) {
	changesE2EEnv(t)
	t.Setenv("SEASONFILL_TMDB_CHANGES_ENABLED", "true")
	defer loops.SetChangesStartupDelayForTest(20 * time.Millisecond)()

	_, stop := bootForTest(t)
	start := time.Now()
	stop()
	assert.Less(t, time.Since(start), 5*time.Second,
		"enabled changes poller must drain on shutdown in <5s")
}
