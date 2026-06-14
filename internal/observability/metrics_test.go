package observability

import (
	"bytes"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// VictoriaMetrics maintains a package-global default set, so these tests must NOT
// run in parallel — each call mutates shared state. They are smoke tests: we
// register metrics, then assert the Prometheus text export contains their names
// and a value greater than zero where applicable.

func writeAndRead(t *testing.T) string {
	t.Helper()
	buf := &bytes.Buffer{}
	WritePrometheus(buf)
	return buf.String()
}

func TestScanCompleted_RegistersAndIncrements(t *testing.T) {
	ScanCompleted("obs_test_a", "completed")
	ScanCompleted("obs_test_a", "completed")
	body := writeAndRead(t)
	assert.Contains(t, body, `seasonfill_scans_total{instance="obs_test_a",status="completed"}`)
}

func TestSeriesEvaluated_RegistersAndIncrements(t *testing.T) {
	SeriesEvaluated("obs_test_b", "grab")
	body := writeAndRead(t)
	assert.Contains(t, body, `seasonfill_series_evaluated_total{instance="obs_test_b",decision="grab"}`)
}

func TestGrabRecorded_RegistersAndIncrements(t *testing.T) {
	GrabRecorded("obs_test_c", "RT", "success")
	body := writeAndRead(t)
	assert.Contains(t, body, "seasonfill_grabs_total")
	assert.Contains(t, body, `instance="obs_test_c"`)
	assert.Contains(t, body, `indexer="RT"`)
}

func TestGrabAttempt_RegistersAndIncrements(t *testing.T) {
	GrabAttempt("obs_test_attempt", "grabbed")
	GrabAttempt("obs_test_attempt", "retried")
	GrabAttempt("obs_test_attempt", "failed")
	body := writeAndRead(t)
	assert.Contains(t, body, "seasonfill_grab_attempts_total")
	assert.Contains(t, body, `status="grabbed"`)
	assert.Contains(t, body, `status="retried"`)
	assert.Contains(t, body, `status="failed"`)
}

func TestSonarrAPIRequest_RegistersAndIncrements(t *testing.T) {
	SonarrAPIRequest("obs_test_d", "/series", "200")
	body := writeAndRead(t)
	assert.Contains(t, body, "seasonfill_sonarr_api_requests_total")
}

func TestObserveSonarrAPIDuration(t *testing.T) {
	ObserveSonarrAPIDuration("obs_test_e", "/series", 0.250)
	body := writeAndRead(t)
	assert.Contains(t, body, "seasonfill_sonarr_api_duration_seconds")
}

func TestObserveScanDuration(t *testing.T) {
	ObserveScanDuration("obs_test_f", 12.5)
	body := writeAndRead(t)
	assert.Contains(t, body, "seasonfill_scan_duration_seconds")
}

func TestObserveCandidatesFound(t *testing.T) {
	ObserveCandidatesFound("obs_test_g", 7)
	body := writeAndRead(t)
	assert.Contains(t, body, "seasonfill_candidates_found")
}

func TestObserveCoverageCount(t *testing.T) {
	ObserveCoverageCount("obs_test_h", 3)
	body := writeAndRead(t)
	assert.Contains(t, body, "seasonfill_coverage_count")
}

func TestSetInstanceAvailable(t *testing.T) {
	SetInstanceAvailable("obs_test_i", true)
	assert.Contains(t, writeAndRead(t), `seasonfill_instances_available{instance="obs_test_i"}`)

	SetInstanceAvailable("obs_test_i", false)
	// Still present, value is now 0.
	assert.Contains(t, writeAndRead(t), `seasonfill_instances_available{instance="obs_test_i"}`)
}

func TestIncDecActiveScans(t *testing.T) {
	IncActiveScans("obs_test_j")
	IncActiveScans("obs_test_j")
	DecActiveScans("obs_test_j")

	body := writeAndRead(t)
	assert.Contains(t, body, `seasonfill_active_scans{instance="obs_test_j"}`)
}

func TestSetCooldownActive(t *testing.T) {
	SetCooldownActive("obs_test_cooldown", "series", 4)
	SetCooldownActive("obs_test_cooldown", "guid", 11)
	body := writeAndRead(t)
	assert.Contains(t, body, "seasonfill_cooldown_active")
	assert.Contains(t, body, `scope="series"`)
	assert.Contains(t, body, `scope="guid"`)
}

func TestWritePrometheus_NotEmpty(t *testing.T) {
	ScanCompleted("obs_test_z", "completed")

	buf := &bytes.Buffer{}
	WritePrometheus(buf)
	require.NotEmpty(t, buf.Bytes())
}

func TestSetInstanceHealth(t *testing.T) {
	SetInstanceHealth("obs_test_health", 0)
	SetInstanceHealth("obs_test_health", 1)
	body := writeAndRead(t)
	assert.Contains(t, body, `seasonfill_instance_health{instance="obs_test_health"}`)
}

func TestIncInstanceHealthTransition(t *testing.T) {
	IncInstanceHealthTransition("obs_test_t", "Available", "UnavailableAuth")
	body := writeAndRead(t)
	assert.Contains(t, body, "seasonfill_instance_health_transitions_total")
	assert.Contains(t, body, `instance="obs_test_t"`)
}

func TestSetInstanceLastCheck(t *testing.T) {
	SetInstanceLastCheck("obs_test_lc", 1716210000)
	body := writeAndRead(t)
	assert.Contains(t, body, `seasonfill_instance_last_check_timestamp{instance="obs_test_lc"}`)
}

func TestIncRateLimitThrottled_RegistersAndIncrements(t *testing.T) {
	IncRateLimitThrottled("obs_test_rl_a", "per_instance")
	IncRateLimitThrottled("obs_test_rl_a", "per_instance")
	IncRateLimitThrottled("", "global")
	body := writeAndRead(t)
	assert.Contains(t, body, "seasonfill_rate_limit_throttled_total")
	assert.Contains(t, body, `instance="obs_test_rl_a"`)
	assert.Contains(t, body, `scope="per_instance"`)
	assert.Contains(t, body, `instance=""`)
	assert.Contains(t, body, `scope="global"`)
}

func TestIncWebhookProcessingFailures_RegistersAndIncrements(t *testing.T) {
	IncWebhookProcessingFailures("obs_test_wh_a", "other")
	IncWebhookProcessingFailures("obs_test_wh_a", "other")
	IncWebhookProcessingFailures("obs_test_wh_b", "not_found")
	body := writeAndRead(t)
	assert.Contains(t, body, "seasonfill_webhook_processing_failures_total")
	assert.Contains(t, body, `instance="obs_test_wh_a"`)
	assert.Contains(t, body, `error_kind="other"`)
	assert.Contains(t, body, `instance="obs_test_wh_b"`)
	assert.Contains(t, body, `error_kind="not_found"`)
}

func TestIncParseRelease_RegistersAndIncrements(t *testing.T) {
	IncParseRelease("obs_test_parse_a", "ok")
	IncParseRelease("obs_test_parse_a", "ok")
	IncParseRelease("obs_test_parse_a", "error")
	IncParseRelease("obs_test_parse_b", "disabled")
	body := writeAndRead(t)
	assert.Contains(t, body, "seasonfill_parse_release_total")
	assert.Contains(t, body, `instance="obs_test_parse_a"`)
	assert.Contains(t, body, `result="ok"`)
	assert.Contains(t, body, `result="error"`)
	assert.Contains(t, body, `result="disabled"`)
	assert.Contains(t, body, `instance="obs_test_parse_b"`)
}

func TestObserveParseReleaseDuration_RegistersHistogram(t *testing.T) {
	ObserveParseReleaseDuration("obs_test_parse_dur", 0.123)
	ObserveParseReleaseDuration("obs_test_parse_dur", 0.456)
	body := writeAndRead(t)
	assert.Contains(t, body, "seasonfill_parse_release_duration_seconds")
	assert.Contains(t, body, `instance="obs_test_parse_dur"`)
}

func TestIncScanSkipped_RegistersAndIncrements(t *testing.T) {
	IncScanSkipped("obs_test_skip_a", "all_complete")
	IncScanSkipped("obs_test_skip_a", "all_complete")
	IncScanSkipped("obs_test_skip_a", "sonarr_handles")
	IncScanSkipped("obs_test_skip_b", "all_complete")
	body := writeAndRead(t)
	assert.Contains(t, body, "seasonfill_scan_skipped_seasons_total")
	assert.Contains(t, body, `instance="obs_test_skip_a"`)
	assert.Contains(t, body, `reason="all_complete"`)
	assert.Contains(t, body, `reason="sonarr_handles"`)
	assert.Contains(t, body, `instance="obs_test_skip_b"`)
}

func TestIncTMDBRequest_RegistersAndIncrements(t *testing.T) {
	IncTMDBRequest("success")
	IncTMDBRequest("rate_limited")
	IncTMDBRequest("error")
	body := writeAndRead(t)
	assert.Contains(t, body, `tmdb_requests_total{result="success"}`)
	assert.Contains(t, body, `tmdb_requests_total{result="rate_limited"}`)
	assert.Contains(t, body, `tmdb_requests_total{result="error"}`)
}

func TestObserveTMDBLimiterWait_Registers(t *testing.T) {
	ObserveTMDBLimiterWait(0)
	ObserveTMDBLimiterWait(0.222)
	body := writeAndRead(t)
	assert.Contains(t, body, "tmdb_limiter_wait_seconds")
}

func TestSetEnrichmentQueueDepth_Registers(t *testing.T) {
	SetEnrichmentQueueDepth("series", 3)
	SetEnrichmentQueueDepth("person", 0)
	body := writeAndRead(t)
	assert.Contains(t, body, `enrichment_queue_depth{worker="series"}`)
	assert.Contains(t, body, `enrichment_queue_depth{worker="person"}`)
}

func TestSetEnrichmentColdStartRemaining_Registers(t *testing.T) {
	SetEnrichmentColdStartRemaining(42)
	body := writeAndRead(t)
	assert.Contains(t, body, "enrichment_cold_start_remaining")
}

func TestMetricConstants_AreNotEmpty(t *testing.T) {
	t.Parallel()
	consts := []string{
		MetricScansTotal,
		MetricSeriesEvaluatedTotal,
		MetricGrabsTotal,
		MetricGrabAttemptsTotal,
		MetricSonarrAPIRequestsTotal,
		MetricScanDurationSeconds,
		MetricSonarrAPIDuration,
		MetricCandidatesFound,
		MetricCoverageCount,
		MetricInstancesAvailable,
		MetricActiveScans,
		MetricCooldownActive,
		MetricInstanceHealth,
		MetricInstanceHealthTransitions,
		MetricInstanceLastCheckTimestamp,
		MetricRateLimitThrottled,
		MetricWebhookProcessingFailures,
	}
	for _, c := range consts {
		assert.NotEmpty(t, c)
		assert.Contains(t, c, "seasonfill_")
	}
}

// 313 — smoke for the 3 new TMDB rate-limit pause helpers.
func TestIncTMDBRateLimitPause_Registers(t *testing.T) {
	IncTMDBRateLimitPause()
	IncTMDBRateLimitPause()
	body := writeAndRead(t)
	assert.Contains(t, body, "tmdb_rate_limit_pauses_total")
}

func TestAddTMDBRateLimitPauseSeconds_Registers(t *testing.T) {
	AddTMDBRateLimitPauseSeconds(0.5)
	AddTMDBRateLimitPauseSeconds(1.25)
	body := writeAndRead(t)
	assert.Contains(t, body, "tmdb_rate_limit_pause_seconds_total")
}

func TestAddTMDBRateLimitPauseSeconds_IgnoresNonPositive(t *testing.T) {
	// Defensive: zero or negative seconds must NOT bump the counter.
	// Pulls the existing value, calls with 0, asserts no change.
	before := writeAndRead(t)
	AddTMDBRateLimitPauseSeconds(0)
	AddTMDBRateLimitPauseSeconds(-1)
	after := writeAndRead(t)
	// Both should contain the same value for the float counter line.
	// We compare exact-line equality on the seconds-total metric line.
	beforeLine := findMetricLine(before, "tmdb_rate_limit_pause_seconds_total")
	afterLine := findMetricLine(after, "tmdb_rate_limit_pause_seconds_total")
	assert.Equal(t, beforeLine, afterLine)
}

func TestSetTMDBRateLimitInPause_Registers(t *testing.T) {
	SetTMDBRateLimitInPause(true)
	body := writeAndRead(t)
	assert.Contains(t, body, "tmdb_rate_limit_in_pause 1")
	SetTMDBRateLimitInPause(false)
	body = writeAndRead(t)
	assert.Contains(t, body, "tmdb_rate_limit_in_pause 0")
}

// findMetricLine returns the metric line (single line) for a given
// counter/gauge name, or "" if not present.
func findMetricLine(body, name string) string {
	for _, line := range strings.Split(body, "\n") {
		if strings.HasPrefix(line, name+" ") {
			return line
		}
	}
	return ""
}
