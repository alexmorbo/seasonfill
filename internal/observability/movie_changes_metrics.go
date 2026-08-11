package observability

import (
	"time"

	"github.com/VictoriaMetrics/metrics"
)

// MovieChangesMetrics is the Ф6-R-4a (L3-2) metric adapter for the /movie/changes
// poller. A DEDICATED series family (seasonfill_movie_changes_*) so the movie
// firehose is independently observable from the /tv/changes poller — the two
// share the generic ChangesPoller code but never share a metric series.
// Satisfies the app-layer enrichment.ChangesMetrics port. IncMiss is NOT
// implemented (the movie miss-detector is a later concern); ChangesMetrics does
// not require it.
type MovieChangesMetrics struct{}

// NewMovieChangesMetrics returns the singleton adapter.
func NewMovieChangesMetrics() *MovieChangesMetrics {
	return &MovieChangesMetrics{}
}

// IncPoll increments the per-result poll counter. result cardinality: 5
// (ok / error / skipped_no_client / skipped_inflight / cursor_reset).
func (m *MovieChangesMetrics) IncPoll(result string) {
	metrics.GetOrCreateCounter(`seasonfill_movie_changes_poll_total{result="` + result + `"}`).Inc()
}

// AddPages counts firehose pages downloaded.
func (m *MovieChangesMetrics) AddPages(n int) {
	metrics.GetOrCreateCounter(`seasonfill_movie_changes_pages_total`).Add(n)
}

// AddFirehoseIDs counts ids received (after in-poll dedup).
func (m *MovieChangesMetrics) AddFirehoseIDs(n int) {
	metrics.GetOrCreateCounter(`seasonfill_movie_changes_firehose_ids_total`).Add(n)
}

// AddMatched counts movie rows actually marked (RowsAffected).
func (m *MovieChangesMetrics) AddMatched(n int64) {
	metrics.GetOrCreateCounter(`seasonfill_movie_changes_matched_total`).AddInt64(n)
}

// ObservePollDuration records full poll-tick latency.
func (m *MovieChangesMetrics) ObservePollDuration(d time.Duration) {
	metrics.GetOrCreateHistogram(`seasonfill_movie_changes_poll_duration_seconds`).Update(d.Seconds())
}

// SetCursorLag records now − last_window_end.
func (m *MovieChangesMetrics) SetCursorLag(d time.Duration) {
	metrics.GetOrCreateGauge(`seasonfill_movie_changes_cursor_lag_seconds`, nil).Set(d.Seconds())
}
