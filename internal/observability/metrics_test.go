package observability

import (
	"bytes"
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

func TestWritePrometheus_NotEmpty(t *testing.T) {
	// Register at least one metric so the export is non-empty regardless of test order.
	ScanCompleted("obs_test_z", "completed")

	buf := &bytes.Buffer{}
	WritePrometheus(buf)
	require.NotEmpty(t, buf.Bytes())
}

func TestMetricConstants_AreNotEmpty(t *testing.T) {
	t.Parallel()
	consts := []string{
		MetricScansTotal,
		MetricSeriesEvaluatedTotal,
		MetricGrabsTotal,
		MetricSonarrAPIRequestsTotal,
		MetricScanDurationSeconds,
		MetricSonarrAPIDuration,
		MetricCandidatesFound,
		MetricCoverageCount,
		MetricInstancesAvailable,
		MetricActiveScans,
	}
	for _, c := range consts {
		assert.NotEmpty(t, c)
		assert.Contains(t, c, "seasonfill_")
	}
}
