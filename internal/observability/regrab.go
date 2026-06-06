package observability

import "github.com/VictoriaMetrics/metrics"

// Watchdog metric names. Frozen by parent 039 §Metrics — adding a new
// label key here breaks Grafana queries. New label *values* are fine.
const (
	MetricWatchdogPollTotal             = `seasonfill_watchdog_poll_total`
	MetricWatchdogUnregisteredTotal     = `seasonfill_watchdog_unregistered_detected_total`
	MetricWatchdogRegrabTotal           = `seasonfill_watchdog_regrab_triggered_total`
	MetricWatchdogBlacklistSize         = `seasonfill_watchdog_blacklist_size`
	MetricWatchdogQbitUnreachableStreak = `seasonfill_watchdog_qbit_unreachable_streak`
)

// Poll result values — emitted as the `result` label on
// MetricWatchdogPollTotal. Single Go const block to prevent
// typo-drift between call sites.
const (
	WatchdogPollResultOK        = "ok"
	WatchdogPollResultQbitError = "qbit_error"
	WatchdogPollResultSkipped   = "skipped"
)

// IncWatchdogPollResult bumps the poll counter. result must be one of
// the WatchdogPollResult* constants above.
func IncWatchdogPollResult(instance, result string) {
	metrics.GetOrCreateCounter(`seasonfill_watchdog_poll_total{instance="` + instance + `",result="` + result + `"}`).Inc()
}

// IncWatchdogUnregisteredDetected bumps the unregistered-detection
// counter. tracker is the lowercased host portion of the announce
// URL (the regrab use case extracts + normalises it via net/url).
func IncWatchdogUnregisteredDetected(instance, tracker string) {
	metrics.GetOrCreateCounter(`seasonfill_watchdog_unregistered_detected_total{instance="` + instance + `",tracker="` + tracker + `"}`).Inc()
}

// IncWatchdogRegrabResult bumps the regrab-result counter. result must
// be a regrab.OutcomeReason string value (the use case casts the typed
// enum to string at the call site so this signature stays string-only).
func IncWatchdogRegrabResult(instance, result string) {
	metrics.GetOrCreateCounter(`seasonfill_watchdog_regrab_triggered_total{instance="` + instance + `",result="` + result + `"}`).Inc()
}

// SetWatchdogBlacklistSize replaces the per-instance blacklist size
// gauge. Called by the regrab subscriber at the end of each successful
// RunInstance — the use case is the source of truth for the count.
func SetWatchdogBlacklistSize(instance string, size int) {
	metrics.GetOrCreateGauge(`seasonfill_watchdog_blacklist_size{instance="`+instance+`"}`, nil).Set(float64(size))
}

// SetWatchdogQbitUnreachableStreak replaces the per-instance qBit
// unreachable-streak gauge. Reset to 0 on the first successful poll
// after one or more failures.
func SetWatchdogQbitUnreachableStreak(instance string, streak int) {
	metrics.GetOrCreateGauge(`seasonfill_watchdog_qbit_unreachable_streak{instance="`+instance+`"}`, nil).Set(float64(streak))
}

// WatchdogMetricsAdapter satisfies application/regrab.Metrics by
// dispatching to the package-level helpers above. The regrab use case
// constructor takes the interface; cmd/server passes a value of this
// type. Zero value is fully functional — no fields, no constructor
// required.
type WatchdogMetricsAdapter struct{}

func (WatchdogMetricsAdapter) IncPollResult(instance, result string) {
	IncWatchdogPollResult(instance, result)
}

func (WatchdogMetricsAdapter) IncUnregistered(instance, tracker string) {
	IncWatchdogUnregisteredDetected(instance, tracker)
}

func (WatchdogMetricsAdapter) IncRegrabResult(instance, result string) {
	IncWatchdogRegrabResult(instance, result)
}

func (WatchdogMetricsAdapter) SetBlacklistSize(instance string, size int) {
	SetWatchdogBlacklistSize(instance, size)
}

func (WatchdogMetricsAdapter) SetQbitUnreachableStreak(instance string, streak int) {
	SetWatchdogQbitUnreachableStreak(instance, streak)
}
