package observability

import (
	"time"

	"github.com/VictoriaMetrics/metrics"

	"github.com/alexmorbo/seasonfill/internal/enrichment/domain/enrichment"
)

// MovieRefreshMetrics is the Ф6-R-4a (L3-2) metric adapter for the SEPARATE
// movie refresh scheduler. A dedicated series family (seasonfill_movie_refresh_*)
// so the movie budget is independently observable from the series refresh
// counters — a movie tick never inflates seasonfill_enrichment_refresh_*.
// Satisfies the app-layer enrichment.MovieRefreshMetrics port.
type MovieRefreshMetrics struct{}

// NewMovieRefreshMetrics returns the singleton adapter. No args — VictoriaMetrics
// owns the global registry.
func NewMovieRefreshMetrics() *MovieRefreshMetrics {
	return &MovieRefreshMetrics{}
}

// IncRefresh increments the per-(tier,result) movie counter. Tier cardinality
// for movies: 2 (changed/normal). Result cardinality: 3 (ok/error/skipped).
func (m *MovieRefreshMetrics) IncRefresh(tier enrichment.RefreshTier, result string) {
	metrics.GetOrCreateCounter(
		`seasonfill_movie_refresh_total{tier="` + tier.String() + `",result="` + result + `"}`,
	).Inc()
}

// ObserveBatchSize records the size of the last movie batch the scheduler picked.
func (m *MovieRefreshMetrics) ObserveBatchSize(n int) {
	metrics.GetOrCreateGauge(`seasonfill_movie_refresh_batch_size`, nil).Set(float64(n))
}

// ObserveTickDuration records end-to-end movie-tick latency.
func (m *MovieRefreshMetrics) ObserveTickDuration(d time.Duration) {
	metrics.GetOrCreateHistogram(`seasonfill_movie_refresh_tick_seconds`).Update(d.Seconds())
}
