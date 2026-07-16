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
// cardinality: 4 (changed/hot/normal/cold). Result cardinality: 3 (ok/error/
// skipped). 12 series total — well inside cardinality budget.
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

// IncRefreshPickedMissingPoster ticks once per candidate the picker
// selected via the W17-1 HOT poster-guard branch (library series with
// no series_media_texts.poster_asset). No labels — one process-wide
// counter. rate() over the backfill window reads "posters healed per
// tick"; it flattens to ~0 once the 49 stuck series drain (the two
// tmdb-less series never enter this branch).
func (m *EnrichmentRefreshMetrics) IncRefreshPickedMissingPoster() {
	metrics.GetOrCreateCounter(`seasonfill_enrichment_refresh_picked_missing_poster_total`).Inc()
}

// IncRefreshPickedHeal ticks once per candidate the picker selected via the
// #1090b null-heal branch (a series with media_type='tv' person_credits that
// all carry a NULL last_appearance_season). No labels — one process-wide
// counter. rate() over a tick window reads "heal picks per tick"; unlike the
// poster counter it does NOT flatten to ~0, because genuinely-unfillable series
// (crew-only / specials-only cast) re-pick every 6h forever — the steady-state
// rate is the unfillable floor.
func (m *EnrichmentRefreshMetrics) IncRefreshPickedHeal() {
	metrics.GetOrCreateCounter(`seasonfill_enrichment_refresh_picked_heal_total`).Inc()
}
