package observability

import (
	"time"

	"github.com/VictoriaMetrics/metrics"
)

// TMDBChangesMetrics is the W2-4 metric adapter for the /tv/changes poller
// (plan §9.1). Mirrors EnrichmentRefreshMetrics — a thin namespace over the
// VictoriaMetrics global registry; label sets are built inline. Satisfies the
// app-layer enrichment.ChangesMetrics port (the compile check lives in W2-6
// wiring, which assigns this to the ChangesPollerDeps.Metrics field).
//
// Deliberately NOT implemented here:
//   - seasonfill_tmdb_changes_dedup_skipped_total — §9.1/§481 marks it optional;
//     it needs a second COUNT query the poller does not run. Deferred.
//   - seasonfill_tmdb_changes_pending — a DB COUNT gauge with a NOT EXISTS
//     attempts>5 guard (B-05); the poller has no reader for it. Belongs to the
//     dashboard story W2-7.
type TMDBChangesMetrics struct{}

// NewTMDBChangesMetrics returns the singleton adapter. No args — VictoriaMetrics
// owns the registry.
func NewTMDBChangesMetrics() *TMDBChangesMetrics {
	return &TMDBChangesMetrics{}
}

// IncPoll increments the per-result poll counter. result label cardinality: 5
// (ok / error / skipped_no_client / skipped_inflight / cursor_reset).
func (m *TMDBChangesMetrics) IncPoll(result string) {
	metrics.GetOrCreateCounter(`seasonfill_tmdb_changes_poll_total{result="` + result + `"}`).Inc()
}

// AddPages counts firehose pages downloaded.
func (m *TMDBChangesMetrics) AddPages(n int) {
	metrics.GetOrCreateCounter(`seasonfill_tmdb_changes_pages_total`).Add(n)
}

// AddFirehoseIDs counts ids received (after in-poll dedup).
func (m *TMDBChangesMetrics) AddFirehoseIDs(n int) {
	metrics.GetOrCreateCounter(`seasonfill_tmdb_changes_firehose_ids_total`).Add(n)
}

// AddMatched counts series rows actually marked (RowsAffected).
func (m *TMDBChangesMetrics) AddMatched(n int64) {
	metrics.GetOrCreateCounter(`seasonfill_tmdb_changes_matched_total`).AddInt64(n)
}

// ObservePollDuration records full poll-tick latency.
func (m *TMDBChangesMetrics) ObservePollDuration(d time.Duration) {
	metrics.GetOrCreateHistogram(`seasonfill_tmdb_changes_poll_duration_seconds`).Update(d.Seconds())
}

// SetCursorLag records now − last_window_end (alert at > 2×interval+24h).
func (m *TMDBChangesMetrics) SetCursorLag(d time.Duration) {
	metrics.GetOrCreateGauge(`seasonfill_tmdb_changes_cursor_lag_seconds`, nil).Set(d.Seconds())
}

// IncMiss increments the firehose recall-miss counter (W2-8 / G3, plan §9.1). Fired
// by the series worker's miss-detector when a real canon-field change was observed
// on refresh that the /tv/changes firehose failed to flag ahead of the last sync
// (and the firehose window covered the interval). No labels.
func (m *TMDBChangesMetrics) IncMiss() {
	metrics.GetOrCreateCounter(`seasonfill_tmdb_changes_miss_total`).Inc()
}
