package observability

import (
	"github.com/VictoriaMetrics/metrics"

	"github.com/alexmorbo/seasonfill/internal/shared/clients/qbit"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
)

// Torrentsync metric names. Frozen by Story B-32 — adding a new label
// key here breaks Grafana queries. New label *values* are fine.
//
// The naming follows the seasonfill convention:
//   - `_total` suffix => counter
//   - `_seconds` suffix on a histogram => duration in seconds
//   - bare names => gauges
const (
	MetricTorrentsyncUnmapped              = `seasonfill_torrentsync_unmapped`
	MetricTorrentsyncRefreshDurationSecond = `seasonfill_torrentsync_refresh_duration_seconds`
	MetricTorrentsyncTorrentsTotal         = `seasonfill_torrentsync_torrents_total`
	MetricTorrentsyncDeltaTotal            = `seasonfill_torrentsync_delta_total`
	MetricTorrentsyncLastRefreshAtSeconds  = `seasonfill_torrentsync_last_refresh_at_seconds`
	MetricTorrentsyncUnmappedTotal         = `seasonfill_torrentsync_unmapped_total`
)

// Refresh outcome label values. Closed set — the loop measures both
// success and failure paths and the histogram carries the outcome
// label so operators can plot "p99 of failed refreshes" separately.
const (
	TorrentsyncOutcomeOK    = "ok"
	TorrentsyncOutcomeError = "error"
)

// Delta op label values. Closed set. {insert, update, delete} mirrors
// the qBit MainData diff semantics — see story 479a Investigation Notes.
const (
	TorrentsyncDeltaOpInsert = "insert"
	TorrentsyncDeltaOpUpdate = "update"
	TorrentsyncDeltaOpDelete = "delete"
)

// SetTorrentsyncUnmapped replaces the gauge value for the named
// instance. Called from application/torrentsync.Reconciler.run (Story
// 221, pre-dates B-32).
func SetTorrentsyncUnmapped(instance domain.InstanceName, count int) {
	metrics.GetOrCreateGauge(`seasonfill_torrentsync_unmapped{instance="`+string(instance)+`"}`, nil).Set(float64(count))
}

// ObserveTorrentsyncRefreshDuration records one iterate() duration.
// outcome MUST be one of TorrentsyncOutcomeOK / TorrentsyncOutcomeError.
// Histogram so Grafana can plot percentiles; VictoriaMetrics auto-
// chooses log-spaced buckets covering ~100us..~10min, which dominates
// the 100ms..30s expected range for a healthy refresh.
func ObserveTorrentsyncRefreshDuration(instance domain.InstanceName, outcome string, seconds float64) {
	metrics.GetOrCreateHistogram(
		`seasonfill_torrentsync_refresh_duration_seconds{instance="` + string(instance) + `",outcome="` + outcome + `"}`,
	).Update(seconds)
}

// SetTorrentsyncTorrentsByState replaces the per-(instance, state)
// gauge. state MUST be one of qbit.StateGroup* values (closed set of
// 8). The use case calls this for every state group on every
// successful Refresh — gauges not touched on a failed Refresh stay at
// their last-good value (intentional; see story 479a Investigation Notes).
func SetTorrentsyncTorrentsByState(instance domain.InstanceName, state qbit.StateGroup, count int) {
	metrics.GetOrCreateGauge(
		`seasonfill_torrentsync_torrents_total{instance="`+string(instance)+`",state="`+string(state)+`"}`, nil,
	).Set(float64(count))
}

// AddTorrentsyncDelta bumps the per-op counter by n. op MUST be one of
// TorrentsyncDeltaOp* constants. n <= 0 is a no-op so callers can
// pass an unconditional delta size.
func AddTorrentsyncDelta(instance domain.InstanceName, op string, n int) {
	if n <= 0 {
		return
	}
	metrics.GetOrCreateCounter(
		`seasonfill_torrentsync_delta_total{instance="` + string(instance) + `",op="` + op + `"}`,
	).Add(n)
}

// SetTorrentsyncLastRefreshAt publishes the Unix epoch seconds of the
// last successful refresh for the instance. Useful for alerting via
// `time() - seasonfill_torrentsync_last_refresh_at_seconds > 600`
// (10min). On a failed refresh the gauge is NOT touched — staleness
// detection is the entire reason this gauge exists.
func SetTorrentsyncLastRefreshAt(instance domain.InstanceName, unixSec int64) {
	metrics.GetOrCreateGauge(
		`seasonfill_torrentsync_last_refresh_at_seconds{instance="`+string(instance)+`"}`, nil,
	).Set(float64(unixSec))
}

// AddTorrentsyncUnmappedDetected bumps the newly-detected-unmapped
// counter by n. Increments per refresh by the count of hashes seen
// THIS tick that were not previously in the store. n <= 0 is a no-op.
//
// Distinct from the gauge: gauge = "how many unmapped right now",
// counter = "how often does the loop see fresh unmapped". A steady-
// state backlog gives counter rate ≈ 0; a flood gives counter rate
// spike. See story 479a Investigation Notes.
func AddTorrentsyncUnmappedDetected(instance domain.InstanceName, n int) {
	if n <= 0 {
		return
	}
	metrics.GetOrCreateCounter(
		`seasonfill_torrentsync_unmapped_total{instance="` + string(instance) + `"}`,
	).Add(n)
}

// TorrentsyncMetricsAdapter satisfies the application/torrentsync
// Metrics port and the legacy reconciler UnmappedGauge. Zero value is
// fully functional — pass it by value at construction.
//
// The adapter intentionally does NOT know the metric names — it
// dispatches to the package-level helpers above so a test can rebind
// behaviour at the helper level without re-implementing the adapter.
type TorrentsyncMetricsAdapter struct{}

// SetTorrentsyncUnmapped implements the legacy reconciler narrow port.
func (TorrentsyncMetricsAdapter) SetTorrentsyncUnmapped(instance domain.InstanceName, count int) {
	SetTorrentsyncUnmapped(instance, count)
}

// ObserveRefreshDuration implements torrentsync.Metrics.
func (TorrentsyncMetricsAdapter) ObserveRefreshDuration(instance domain.InstanceName, outcome string, seconds float64) {
	ObserveTorrentsyncRefreshDuration(instance, outcome, seconds)
}

// SetTorrentsByState implements torrentsync.Metrics.
func (TorrentsyncMetricsAdapter) SetTorrentsByState(instance domain.InstanceName, state qbit.StateGroup, count int) {
	SetTorrentsyncTorrentsByState(instance, state, count)
}

// AddDelta implements torrentsync.Metrics.
func (TorrentsyncMetricsAdapter) AddDelta(instance domain.InstanceName, op string, n int) {
	AddTorrentsyncDelta(instance, op, n)
}

// SetLastRefreshAt implements torrentsync.Metrics.
func (TorrentsyncMetricsAdapter) SetLastRefreshAt(instance domain.InstanceName, unixSec int64) {
	SetTorrentsyncLastRefreshAt(instance, unixSec)
}

// AddUnmappedDetected implements torrentsync.Metrics.
func (TorrentsyncMetricsAdapter) AddUnmappedDetected(instance domain.InstanceName, n int) {
	AddTorrentsyncUnmappedDetected(instance, n)
}

// SetSessionAge implements torrentsync.Metrics (story 479b). Dispatches
// to the qBit session telemetry helper so a single adapter value
// covers both the torrentsync loop metrics AND the qBit session age
// gauge — no extra port wiring at the constructor site.
func (TorrentsyncMetricsAdapter) SetSessionAge(instance domain.InstanceName, ageSec float64) {
	SetQbitSessionAge(instance, ageSec)
}
