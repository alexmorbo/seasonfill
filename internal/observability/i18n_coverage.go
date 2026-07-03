package observability

import "github.com/VictoriaMetrics/metrics"

// MetricI18nBaseCoverage is the per-table base-lang (en-US) coverage gauge
// (S-E1). Value is a percentage in [0,100] — the share of relevant entities
// (parent series tmdb_id NOT NULL) that carry an en-US row in `table`.
// Filled by the periodic collector (cmd/server/loops/i18n_coverage.go) every
// 5 minutes. Labels:
//   - table: closed set {series_texts, series_media_texts, episode_texts,
//     season_texts, season_media_texts}.
//
// This is the metric the S-E3 O-1 deploy gate reads: series_texts +
// series_media_texts must sit at 100 before any canon DROP. S-E1 only
// publishes the number; it enforces no gate.
const MetricI18nBaseCoverage = `seasonfill_i18n_base_coverage`

// SetI18nBaseCoverage publishes the coverage percentage for one table.
// pct is [0,100]; the collector passes 100 for an empty denominator
// (vacuously complete) so a fresh library never reads as a 0% blocker.
func SetI18nBaseCoverage(table string, pct float64) {
	metrics.GetOrCreateGauge(
		`seasonfill_i18n_base_coverage{table="`+table+`"}`, nil,
	).Set(pct)
}

// I18nCoverageMetricsAdapter satisfies the loops collector's narrow metrics
// port. Zero value works.
type I18nCoverageMetricsAdapter struct{}

// SetI18nBaseCoverage implements loops.I18nCoverageMetrics.
func (I18nCoverageMetricsAdapter) SetI18nBaseCoverage(table string, pct float64) {
	SetI18nBaseCoverage(table, pct)
}
