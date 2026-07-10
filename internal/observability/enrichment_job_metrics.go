package observability

import (
	"time"

	"github.com/VictoriaMetrics/metrics"
)

// Story M-2 — per-job enrichment-worker saturation (RED) metrics. One family
// per kind ∈ {series, person, omdb} (bounded — see EntityKind.IsValid). These
// sit at the dispatcher's single per-job choke point (runHandler), so ALL kinds
// are covered by one wrapper. The enrichment QUEUE depth/drops family
// (enrichment_queue_depth / enrichment_queue_drops_total, written from queue.go)
// is a separate stage and is NOT duplicated here — M-2 measures handler
// execution, not queue occupancy.

// IncEnrichmentJobInflight marks a job of kind as in-flight. Paired with the Dec
// inside ObserveEnrichmentJobDone via a SINGLE defer at the call site, so the
// gauge is balanced on EVERY exit path including panic and error. kind is a
// bounded EntityKind string ("series"/"person"/"omdb") — never user-derived.
func IncEnrichmentJobInflight(kind string) {
	metrics.GetOrCreateGauge(`seasonfill_enrichment_job_inflight{kind="`+kind+`"}`, nil).Inc()
}

// ObserveEnrichmentJobDone closes out one job: decrements the inflight gauge,
// records the handler wall-time histogram, and increments the per-(kind,result)
// outcome counter. result ∈ {success, error, skipped}; skipped is the
// nil-handler placeholder path (an unset person/omdb handler slot).
func ObserveEnrichmentJobDone(kind, result string, dur time.Duration) {
	metrics.GetOrCreateGauge(`seasonfill_enrichment_job_inflight{kind="`+kind+`"}`, nil).Dec()
	metrics.GetOrCreateHistogram(`seasonfill_enrichment_job_duration_seconds{kind="` + kind + `"}`).Update(dur.Seconds())
	metrics.GetOrCreateCounter(`seasonfill_enrichment_job_total{kind="` + kind + `",result="` + result + `"}`).Inc()
}
