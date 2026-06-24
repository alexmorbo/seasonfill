package observability

import (
	"io"
	"time"

	"github.com/VictoriaMetrics/metrics"

	"github.com/alexmorbo/seasonfill/internal/shared/domain"
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

	// Story 346 — per-kind cold-start canon-images recovery telemetry.
	// `recovery_sweep_enqueued_total{kind}` ticks by the count of canon
	// rows the boot-time recovery sweep observed missing poster_asset
	// (`kind=poster`) or backdrop_asset (`kind=backdrop`) and re-enqueued
	// for re-enrichment. Reads "is the sweep actually moving rows" on
	// the operator dashboard. A flat counter across successive deploys
	// with the same backlog row count means the sweep is firing but the
	// enrichment write side never lands the path — i.e. the defensive
	// write-side guard is the only fix.
	MetricRecoverySweepEnqueuedTotal = `recovery_sweep_enqueued_total`

	// Story 351 — generic external-HTTP observability. Written by
	// infrastructure/httpx.MetricsTransport at the http.RoundTripper
	// layer. Labels:
	//   - client:   closed set {tmdb, omdb, tmdb_cdn, sonarr (reserved)}
	//   - endpoint: normalised path (see infrastructure/httpx tables)
	//   - method:   HTTP method (GET / POST / ...)
	//   - status:   CLOSED SET {200, 304, 401, 404, 429, 500, 502, 503, 504,
	//                            other, error} — bounded cardinality + per-code
	//                            alerts (429 rate-limit, 504 timeout, 401 auth)
	//
	// IMPORTANT: this family is per-HTTP-call. The legacy
	// tmdb_requests_total{result} (Story 306) is retry-semantic. Both
	// surface; both useful. Do NOT delete the legacy series.
	MetricExternalHTTPRequestsTotal    = `seasonfill_external_http_requests_total`
	MetricExternalHTTPRequestDuration  = `seasonfill_external_http_request_duration_seconds`
	MetricExternalHTTPRequestsInFlight = `seasonfill_external_http_requests_in_flight`

	// Story 503 — generic cache observability (PRD §6.7). The helper at
	// internal/shared/cachewatch registers and writes these directly via
	// VictoriaMetrics; the names are duplicated here only for grep-ability
	// and Grafana / alert authoring. Labels:
	//   - cache:  caller-supplied name (closed set, one per registered
	//             instance — see cachewatch.Names())
	//   - reason: closed set {capacity, ttl, manual} on the evictions
	//             counter family ONLY.
	MetricCacheEntries        = `cache_entries`
	MetricCacheBytesEstimated = `cache_bytes_estimated`
	MetricCacheHitsTotal      = `cache_hits_total`
	MetricCacheMissesTotal    = `cache_misses_total`
	MetricCacheEvictionsTotal = `cache_evictions_total`
	MetricCachePendingFetches = `cache_pending_fetches`
	MetricCacheDedupHitsTotal = `cache_dedup_hits_total`

	// Story 506 — discovery worker observability (PRD §5.1.1 lines
	// 696-702). Five families. Labels:
	//   - kind:     closed set {trending_day, trending_week, popular,
	//                by_genre, by_network, by_keyword}
	//   - language: per-list language tag (e.g. "en-US", "ru-RU")
	//   - outcome:  closed set {ok, error} on the refresh counter ONLY
	//
	// discovery_warming is a 0/1 GLOBAL gauge (no labels) — 1 from
	// worker construction until the first successful repo.ReplaceList
	// for ANY (kind, language). The handler (story 507) reads it to
	// emit the {"degraded":["discovery_warming"]} JSON wrapper.
	MetricDiscoveryRefreshTotal           = `seasonfill_discovery_refresh_total`
	MetricDiscoveryRefreshDurationSeconds = `seasonfill_discovery_refresh_duration_seconds`
	MetricDiscoveryListAgeSeconds         = `seasonfill_discovery_list_age_seconds`
	MetricDiscoveryListSize               = `seasonfill_discovery_list_size`
	MetricDiscoveryWarming                = `seasonfill_discovery_warming`
	// Story 509 (N-2h). Counts /discovery/discover handler outcomes per
	// branch of Pattern B: hit (LRU), miss_sync (sync fetch ok),
	// miss_warming (sync timeout → 202 + bg enqueue), error (TMDB 5xx).
	MetricDiscoverHandlerOutcome = `seasonfill_discover_handler_outcome_total`
)

// Webhook reconcile result values — emitted as the `result` label on
// MetricWebhookReconcileTotal. Single Go const block so the loop and
// tests share the spelling.
const (
	WebhookReconcileResultOK      = "ok"
	WebhookReconcileResultError   = "error"
	WebhookReconcileResultSkipped = "skipped"
)

func ScanCompleted(instance domain.InstanceName, status string) {
	metrics.GetOrCreateCounter(`seasonfill_scans_total{instance="` + string(instance) + `",status="` + status + `"}`).Inc()
}

func SeriesEvaluated(instance domain.InstanceName, decision string) {
	metrics.GetOrCreateCounter(`seasonfill_series_evaluated_total{instance="` + string(instance) + `",decision="` + decision + `"}`).Inc()
}

func GrabRecorded(instance domain.InstanceName, indexer, status string) {
	metrics.GetOrCreateCounter(`seasonfill_grabs_total{instance="` + string(instance) + `",indexer="` + indexer + `",result="` + status + `"}`).Inc()
}

// GrabAttempt counts individual force-grab attempts with their classification:
// "grabbed", "retried", or "failed".
func GrabAttempt(instance domain.InstanceName, status string) {
	metrics.GetOrCreateCounter(`seasonfill_grab_attempts_total{instance="` + string(instance) + `",status="` + status + `"}`).Inc()
}

func SonarrAPIRequest(instance domain.InstanceName, endpoint, status string) {
	metrics.GetOrCreateCounter(`seasonfill_sonarr_api_requests_total{instance="` + string(instance) + `",endpoint="` + endpoint + `",status="` + status + `"}`).Inc()
}

func ObserveSonarrAPIDuration(instance domain.InstanceName, endpoint string, seconds float64) {
	metrics.GetOrCreateHistogram(`seasonfill_sonarr_api_duration_seconds{instance="` + string(instance) + `",endpoint="` + endpoint + `"}`).Update(seconds)
}

func ObserveScanDuration(instance domain.InstanceName, seconds float64) {
	metrics.GetOrCreateHistogram(`seasonfill_scan_duration_seconds{instance="` + string(instance) + `"}`).Update(seconds)
}

func ObserveCandidatesFound(instance domain.InstanceName, count int) {
	metrics.GetOrCreateHistogram(`seasonfill_candidates_found{instance="` + string(instance) + `"}`).Update(float64(count))
}

func ObserveCoverageCount(instance domain.InstanceName, count int) {
	metrics.GetOrCreateHistogram(`seasonfill_coverage_count{instance="` + string(instance) + `"}`).Update(float64(count))
}

// SetInstanceAvailable is retained for back-compat with the legacy dashboard.
// New code should prefer SetInstanceHealth which carries the typed state.
func SetInstanceAvailable(instance domain.InstanceName, available bool) {
	g := metrics.GetOrCreateGauge(`seasonfill_instances_available{instance="`+string(instance)+`"}`, nil)
	if available {
		g.Set(1)
	} else {
		g.Set(0)
	}
}

func IncActiveScans(instance domain.InstanceName) {
	metrics.GetOrCreateGauge(`seasonfill_active_scans{instance="`+string(instance)+`"}`, nil).Inc()
}

func DecActiveScans(instance domain.InstanceName) {
	metrics.GetOrCreateGauge(`seasonfill_active_scans{instance="`+string(instance)+`"}`, nil).Dec()
}

// SetCooldownActive records the current count of active cooldowns per scope.
func SetCooldownActive(instance domain.InstanceName, scope string, count int) {
	metrics.GetOrCreateGauge(`seasonfill_cooldown_active{instance="`+string(instance)+`",scope="`+scope+`"}`, nil).Set(float64(count))
}

// SetInstanceHealth records the numeric health code (0=Available, 1=Auth,
// 2=Network, 3=Unknown).
func SetInstanceHealth(instance domain.InstanceName, code int) {
	metrics.GetOrCreateGauge(`seasonfill_instance_health{instance="`+string(instance)+`"}`, nil).Set(float64(code))
}

// IncInstanceHealthTransition increments the per-transition counter.
func IncInstanceHealthTransition(instance domain.InstanceName, from, to string) {
	metrics.GetOrCreateCounter(`seasonfill_instance_health_transitions_total{instance="` + string(instance) + `",from="` + from + `",to="` + to + `"}`).Inc()
}

// SetInstanceLastCheck records the Unix-second timestamp of the most recent
// check.
func SetInstanceLastCheck(instance domain.InstanceName, unixSec int64) {
	metrics.GetOrCreateGauge(`seasonfill_instance_last_check_timestamp{instance="`+string(instance)+`"}`, nil).Set(float64(unixSec))
}

// IncRateLimitThrottled records that a rate-limited call would have blocked.
// scope is "per_instance" or "global"; instance is the Sonarr instance name
// for the per-instance limiter, or "" for the shared global limiter.
func IncRateLimitThrottled(instance domain.InstanceName, scope string) {
	metrics.GetOrCreateCounter(`seasonfill_rate_limit_throttled_total{instance="` + string(instance) + `",scope="` + scope + `"}`).Inc()
}

// IncWebhookProcessingFailures records a non-transient failure from
// the webhook UC. Transient (DB-unavailable) failures map to HTTP 500
// and are NOT counted — Sonarr retries and a successful retry should
// not pollute the failure rate. `errorKind` is produced by
// `application/webhook.ErrorKind` (low-cardinality).
func IncWebhookProcessingFailures(instance domain.InstanceName, errorKind string) {
	metrics.GetOrCreateCounter(`seasonfill_webhook_processing_failures_total{instance="` + string(instance) + `",error_kind="` + errorKind + `"}`).Inc()
}

// IncWebhookReconcileResult bumps the per-instance reconcile counter.
// result must be one of the WebhookReconcileResult* constants above.
func IncWebhookReconcileResult(instance domain.InstanceName, result string) {
	metrics.GetOrCreateCounter(`seasonfill_webhook_reconcile_total{instance="` + string(instance) + `",result="` + result + `"}`).Inc()
}

// ObserveWebhookReconcileDuration records the wall-clock duration of a
// single Reconcile attempt (including the 3 s per-instance timeout
// cap). Skips do NOT call this — only attempts that actually called
// Reconcile.
func ObserveWebhookReconcileDuration(instance domain.InstanceName, seconds float64) {
	metrics.GetOrCreateHistogram(`seasonfill_webhook_reconcile_duration_seconds{instance="` + string(instance) + `"}`).Update(seconds)
}

// IncParseRelease bumps the per-instance, per-result parse counter.
// result ∈ {"ok","error","skipped","disabled"}.
func IncParseRelease(instance domain.InstanceName, result string) {
	metrics.GetOrCreateCounter(`seasonfill_parse_release_total{instance="` + string(instance) + `",result="` + result + `"}`).Inc()
}

// ObserveParseReleaseDuration records the wall-clock seconds spent in
// one parse pass (Sonarr round-trip + ExtractExtras).
func ObserveParseReleaseDuration(instance domain.InstanceName, seconds float64) {
	metrics.GetOrCreateHistogram(`seasonfill_parse_release_duration_seconds{instance="` + string(instance) + `"}`).Update(seconds)
}

// IncScanSkipped bumps the 046b pre-filter counter. `reason` must be
// one of {"all_complete", "sonarr_handles"} — the call site (scan_usecase)
// is the only producer and uses the same string literals to populate
// the synthetic Decision row's category.
func IncScanSkipped(instance domain.InstanceName, reason string) {
	metrics.GetOrCreateCounter(`seasonfill_scan_skipped_seasons_total{instance="` + string(instance) + `",reason="` + reason + `"}`).Inc()
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

// AddRecoverySweepEnqueued bumps the per-kind recovery-sweep enqueue
// counter (Story 346) by n. kind ∈ {"poster", "backdrop"} — the boot
// one-shot recovery sweep calls this with the count of canon rows it
// observed missing the named column. n <= 0 is a no-op so callers can
// pass an unconditional count without a guard.
func AddRecoverySweepEnqueued(kind string, n int) {
	if n <= 0 {
		return
	}
	metrics.GetOrCreateCounter(`recovery_sweep_enqueued_total{kind="` + kind + `"}`).Add(n)
}

// IncOnDemandEnrich bumps the per-result on-demand enrichment counter
// (Story 528). result ∈ {"enqueued","throttled","skipped_full",
// "skipped_invalid_id","skipped_no_dispatcher","skipped_closed","panic"}
// — closed label set; callers passing other values pollute cardinality.
func IncOnDemandEnrich(result string) {
	metrics.GetOrCreateCounter(
		`seasonfill_seriesdetail_ondemand_enrich_total{result="` + result + `"}`,
	).Inc()
}

// IncSeriesdetailFreshen bumps the per-result counter for the Story 533
// read-through TMDB sync path. result ∈ {"fresh","refreshed","timeout",
// "error","async_only","skipped"} — closed label set; callers passing
// other values pollute cardinality.
func IncSeriesdetailFreshen(result string) {
	metrics.GetOrCreateCounter(
		`seasonfill_seriesdetail_freshen_total{result="` + result + `"}`,
	).Inc()
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

// IncDiscoveryRefresh ticks the per-(kind,language,outcome) refresh
// counter. outcome ∈ {"ok","error"} per the closed label set; any
// caller passing other values pollutes the cardinality budget — the
// worker is the only writer and only emits ok|error.
func IncDiscoveryRefresh(kind, language, outcome string) {
	metrics.GetOrCreateCounter(
		`seasonfill_discovery_refresh_total{kind="` + kind +
			`",language="` + language +
			`",outcome="` + outcome + `"}`).Inc()
}

// ObserveDiscoveryRefreshDuration records the per-refresh wall-clock
// (TMDB fetch + stub-upsert fan-out + ReplaceList). Seconds, histogram.
func ObserveDiscoveryRefreshDuration(kind, language string, d time.Duration) {
	metrics.GetOrCreateHistogram(
		`seasonfill_discovery_refresh_duration_seconds{kind="` + kind +
			`",language="` + language + `"}`).Update(d.Seconds())
}

// SetDiscoveryListAge publishes seconds-since-last-refresh per
// (kind, language). Worker writes this right after a successful
// ReplaceList (where age=0) so dashboards see a sawtooth.
func SetDiscoveryListAge(kind, language string, ageSeconds float64) {
	metrics.GetOrCreateGauge(
		`seasonfill_discovery_list_age_seconds{kind="`+kind+
			`",language="`+language+`"}`, nil).Set(ageSeconds)
}

// SetDiscoveryListSize publishes the row count after a ReplaceList.
// 0 means "list cleared" — alert if size==0 for >1 ScheduleFor cycle.
func SetDiscoveryListSize(kind, language string, size int) {
	metrics.GetOrCreateGauge(
		`seasonfill_discovery_list_size{kind="`+kind+
			`",language="`+language+`"}`, nil).Set(float64(size))
}

// SetDiscoveryWarming flips the 0/1 global gauge. Worker sets it to
// 1 at construction and to 0 the first time any ReplaceList succeeds.
// Subsequent flips back to 1 are NOT supported by spec — the gauge
// is monotonic 1→0 within a process lifetime.
func SetDiscoveryWarming(warming bool) {
	g := metrics.GetOrCreateGauge(`seasonfill_discovery_warming`, nil)
	if warming {
		g.Set(1)
		return
	}
	g.Set(0)
}

// IncDiscoverHandlerOutcome ticks the per-outcome counter. outcome ∈
// {"hit","miss_sync","miss_warming","error"} per the closed label set;
// the rest handler is the only writer.
func IncDiscoverHandlerOutcome(outcome string) {
	metrics.GetOrCreateCounter(
		`seasonfill_discover_handler_outcome_total{outcome="` + outcome + `"}`).Inc()
}

// ObserveDiscoveryRefreshPaceWait records wall-clock spent waiting on the
// worker's pace-limiter (B-39). Histogram in seconds. Steady-state near
// zero; cold-start spikes confirm the limiter is smoothing the stub-upsert
// burst against the enrichment prewarm queue.
func ObserveDiscoveryRefreshPaceWait(kind, language string, d time.Duration) {
	metrics.GetOrCreateHistogram(
		`seasonfill_discovery_refresh_pace_wait_seconds{kind="` + kind +
			`",language="` + language + `"}`).Update(d.Seconds())
}
