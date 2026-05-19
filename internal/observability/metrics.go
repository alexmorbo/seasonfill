package observability

import (
	"io"

	"github.com/VictoriaMetrics/metrics"
)

const (
	MetricScansTotal             = `seasonfill_scans_total`
	MetricSeriesEvaluatedTotal   = `seasonfill_series_evaluated_total`
	MetricGrabsTotal             = `seasonfill_grabs_total`
	MetricSonarrAPIRequestsTotal = `seasonfill_sonarr_api_requests_total`
	MetricScanDurationSeconds    = `seasonfill_scan_duration_seconds`
	MetricSonarrAPIDuration      = `seasonfill_sonarr_api_duration_seconds`
	MetricCandidatesFound        = `seasonfill_candidates_found`
	MetricCoverageCount          = `seasonfill_coverage_count`
	MetricInstancesAvailable     = `seasonfill_instances_available`
	MetricActiveScans            = `seasonfill_active_scans`
)

func ScanCompleted(instance, status string) {
	metrics.GetOrCreateCounter(`seasonfill_scans_total{instance="` + instance + `",status="` + status + `"}`).Inc()
}

func SeriesEvaluated(instance, decision string) {
	metrics.GetOrCreateCounter(`seasonfill_series_evaluated_total{instance="` + instance + `",decision="` + decision + `"}`).Inc()
}

func GrabRecorded(instance, indexer, status string) {
	metrics.GetOrCreateCounter(`seasonfill_grabs_total{instance="` + instance + `",indexer="` + indexer + `",status="` + status + `"}`).Inc()
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

func WritePrometheus(w io.Writer) {
	metrics.WritePrometheus(w, true)
}
