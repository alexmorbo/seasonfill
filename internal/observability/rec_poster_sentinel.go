package observability

import "github.com/VictoriaMetrics/metrics"

// MetricRecPosterSentinel counts recommendation-card poster resolves that fell
// through to the missing-art sentinel hash, labeled by why. Story 1110 — lets us
// see in prod whether placeholders come from a missing i18n row, a confirmed-
// absent (empty-poster) row, or a genuine media-pipeline resolver miss.
const MetricRecPosterSentinel = "seasonfill_rec_poster_sentinel_total"

// Recommendation poster sentinel reasons. Fixed, low-cardinality set (3) — never
// put series_id or lang in the label.
const (
	// RecPosterSentinelNoRow — the series had no series_media_texts entry in the
	// batch map (nil raw path, no row present).
	RecPosterSentinelNoRow = "no_row"
	// RecPosterSentinelEmptyPosterRow — a row was present but its poster_asset was
	// NULL/empty (Story 1081b confirmed-absent, or an un-warmed stub).
	RecPosterSentinelEmptyPosterRow = "empty_poster_row"
	// RecPosterSentinelResolverMiss — the raw poster path was non-empty but the
	// media resolver still returned the sentinel (EnsurePending failure etc.).
	RecPosterSentinelResolverMiss = "resolver_miss"
)

// IncRecPosterSentinel ticks the per-reason counter. reason MUST be one of the
// three RecPosterSentinel* constants (cardinality budget = 3 series).
func IncRecPosterSentinel(reason string) {
	metrics.GetOrCreateCounter(`seasonfill_rec_poster_sentinel_total{reason="` + reason + `"}`).Inc()
}
