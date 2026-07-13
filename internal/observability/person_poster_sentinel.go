package observability

import "github.com/VictoriaMetrics/metrics"

// MetricPersonPosterSentinel counts person-page credit posters (library_credits
// + other_credits) that resolved to the missing-art sentinel hash, labeled by
// why. Task 1127 "Observability B" — the person analogue of the recommendation
// sentinel (rec_poster_sentinel.go). The person use case resolves each credit
// poster through the shared MediaResolver (exactly like recs), so a miss comes
// back as the SentinelMissingHash and the classification reuses the same signals.
const MetricPersonPosterSentinel = "seasonfill_person_poster_sentinel_total"

// Person poster sentinel reasons. Same fixed, low-cardinality vocabulary as the
// rec sentinel so dashboards stay consistent — never put tmdb_id, series_id or
// lang in the label.
const (
	// PersonPosterSentinelNoRow — the credit had no series_media_texts entry (nil
	// staged poster path, no row present) and resolved to the sentinel.
	PersonPosterSentinelNoRow = "no_row"
	// PersonPosterSentinelEmptyPosterRow — a series_media_texts row was present but
	// its poster_asset was NULL/empty (confirmed-absent or un-warmed stub).
	PersonPosterSentinelEmptyPosterRow = "empty_poster_row"
	// PersonPosterSentinelResolverMiss — the staged poster path was non-empty but
	// the media resolver still returned the sentinel (EnsurePending failure etc.).
	PersonPosterSentinelResolverMiss = "resolver_miss"
)

// IncPersonPosterSentinel ticks the per-reason counter. reason MUST be one of the
// three PersonPosterSentinel* constants (cardinality budget = 3 series).
func IncPersonPosterSentinel(reason string) {
	metrics.GetOrCreateCounter(`seasonfill_person_poster_sentinel_total{reason="` + reason + `"}`).Inc()
}
