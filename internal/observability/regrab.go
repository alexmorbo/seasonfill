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
