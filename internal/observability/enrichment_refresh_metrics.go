package observability

import (
	"time"

	"github.com/VictoriaMetrics/metrics"

	"github.com/alexmorbo/seasonfill/internal/enrichment/domain/enrichment"
)

// EnrichmentRefreshMetrics is the Story 534 metric adapter. Mirrors the
// pattern used by other enrichment_* adapters in this package.
type EnrichmentRefreshMetrics struct{}

// NewEnrichmentRefreshMetrics returns the singleton. No constructor
// args because VictoriaMetrics owns the global registry under the hood
// — the adapter is a thin namespace.
func NewEnrichmentRefreshMetrics() *EnrichmentRefreshMetrics {
	return &EnrichmentRefreshMetrics{}
}

// IncRefresh increments the per-(tier,result) counter. Tier label
// cardinality: 3 (hot/normal/cold). Result cardinality: 3 (ok/error/
// skipped). 9 series total — well inside cardinality budget.
func (m *EnrichmentRefreshMetrics) IncRefresh(tier enrichment.RefreshTier, result string) {
	metrics.GetOrCreateCounter(
		`seasonfill_enrichment_refresh_total{tier="` + tier.String() + `",result="` + result + `"}`,
	).Inc()
}

// ObserveBatchSize records the size of the last batch the scheduler
// picked. Gauge so the most recent value sticks across scrapes — the
// "last batch was 0" state is operationally meaningful (queue drained).
func (m *EnrichmentRefreshMetrics) ObserveBatchSize(n int) {
	metrics.GetOrCreateGauge(`seasonfill_enrichment_refresh_batch_size`, nil).Set(float64(n))
}

// ObserveTickDuration records end-to-end tick latency. Histogram with
// VM-default buckets; on a healthy system most ticks land in the
// 1-30s range.
func (m *EnrichmentRefreshMetrics) ObserveTickDuration(d time.Duration) {
	metrics.GetOrCreateHistogram(`seasonfill_enrichment_refresh_tick_seconds`).Update(d.Seconds())
}
