package observability

import "github.com/VictoriaMetrics/metrics"

// MetricWebhookOrphan counts Sonarr webhook events that arrived for a release
// seasonfill has no matching grab_records row for ("orphans"), labeled by which
// matcher missed and by the domain event type. Backlog SI-1 (risk R1) — this is
// the measure-before-fix instrument: it surfaces orphan RATE and trend so a
// later matching fix can be justified (or ruled out) by data rather than guessed.
//
// path distinguishes the two miss sites and their very different meaning:
//   - "status" — the status-transition matcher missed (handleStatus,
//     usecase.go webhook_orphan_event). ALWAYS an anomaly: a state-change event
//     for a release we never recorded a grab for.
//   - "grab"   — the grabbed-branch matcher missed (handleGrabbed,
//     usecase.go webhook_grab_orphan_no_row). Partially by-design for
//     webhook-only flows where Sonarr grabbed something seasonfill never queued.
//
// event_type is the domain webhook.EventType string (grabbed|imported|
// import_failed|series_add|series_deleted|episode_file_delete|unsupported).
// Cardinality budget = 2 paths x ~7 event types = ~14 series (realistically ~10);
// never put series_id, instance, title, or download_id in a label.
const MetricWebhookOrphan = "seasonfill_webhook_orphan_total"

// IncWebhookOrphan ticks the per-(path,event_type) orphan counter. path MUST be
// one of "status" or "grab"; eventType is string(evt.Type).
func IncWebhookOrphan(path, eventType string) {
	metrics.GetOrCreateCounter(
		`seasonfill_webhook_orphan_total{path="` + path + `",event_type="` + eventType + `"}`,
	).Inc()
}
