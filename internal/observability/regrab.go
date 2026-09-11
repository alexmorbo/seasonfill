package observability

import (
	"github.com/VictoriaMetrics/metrics"

	"github.com/alexmorbo/seasonfill/internal/shared/domain"
)

// Watchdog metric names. Frozen by parent 039 §Metrics — adding a new
// label key here breaks Grafana queries. New label *values* are fine.
const (
	MetricWatchdogPollTotal             = `seasonfill_watchdog_poll_total`
	MetricWatchdogUnregisteredTotal     = `seasonfill_watchdog_unregistered_detected_total`
	MetricWatchdogRegrabTotal           = `seasonfill_watchdog_regrab_triggered_total`
	MetricWatchdogBlacklistSize         = `seasonfill_watchdog_blacklist_size`
	MetricWatchdogQbitUnreachableStreak = `seasonfill_watchdog_qbit_unreachable_streak`
	MetricWatchdogCooldownPending       = `seasonfill_watchdog_cooldown_pending`
	MetricWatchdogRegrabCandidates      = `seasonfill_watchdog_regrab_candidates`

	// MetricRegrabUnresolvedInstance is the ADR-0025 F2 gauge: 1 while the
	// regrab loop refuses to run against an instance whose arr type it does
	// not support, 0 once that instance stops being skipped.
	//
	// Deliberately NOT in the seasonfill_watchdog_* family of its
	// neighbours: ADR-0025 F4 names this series verbatim in its alert rule
	// (`seasonfill_regrab_unresolved_instance > 0`). Renaming it for prefix
	// symmetry would silently break that rule before it is even written.
	MetricRegrabUnresolvedInstance = `seasonfill_regrab_unresolved_instance`
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
func IncWatchdogPollResult(instance domain.InstanceName, result string) {
	metrics.GetOrCreateCounter(`seasonfill_watchdog_poll_total{instance="` + string(instance) + `",result="` + result + `"}`).Inc()
}

// IncWatchdogUnregisteredDetected bumps the unregistered-detection
// counter. tracker is the lowercased host portion of the announce
// URL (the regrab use case extracts + normalises it via net/url).
func IncWatchdogUnregisteredDetected(instance domain.InstanceName, tracker string) {
	metrics.GetOrCreateCounter(`seasonfill_watchdog_unregistered_detected_total{instance="` + string(instance) + `",tracker="` + tracker + `"}`).Inc()
}

// IncWatchdogRegrabResult bumps the regrab-result counter. result must
// be a regrab.OutcomeReason string value (the use case casts the typed
// enum to string at the call site so this signature stays string-only).
func IncWatchdogRegrabResult(instance domain.InstanceName, result string) {
	metrics.GetOrCreateCounter(`seasonfill_watchdog_regrab_triggered_total{instance="` + string(instance) + `",result="` + result + `"}`).Inc()
}

// SetWatchdogBlacklistSize replaces the per-instance blacklist size
// gauge. Called by the regrab subscriber at the end of each successful
// RunInstance — the use case is the source of truth for the count.
func SetWatchdogBlacklistSize(instance domain.InstanceName, size int) {
	metrics.GetOrCreateGauge(`seasonfill_watchdog_blacklist_size{instance="`+string(instance)+`"}`, nil).Set(float64(size))
}

// SetWatchdogQbitUnreachableStreak replaces the per-instance qBit
// unreachable-streak gauge. Reset to 0 on the first successful poll
// after one or more failures.
func SetWatchdogQbitUnreachableStreak(instance domain.InstanceName, streak int) {
	metrics.GetOrCreateGauge(`seasonfill_watchdog_qbit_unreachable_streak{instance="`+string(instance)+`"}`, nil).Set(float64(streak))
}

// SetWatchdogCooldownPending replaces the per-instance gauge for the
// count of (series, season) cooldowns currently active in the
// regrab_retry scope. Published by the periodic watchdog state
// collector (cmd/server/loops/watchdog_state_collector.go) every 5
// minutes. Source query: cooldown table where scope=regrab_retry and
// expires_at > now, grouped by the instance segment of the key.
func SetWatchdogCooldownPending(instance domain.InstanceName, count int) {
	metrics.GetOrCreateGauge(
		`seasonfill_watchdog_cooldown_pending{instance="`+string(instance)+`"}`, nil,
	).Set(float64(count))
}

// SetWatchdogRegrabCandidates replaces the per-instance gauge for the
// count of unregistered torrents detected on the LAST completed
// regrab cycle. Equivalent to RunResult.UnregisteredCount. Published
// by cmd/server/loops/regrab.go after each iterate. The value can be
// 0 — that's the steady-state "all good" reading.
func SetWatchdogRegrabCandidates(instance domain.InstanceName, count int) {
	metrics.GetOrCreateGauge(
		`seasonfill_watchdog_regrab_candidates{instance="`+string(instance)+`"}`, nil,
	).Set(float64(count))
}

// SetRegrabUnresolvedInstance replaces the per-instance gauge that marks an
// instance present in qbit_settings which the regrab loop skips because
// regrab does not support its arr type (ADR-0025 F2). Published by
// cmd/server/loops/regrab.go from SwapSettings: 1 on every swap while the
// instance stays unsupported, 0 exactly once when it stops being skipped
// (removed, disabled, or its type changed).
//
// The reset to 0 is mandatory, not cosmetic — the F4 alert rule is
// `seasonfill_regrab_unresolved_instance > 0`, so a gauge left at 1 would
// alert forever after the operator fixed the instance.
func SetRegrabUnresolvedInstance(instance domain.InstanceName, value int) {
	metrics.GetOrCreateGauge(
		`seasonfill_regrab_unresolved_instance{instance="`+string(instance)+`"}`, nil,
	).Set(float64(value))
}

// WatchdogMetricsAdapter satisfies application/regrab.Metrics by
// dispatching to the package-level helpers above. The regrab use case
// constructor takes the interface; cmd/server passes a value of this
// type. Zero value is fully functional — no fields, no constructor
// required.
type WatchdogMetricsAdapter struct{}

func (WatchdogMetricsAdapter) IncPollResult(instance domain.InstanceName, result string) {
	IncWatchdogPollResult(instance, result)
}

func (WatchdogMetricsAdapter) IncUnregistered(instance domain.InstanceName, tracker string) {
	IncWatchdogUnregisteredDetected(instance, tracker)
}

func (WatchdogMetricsAdapter) IncRegrabResult(instance domain.InstanceName, result string) {
	IncWatchdogRegrabResult(instance, result)
}

func (WatchdogMetricsAdapter) SetBlacklistSize(instance domain.InstanceName, size int) {
	SetWatchdogBlacklistSize(instance, size)
}

func (WatchdogMetricsAdapter) SetQbitUnreachableStreak(instance domain.InstanceName, streak int) {
	SetWatchdogQbitUnreachableStreak(instance, streak)
}

func (WatchdogMetricsAdapter) SetCooldownPending(instance domain.InstanceName, count int) {
	SetWatchdogCooldownPending(instance, count)
}

func (WatchdogMetricsAdapter) SetRegrabCandidates(instance domain.InstanceName, count int) {
	SetWatchdogRegrabCandidates(instance, count)
}

func (WatchdogMetricsAdapter) SetRegrabUnresolvedInstance(instance domain.InstanceName, value int) {
	SetRegrabUnresolvedInstance(instance, value)
}
