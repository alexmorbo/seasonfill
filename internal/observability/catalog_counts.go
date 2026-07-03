package observability

import "github.com/VictoriaMetrics/metrics"

// MetricSeriesTotal is the total number of rows in the series table — the
// size of the catalog's top-level entities. Filled by the periodic
// collector (cmd/server/loops/catalog_counts.go) every 5 minutes.
const MetricSeriesTotal = "seasonfill_series_total"

// MetricSeasonsTotal is the total number of rows in the seasons table.
// Filled by the same periodic collector.
const MetricSeasonsTotal = "seasonfill_seasons_total"

// MetricEpisodesTotal is the total number of rows in the episodes table.
// Filled by the same periodic collector.
const MetricEpisodesTotal = "seasonfill_episodes_total"

// SetCatalogCounts publishes the three catalog-size gauges in one call.
func SetCatalogCounts(series, seasons, episodes int64) {
	metrics.GetOrCreateGauge(MetricSeriesTotal, nil).Set(float64(series))
	metrics.GetOrCreateGauge(MetricSeasonsTotal, nil).Set(float64(seasons))
	metrics.GetOrCreateGauge(MetricEpisodesTotal, nil).Set(float64(episodes))
}

// CatalogCountsMetricsAdapter satisfies the loops collector's narrow
// metrics port. Zero value works.
type CatalogCountsMetricsAdapter struct{}

// SetCatalogCounts implements loops.CatalogCountsMetrics.
func (CatalogCountsMetricsAdapter) SetCatalogCounts(series, seasons, episodes int64) {
	SetCatalogCounts(series, seasons, episodes)
}
