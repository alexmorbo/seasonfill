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
	// 046b — scan pre-filter counter. Emitted once per skipped season
	// inside scan_usecase.processScan when the pre-filter short-circuits
	// the per-season SearchReleases / ListEpisodes round-trips. Labels:
	// `instance` (per-instance), `reason` ∈ {all_complete, sonarr_handles}.
	// reason matches the CATEGORY name (not the typed Reason string) so
	// Grafana queries align with PRD §3 B4 wording.
	MetricScanSkippedSeasonsTotal = `seasonfill_scan_skipped_seasons_total`

	// Story 306 — TMDB observability for the cold-start backfill.
	// `tmdb_requests_total` is incremented exactly once per call to
	// (*tmdb.Client).do() with `result` set from the dispatch verdict:
	//   - success: 2xx
	//   - rate_limited: 429 (counted on the FIRST 429; retries that
	//     also 429 increment again — see story 306 §B notes)
	//   - error: every other terminal path (5xx exhausted, network,
	//     terminal 4xx, JSON parse)
	// `tmdb_limiter_wait_seconds` records wall-clock seconds spent
	// inside tokenBucket.Wait() per call — graphs "how saturated is
	// the bucket". Histogram, no labels (single global bucket).
	MetricTMDBRequestsTotal      = `tmdb_requests_total`
	MetricTMDBLimiterWaitSeconds = `tmdb_limiter_wait_seconds`

	// Story 306 — enrichment dispatcher observability.
	// `enrichment_queue_depth{worker}` is gauge of pending + in-flight
	// jobs per EntityKind. The dispatcher reads `worker` from the
	// EntityKind string ("series" / "person" / "omdb").
	// `enrichment_cold_start_remaining` is a single global gauge
	// initialised to the count of unjournalled series at BackfillSeries
	// entry and decremented to zero as each series job completes.
	MetricEnrichmentQueueDepth         = `enrichment_queue_depth`
	MetricEnrichmentColdStartRemaining = `enrichment_cold_start_remaining`

	// Story 318 — periodic cold-start re-sweep observability.
	// `enrichment_cold_start_resweeps_total` ticks once per BackfillSeries
	// invocation (including empty sweeps — operator wants to see the
	// goroutine is alive).
	// `enrichment_cold_start_resweep_enqueued_total` increments by
	// len(ids) per sweep (counts attempted enqueues; some may be
	// dispatcher-dedup-skipped or channel-full-dropped — see
	// `enrichment_queue_drops_total`).
	// `enrichment_queue_drops_total{worker}` increments every time
	// priorityQueue.enqueue() takes the channel-full default branch.
	MetricEnrichmentColdStartResweepsTotal        = `enrichment_cold_start_resweeps_total`
	MetricEnrichmentColdStartResweepEnqueuedTotal = `enrichment_cold_start_resweep_enqueued_total`
	MetricEnrichmentQueueDropsTotal               = `enrichment_queue_drops_total`

	// Story 313 — adaptive TMDB rate-limit pause observability.
	// `tmdb_rate_limit_pauses_total` is a counter ticked once per
	// 429-triggered global pause entry. Compounding 429s during an
	// existing pause do NOT bump the counter — only the FIRST entry
	// to the paused state ticks (so "10 retries during a single
	// 30s pause" reads as 1, not 10).
	// `tmdb_rate_limit_pause_seconds_total` is a counter of cumulative
	// seconds the bucket was paused. Reads "how many seconds of
	// enrichment throughput did we lose to TMDB pushback today".
	// `tmdb_rate_limit_in_pause` is a 0/1 gauge — 1 while a pause is
	// active, 0 otherwise. Alert: in_pause==1 for >2× expected RetryAfter.
	MetricTMDBRateLimitPausesTotal       = `tmdb_rate_limit_pauses_total`
	MetricTMDBRateLimitPauseSecondsTotal = `tmdb_rate_limit_pause_seconds_total`
	MetricTMDBRateLimitInPause           = `tmdb_rate_limit_in_pause`
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

// IncScanSkipped bumps the 046b pre-filter counter. `reason` must be
// one of {"all_complete", "sonarr_handles"} — the call site (scan_usecase)
// is the only producer and uses the same string literals to populate
// the synthetic Decision row's category.
func IncScanSkipped(instance, reason string) {
	metrics.GetOrCreateCounter(`seasonfill_scan_skipped_seasons_total{instance="` + instance + `",reason="` + reason + `"}`).Inc()
}

func WritePrometheus(w io.Writer) {
	metrics.WritePrometheus(w, true)
}

// IncTMDBRequest bumps the per-result TMDB request counter. result
// ∈ {"success","rate_limited","error"} — kept as a closed set so
// Grafana queries are stable.
func IncTMDBRequest(result string) {
	metrics.GetOrCreateCounter(`tmdb_requests_total{result="` + result + `"}`).Inc()
}

// ObserveTMDBLimiterWait records wall-clock seconds a TMDB call
// spent waiting on the shared 4.5-rps token bucket. Zero-wait
// calls (the bucket had a pre-filled token) are recorded as 0.
func ObserveTMDBLimiterWait(seconds float64) {
	metrics.GetOrCreateHistogram(`tmdb_limiter_wait_seconds`).Update(seconds)
}

// SetEnrichmentQueueDepth publishes the per-kind queue depth.
// worker is the EntityKind string ("series" / "person" / "omdb").
// Called from inside priorityQueue under its mutex — single writer
// per (worker) label, no atomic needed at the call site.
func SetEnrichmentQueueDepth(worker string, depth int) {
	metrics.GetOrCreateGauge(`enrichment_queue_depth{worker="`+worker+`"}`, nil).Set(float64(depth))
}

// SetEnrichmentColdStartRemaining publishes the count of unjournalled
// series still pending the cold-start backfill. Initialised to
// len(ids) at BackfillSeries entry; decremented to zero as each
// series job completes.
func SetEnrichmentColdStartRemaining(n int) {
	metrics.GetOrCreateGauge(`enrichment_cold_start_remaining`, nil).Set(float64(n))
}

// IncEnrichmentColdStartResweep ticks the per-sweep counter (Story 318).
// Fires on EVERY BackfillSeries invocation, including empty sweeps —
// the operator wants to verify the re-sweep goroutine is alive even
// when there is nothing to enqueue.
func IncEnrichmentColdStartResweep() {
	metrics.GetOrCreateCounter(`enrichment_cold_start_resweeps_total`).Inc()
}

// AddEnrichmentColdStartResweepEnqueued bumps the per-sweep enqueue
// counter (Story 318) by n. n is len(ids) BEFORE deduplication /
// channel-full drops — to see drops, subtract a delta of
// `enrichment_queue_drops_total{worker="series"}` over the same window.
func AddEnrichmentColdStartResweepEnqueued(n int) {
	if n <= 0 {
		return
	}
	metrics.GetOrCreateCounter(`enrichment_cold_start_resweep_enqueued_total`).Add(n)
}

// IncEnrichmentQueueDrop ticks the per-worker channel-full drop
// counter (Story 318). Called from priorityQueue.enqueue when the
// non-blocking send takes the default branch.
func IncEnrichmentQueueDrop(worker string) {
	metrics.GetOrCreateCounter(`enrichment_queue_drops_total{worker="` + worker + `"}`).Inc()
}

// IncTMDBRateLimitPause bumps the pause-entry counter (Story 313).
// Called exactly once per fresh entry to the paused state — repeated
// 429s during an existing pause MUST NOT tick this (the caller's
// "is_already_paused" guard owns that). Reads "how many distinct
// rate-limit windows did we hit today" on the operator dashboard.
func IncTMDBRateLimitPause() {
	metrics.GetOrCreateCounter(`tmdb_rate_limit_pauses_total`).Inc()
}

// AddTMDBRateLimitPauseSeconds records the wall-clock seconds spent
// in the just-ended pause (Story 313). Cumulative — Grafana plots
// rate() over a window to see "seconds-lost per minute".
func AddTMDBRateLimitPauseSeconds(seconds float64) {
	if seconds <= 0 {
		return
	}
	// VictoriaMetrics counters accept floats via FloatCounter, but the
	// global GetOrCreateCounter returns an integer counter — bucket
	// seconds at millisecond granularity via FloatCounter for sub-second
	// pauses. The metric name stays scalar so the Grafana query is
	// unchanged.
	metrics.GetOrCreateFloatCounter(`tmdb_rate_limit_pause_seconds_total`).Add(seconds)
}

// SetTMDBRateLimitInPause flips the 0/1 in-pause gauge (Story 313).
// Pause entry → SetTMDBRateLimitInPause(true); resume → false.
// A nil-resume (e.g. process death mid-pause) leaves the gauge at 1
// — which is the correct on-restart picture (pod went down WHILE
// paused). The first call after restart from the pause path will
// re-publish the truth.
func SetTMDBRateLimitInPause(paused bool) {
	g := metrics.GetOrCreateGauge(`tmdb_rate_limit_in_pause`, nil)
	if paused {
		g.Set(1)
		return
	}
	g.Set(0)
}
