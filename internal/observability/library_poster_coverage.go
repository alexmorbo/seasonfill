package observability

import "github.com/VictoriaMetrics/metrics"

// MetricLibraryPosterCoverage is the library poster coverage ratio in [0,1]
// — the share of non-deleted library series (series_cache) that carry a
// series_media_texts row with a non-NULL poster_asset. Filled by the
// periodic collector (cmd/server/loops/library_poster_coverage.go) every 5
// minutes. An empty library reads 1.0 (vacuously complete) so a fresh pod
// never trips a low-coverage alert.
const MetricLibraryPosterCoverage = "seasonfill_library_poster_coverage"

// MetricLibraryPosterCovered is the raw count of library series that carry a
// poster asset. Filled by the same collector.
const MetricLibraryPosterCovered = "seasonfill_library_poster_covered"

// MetricLibraryPosterTotal is the raw count of non-deleted library series.
// Filled by the same collector. The steady-state alert reads
// total - covered so it survives a coverage denominator change.
const MetricLibraryPosterTotal = "seasonfill_library_poster_total"

// SetLibraryPosterCoverage publishes the covered, total and ratio gauges in
// one call. total==0 → ratio 1.0 (vacuously complete).
func SetLibraryPosterCoverage(covered, total int64) {
	ratio := 1.0
	if total > 0 {
		ratio = float64(covered) / float64(total)
	}
	metrics.GetOrCreateGauge(MetricLibraryPosterCovered, nil).Set(float64(covered))
	metrics.GetOrCreateGauge(MetricLibraryPosterTotal, nil).Set(float64(total))
	metrics.GetOrCreateGauge(MetricLibraryPosterCoverage, nil).Set(ratio)
}

// LibraryPosterCoverageMetricsAdapter satisfies the loops collector's narrow
// metrics port. Zero value works.
type LibraryPosterCoverageMetricsAdapter struct{}

// SetLibraryPosterCoverage implements loops.LibraryPosterCoverageMetrics.
func (LibraryPosterCoverageMetricsAdapter) SetLibraryPosterCoverage(covered, total int64) {
	SetLibraryPosterCoverage(covered, total)
}
