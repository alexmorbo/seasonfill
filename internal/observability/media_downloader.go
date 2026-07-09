package observability

import (
	"time"

	"github.com/VictoriaMetrics/metrics"
)

// Story M-1 — media pre-warm pipeline saturation metrics. Thin adapter
// over the VictoriaMetrics global registry (same idiom as
// EnrichmentRefreshMetrics). The adapter is stateless — VictoriaMetrics
// owns the series by name — so multiple instances (the Enqueuer builds
// one; the Downloader shares it) write to the same process-wide series.
//
// All methods are nil-guarded so a zero-value pipeline (adapter never
// wired) degrades to a no-op instead of panicking.
const (
	// Gauge: pending jobs in the pre-warm channel. Set on each successful
	// enqueue (producer) and each worker dequeue (consumer) so it tracks
	// true backpressure. Approximate under contention (last writer wins),
	// which is exactly right for a depth gauge.
	MetricMediaPrewarmQueueDepth = `seasonfill_media_prewarm_queue_depth`
	// Gauge: channel capacity (channelCap). Set once at Enqueuer
	// construction. depth/capacity → % full on the dashboard.
	MetricMediaPrewarmQueueCapacity = `seasonfill_media_prewarm_queue_capacity`
	// Counter: jobs dropped because the channel was full. rate() reads
	// "cold-start overflow pressure"; previously visible only as a
	// rate-limited WARN (media.prewarm.queue_full).
	MetricMediaPrewarmDropsTotal = `seasonfill_media_prewarm_drops_total`
	// Gauge: configured drain-goroutine count. Set at Start.
	MetricMediaDownloaderWorkers = `seasonfill_media_downloader_workers`
	// Gauge: jobs currently executing inside handle(). Inc on enter, Dec
	// via defer on every exit path. inflight/workers → pool saturation.
	MetricMediaDownloaderInflight = `seasonfill_media_downloader_inflight`
	// Histogram (label outcome): per-job wall-clock in seconds — the same
	// value behind the existing duration_ms logs.
	MetricMediaDownloadDurationSeconds = `seasonfill_media_download_duration_seconds`
	// Counter (label outcome): one tick per handle() call, keyed by the
	// terminal branch.
	MetricMediaDownloadTotal = `seasonfill_media_download_total`
)

// Media download outcome labels — closed, bounded set (6). DERIVED from
// the real handle() terminal branches; never add url/hash/kind.
const (
	MediaDownloadOutcomeSuccess       = "success"
	MediaDownloadOutcomeStatHit       = "stat_hit"
	MediaDownloadOutcomeDownloadError = "download_error"
	MediaDownloadOutcomePutError      = "put_error"
	MediaDownloadOutcomeRowError      = "row_error"
	// MediaDownloadOutcomeIncomplete is the defer's initial value —
	// recorded only if a branch never set outcome (an unexpected panic
	// inside handle). Keeps the panic path bounded to one extra series.
	MediaDownloadOutcomeIncomplete = "incomplete"
)

// MediaDownloaderMetrics is the M-1 adapter. Stateless namespace over
// the global registry.
type MediaDownloaderMetrics struct{}

// NewMediaDownloaderMetrics returns the adapter. No args — the registry
// is global; the adapter is a thin namespace.
func NewMediaDownloaderMetrics() *MediaDownloaderMetrics {
	return &MediaDownloaderMetrics{}
}

// SetQueueCapacity publishes the channel cap once at construction.
func (m *MediaDownloaderMetrics) SetQueueCapacity(n int) {
	if m == nil {
		return
	}
	metrics.GetOrCreateGauge(MetricMediaPrewarmQueueCapacity, nil).Set(float64(n))
}

// SetQueueDepth publishes the current pending-job count.
func (m *MediaDownloaderMetrics) SetQueueDepth(n int) {
	if m == nil {
		return
	}
	metrics.GetOrCreateGauge(MetricMediaPrewarmQueueDepth, nil).Set(float64(n))
}

// IncDrop ticks the channel-full drop counter.
func (m *MediaDownloaderMetrics) IncDrop() {
	if m == nil {
		return
	}
	metrics.GetOrCreateCounter(MetricMediaPrewarmDropsTotal).Inc()
}

// SetWorkers publishes the configured drain-goroutine count.
func (m *MediaDownloaderMetrics) SetWorkers(n int) {
	if m == nil {
		return
	}
	metrics.GetOrCreateGauge(MetricMediaDownloaderWorkers, nil).Set(float64(n))
}

// IncInflight / DecInflight move the in-flight gauge. Gauge Inc/Dec are
// atomic in VictoriaMetrics, so concurrent workers are safe.
func (m *MediaDownloaderMetrics) IncInflight() {
	if m == nil {
		return
	}
	metrics.GetOrCreateGauge(MetricMediaDownloaderInflight, nil).Inc()
}

func (m *MediaDownloaderMetrics) DecInflight() {
	if m == nil {
		return
	}
	metrics.GetOrCreateGauge(MetricMediaDownloaderInflight, nil).Dec()
}

// IncDownload ticks the per-outcome terminal counter. outcome MUST be
// one of the MediaDownloadOutcome* constants (cardinality budget = 6).
func (m *MediaDownloaderMetrics) IncDownload(outcome string) {
	if m == nil {
		return
	}
	metrics.GetOrCreateCounter(MetricMediaDownloadTotal + `{outcome="` + outcome + `"}`).Inc()
}

// ObserveDownload records per-job wall-clock in seconds, labeled by
// outcome (seconds unit matches the other _seconds histograms).
func (m *MediaDownloaderMetrics) ObserveDownload(outcome string, d time.Duration) {
	if m == nil {
		return
	}
	metrics.GetOrCreateHistogram(MetricMediaDownloadDurationSeconds + `{outcome="` + outcome + `"}`).Update(d.Seconds())
}
