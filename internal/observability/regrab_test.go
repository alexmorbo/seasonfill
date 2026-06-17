package observability

import (
	"bytes"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func dumpMetrics(t *testing.T) string {
	t.Helper()
	var buf bytes.Buffer
	WritePrometheus(&buf)
	return buf.String()
}

func TestWatchdogPollResult_EmitsCounter(t *testing.T) {
	t.Parallel()
	IncWatchdogPollResult("alpha", WatchdogPollResultOK)
	IncWatchdogPollResult("alpha", WatchdogPollResultQbitError)
	IncWatchdogPollResult("alpha", WatchdogPollResultOK)

	out := dumpMetrics(t)
	assert.Contains(t, out, `seasonfill_watchdog_poll_total{instance="alpha",result="ok"}`)
	assert.Contains(t, out, `seasonfill_watchdog_poll_total{instance="alpha",result="qbit_error"}`)
}

func TestWatchdogUnregistered_EmitsCounter(t *testing.T) {
	t.Parallel()
	IncWatchdogUnregisteredDetected("alpha", "tracker.example.com")

	out := dumpMetrics(t)
	assert.Contains(t, out,
		`seasonfill_watchdog_unregistered_detected_total{instance="alpha",tracker="tracker.example.com"}`)
}

func TestWatchdogRegrabResult_EmitsCounter(t *testing.T) {
	t.Parallel()
	IncWatchdogRegrabResult("alpha", "grabbed")
	IncWatchdogRegrabResult("alpha", "nothing_better")

	out := dumpMetrics(t)
	assert.Contains(t, out, `seasonfill_watchdog_regrab_triggered_total{instance="alpha",result="grabbed"}`)
	assert.Contains(t, out, `seasonfill_watchdog_regrab_triggered_total{instance="alpha",result="nothing_better"}`)
}

func TestWatchdogBlacklistSize_EmitsGauge(t *testing.T) {
	t.Parallel()
	SetWatchdogBlacklistSize("alpha", 3)

	out := dumpMetrics(t)
	assert.Contains(t, out, `seasonfill_watchdog_blacklist_size{instance="alpha"}`)
	// Gauge value sanity — should be present as " 3" (Prometheus text
	// format trails the value after the label set).
	assert.True(t, strings.Contains(out, `seasonfill_watchdog_blacklist_size{instance="alpha"} 3`),
		"gauge value should be 3, got:\n%s", out)
}

func TestWatchdogStreak_EmitsGauge(t *testing.T) {
	t.Parallel()
	SetWatchdogQbitUnreachableStreak("alpha", 5)
	SetWatchdogQbitUnreachableStreak("alpha", 0) // recovery

	out := dumpMetrics(t)
	assert.Contains(t, out, `seasonfill_watchdog_qbit_unreachable_streak{instance="alpha"} 0`)
}

func TestWatchdogMetricsAdapter_DelegatesAll(t *testing.T) {
	t.Parallel()
	var a WatchdogMetricsAdapter
	a.IncPollResult("beta", "ok")
	a.IncUnregistered("beta", "t.example")
	a.IncRegrabResult("beta", "filter_dropped")
	a.SetBlacklistSize("beta", 1)
	a.SetQbitUnreachableStreak("beta", 2)

	out := dumpMetrics(t)
	assert.Contains(t, out, `seasonfill_watchdog_poll_total{instance="beta",result="ok"}`)
	assert.Contains(t, out, `seasonfill_watchdog_unregistered_detected_total{instance="beta",tracker="t.example"}`)
	assert.Contains(t, out, `seasonfill_watchdog_regrab_triggered_total{instance="beta",result="filter_dropped"}`)
	assert.Contains(t, out, `seasonfill_watchdog_blacklist_size{instance="beta"}`)
	assert.Contains(t, out, `seasonfill_watchdog_qbit_unreachable_streak{instance="beta"} 2`)
}
