package observability

import (
	"io"

	"github.com/VictoriaMetrics/metrics"
)

const (
	MetricScansTotal                      = `seasonfill_scans_total`
	MetricSeriesEvaluatedTotal            = `seasonfill_series_evaluated_total`
	MetricGrabsTotal                      = `seasonfill_grabs_total`
	MetricGrabAttemptsTotal               = `seasonfill_grab_attempts_total`
	MetricSonarrAPIRequestsTotal          = `seasonfill_sonarr_api_requests_total`
	MetricScanDurationSeconds             = `seasonfill_scan_duration_seconds`
	MetricSonarrAPIDuration               = `seasonfill_sonarr_api_duration_seconds`
	MetricCandidatesFound                 = `seasonfill_candidates_found`
	MetricCoverageCount                   = `seasonfill_coverage_count`
	MetricInstancesAvailable              = `seasonfill_instances_available`
	MetricActiveScans                     = `seasonfill_active_scans`
	MetricCooldownActive                  = `seasonfill_cooldown_active`
	MetricInstanceHealth                  = `seasonfill_instance_health`
	MetricInstanceHealthTransitions       = `seasonfill_instance_health_transitions_total`
	MetricInstanceLastCheckTimestamp      = `seasonfill_instance_last_check_timestamp`
	MetricRateLimitThrottled              = `seasonfill_rate_limit_throttled_total`
	MetricWebhookProcessingFailures       = `seasonfill_webhook_processing_failures_total`
	MetricWebhookReconcileTotal           = `seasonfill_webhook_reconcile_total`
	MetricWebhookReconcileDurationSeconds = `seasonfill_webhook_reconcile_duration_seconds`
)

// Webhook reconcile result values — emitted as the `result` label on
// MetricWebhookReconcileTotal. Single Go const block so the loop and
// tests share the spelling.
const (
	WebhookReconcileResultOK      = "ok"
	WebhookReconcileResultError   = "error"
	WebhookReconcileResultSkipped = "skipped"
)

func ScanCompleted(instance, status string) {
	metrics.GetOrCreateCounter(`seasonfill_scans_total{instance="` + instance + `",status="` + status + `"}`).Inc()
}

func SeriesEvaluated(instance, decision string) {
	metrics.GetOrCreateCounter(`seasonfill_series_evaluated_total{instance="` + instance + `",decision="` + decision + `"}`).Inc()
}

func GrabRecorded(instance, indexer, status string) {
	metrics.GetOrCreateCounter(`seasonfill_grabs_total{instance="` + instance + `",indexer="` + indexer + `",result="` + status + `"}`).Inc()
}

// GrabAttempt counts individual force-grab attempts with their classification:
// "grabbed", "retried", or "failed".
func GrabAttempt(instance, status string) {
	metrics.GetOrCreateCounter(`seasonfill_grab_attempts_total{instance="` + instance + `",status="` + status + `"}`).Inc()
}

func SonarrAPIRequest(instance, endpoint, status string) {
	metrics.GetOrCreateCounter(`seasonfill_sonarr_api_requests_total{instance="` + instance + `",endpoint="` + endpoint + `",status="` + status + `"}`).Inc()
}

func ObserveSonarrAPIDuration(instance, endpoint string, seconds float64) {
	metrics.GetOrCreateHistogram(`seasonfill_sonarr_api_duration_seconds{instance="` + instance + `",endpoint="` + endpoint + `"}`).Update(seconds)
}

func ObserveScanDuration(instance string, seconds float64) {
	metrics.GetOrCreateHistogram(`seasonfill_scan_duration_seconds{instance="` + instance + `"}`).Update(seconds)
}

func ObserveCandidatesFound(instance string, count int) {
	metrics.GetOrCreateHistogram(`seasonfill_candidates_found{instance="` + instance + `"}`).Update(float64(count))
}

func ObserveCoverageCount(instance string, count int) {
	metrics.GetOrCreateHistogram(`seasonfill_coverage_count{instance="` + instance + `"}`).Update(float64(count))
}

// SetInstanceAvailable is retained for back-compat with the legacy dashboard.
// New code should prefer SetInstanceHealth which carries the typed state.
func SetInstanceAvailable(instance string, available bool) {
	g := metrics.GetOrCreateGauge(`seasonfill_instances_available{instance="`+instance+`"}`, nil)
	if available {
		g.Set(1)
	} else {
		g.Set(0)
	}
}

func IncActiveScans(instance string) {
	metrics.GetOrCreateGauge(`seasonfill_active_scans{instance="`+instance+`"}`, nil).Inc()
}

func DecActiveScans(instance string) {
	metrics.GetOrCreateGauge(`seasonfill_active_scans{instance="`+instance+`"}`, nil).Dec()
}

// SetCooldownActive records the current count of active cooldowns per scope.
func SetCooldownActive(instance, scope string, count int) {
	metrics.GetOrCreateGauge(`seasonfill_cooldown_active{instance="`+instance+`",scope="`+scope+`"}`, nil).Set(float64(count))
}

// SetInstanceHealth records the numeric health code (0=Available, 1=Auth,
// 2=Network, 3=Unknown).
func SetInstanceHealth(instance string, code int) {
	metrics.GetOrCreateGauge(`seasonfill_instance_health{instance="`+instance+`"}`, nil).Set(float64(code))
}

// IncInstanceHealthTransition increments the per-transition counter.
func IncInstanceHealthTransition(instance, from, to string) {
	metrics.GetOrCreateCounter(`seasonfill_instance_health_transitions_total{instance="` + instance + `",from="` + from + `",to="` + to + `"}`).Inc()
}

// SetInstanceLastCheck records the Unix-second timestamp of the most recent
// check.
func SetInstanceLastCheck(instance string, unixSec int64) {
	metrics.GetOrCreateGauge(`seasonfill_instance_last_check_timestamp{instance="`+instance+`"}`, nil).Set(float64(unixSec))
}

// IncRateLimitThrottled records that a rate-limited call would have blocked.
// scope is "per_instance" or "global"; instance is the Sonarr instance name
// for the per-instance limiter, or "" for the shared global limiter.
func IncRateLimitThrottled(instance, scope string) {
	metrics.GetOrCreateCounter(`seasonfill_rate_limit_throttled_total{instance="` + instance + `",scope="` + scope + `"}`).Inc()
}

// IncWebhookProcessingFailures records a non-transient failure from
// the webhook UC. Transient (DB-unavailable) failures map to HTTP 500
// and are NOT counted — Sonarr retries and a successful retry should
// not pollute the failure rate. `errorKind` is produced by
// `application/webhook.ErrorKind` (low-cardinality).
func IncWebhookProcessingFailures(instance, errorKind string) {
	metrics.GetOrCreateCounter(`seasonfill_webhook_processing_failures_total{instance="` + instance + `",error_kind="` + errorKind + `"}`).Inc()
}

// IncWebhookReconcileResult bumps the per-instance reconcile counter.
// result must be one of the WebhookReconcileResult* constants above.
func IncWebhookReconcileResult(instance, result string) {
	metrics.GetOrCreateCounter(`seasonfill_webhook_reconcile_total{instance="` + instance + `",result="` + result + `"}`).Inc()
}

// ObserveWebhookReconcileDuration records the wall-clock duration of a
// single Reconcile attempt (including the 3 s per-instance timeout
// cap). Skips do NOT call this — only attempts that actually called
// Reconcile.
func ObserveWebhookReconcileDuration(instance string, seconds float64) {
	metrics.GetOrCreateHistogram(`seasonfill_webhook_reconcile_duration_seconds{instance="` + instance + `"}`).Update(seconds)
}

// IncParseRelease bumps the per-instance, per-result parse counter.
// result ∈ {"ok","error","skipped","disabled"}.
func IncParseRelease(instance, result string) {
	metrics.GetOrCreateCounter(`seasonfill_parse_release_total{instance="` + instance + `",result="` + result + `"}`).Inc()
}

// ObserveParseReleaseDuration records the wall-clock seconds spent in
// one parse pass (Sonarr round-trip + ExtractExtras).
func ObserveParseReleaseDuration(instance string, seconds float64) {
	metrics.GetOrCreateHistogram(`seasonfill_parse_release_duration_seconds{instance="` + instance + `"}`).Update(seconds)
}

func WritePrometheus(w io.Writer) {
	metrics.WritePrometheus(w, true)
}
