package observability

import "github.com/VictoriaMetrics/metrics"

// M-8 backfill coverage-detail gauges. All are periodically SAMPLED gauges
// published by the 5-minute collector (cmd/server/loops/enrichment_coverage.go),
// NOT counters — the `_total` suffix on checked-empty follows the operator-
// specified metric name; VictoriaMetrics does not attach counter semantics to
// the suffix.

// MetricEnrichmentPosterCoverageRatio is the per-language share of LIBRARY
// series (distinct non-deleted series_cache.series_id) that carry a NON-EMPTY
// localized poster (series_media_texts.poster_asset IS NOT NULL AND <> ”).
// #1110: row presence != poster presence — a checked-but-empty row does NOT
// count as covered. total==0 → 1.0 (vacuously complete), matching the
// SetLibraryPosterCoverage convention. Label: lang (e.g. en-US, ru-RU).
const MetricEnrichmentPosterCoverageRatio = `seasonfill_enrichment_poster_coverage_ratio`

// MetricEnrichmentCheckedEmptyTotal is the count of #1081b checked-but-empty
// per-locale markers in the library: asset NULL/empty AND *_checked_at SET
// ("we asked TMDB, no localized art exists"). Label: kind ∈ {poster, backdrop}.
const MetricEnrichmentCheckedEmptyTotal = `seasonfill_enrichment_checked_empty_total`

// MetricEnrichmentUnenrichedSeries splits the remaining backfill by reason.
// Label: reason ∈ {no_tmdb_id (tmdb_id IS NULL — unenrichable via TMDB),
// never_synced (tmdb_id NOT NULL AND enrichment_tmdb_synced_at IS NULL —
// equals enrichment_cold_start_remaining)}.
const MetricEnrichmentUnenrichedSeries = `seasonfill_enrichment_unenriched_series`

// SetEnrichmentPosterCoverageRatio publishes the ratio [0,1] for one language.
func SetEnrichmentPosterCoverageRatio(lang string, ratio float64) {
	metrics.GetOrCreateGauge(
		MetricEnrichmentPosterCoverageRatio+`{lang="`+lang+`"}`, nil,
	).Set(ratio)
}

// SetEnrichmentCheckedEmpty publishes the checked-but-empty count for one kind.
func SetEnrichmentCheckedEmpty(kind string, n int64) {
	metrics.GetOrCreateGauge(
		MetricEnrichmentCheckedEmptyTotal+`{kind="`+kind+`"}`, nil,
	).Set(float64(n))
}

// SetEnrichmentUnenrichedSeries publishes the unenriched count for one reason.
func SetEnrichmentUnenrichedSeries(reason string, n int64) {
	metrics.GetOrCreateGauge(
		MetricEnrichmentUnenrichedSeries+`{reason="`+reason+`"}`, nil,
	).Set(float64(n))
}

// EnrichmentCoverageMetricsAdapter satisfies the loops collector's narrow
// metrics port. Zero value works.
type EnrichmentCoverageMetricsAdapter struct{}

// SetPosterCoverageRatio implements loops.EnrichmentCoverageMetrics.
func (EnrichmentCoverageMetricsAdapter) SetPosterCoverageRatio(lang string, ratio float64) {
	SetEnrichmentPosterCoverageRatio(lang, ratio)
}

// SetCheckedEmpty implements loops.EnrichmentCoverageMetrics.
func (EnrichmentCoverageMetricsAdapter) SetCheckedEmpty(kind string, n int64) {
	SetEnrichmentCheckedEmpty(kind, n)
}

// SetUnenrichedSeries implements loops.EnrichmentCoverageMetrics.
func (EnrichmentCoverageMetricsAdapter) SetUnenrichedSeries(reason string, n int64) {
	SetEnrichmentUnenrichedSeries(reason, n)
}
