package observability

import "github.com/VictoriaMetrics/metrics"

// MetricGridPosterSentinel counts catalog/library grid tiles whose poster
// resolved to the missing-art state (a nil poster_hash → FE monogram), labeled
// by why. Task 1127 "Observability B" — the grid analogue of the recommendation
// sentinel (rec_poster_sentinel.go). Unlike recs/person the grid computes the
// hash directly from the poster path (mediaHashForPosterAsset), so the missing
// state manifests as a nil poster_hash rather than the SentinelMissingHash — the
// classification signals (series_media_texts row-presence vs poster-presence)
// are identical, so the same reason vocabulary applies.
const MetricGridPosterSentinel = "seasonfill_grid_poster_sentinel_total"

// Grid poster sentinel reasons. Same fixed, low-cardinality vocabulary as the
// rec sentinel so dashboards stay consistent — never put series_id or lang in
// the label.
const (
	// GridPosterSentinelNoRow — the tile had no series_media_texts entry in the
	// per-lang batch map (and no canon poster), so no poster_hash was produced.
	GridPosterSentinelNoRow = "no_row"
	// GridPosterSentinelEmptyPosterRow — a series_media_texts row was present but
	// its poster_asset was NULL/empty (and no canon poster), so no poster_hash was
	// produced.
	GridPosterSentinelEmptyPosterRow = "empty_poster_row"
	// GridPosterSentinelResolverMiss — defined for vocabulary parity with the rec
	// sentinel. The grid derives the hash directly from a non-empty poster path
	// (never through the media resolver), so a non-empty poster always yields a
	// hash — this reason never fires on the grid but is kept so the label set
	// matches recs/person for uniform dashboard queries.
	GridPosterSentinelResolverMiss = "resolver_miss"
)

// IncGridPosterSentinel ticks the per-reason counter. reason MUST be one of the
// three GridPosterSentinel* constants (cardinality budget = 3 series).
func IncGridPosterSentinel(reason string) {
	metrics.GetOrCreateCounter(`seasonfill_grid_poster_sentinel_total{reason="` + reason + `"}`).Inc()
}
