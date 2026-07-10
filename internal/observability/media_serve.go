package observability

import "github.com/VictoriaMetrics/metrics"

// Media serve-path metrics (Story M-5). These COMPLEMENT — and never touch — the
// store-side s3_* suite (s3_store.go) and the per-reason
// seasonfill_media_serve_degraded_total{reason}. They observe the REST serve path
// (internal/mediaproxy/rest/media.go) and the on-demand fetcher
// (internal/mediaproxy/app/ondemand.go).
//
// Every label value is a compile-time literal from the handler / fetcher (never a
// hash or url), so there is no metric-name injection surface and cardinality stays
// bounded. Metric NAMES are frozen — adding/removing a label key breaks Grafana; new
// label VALUES are fine.
const (
	// MetricMediaServeTotal — serve-mix counter, one Inc per terminal Serve branch.
	// outcome ∈ {stored,not_modified,placeholder,degraded,sentinel,invalid,repo_error}.
	MetricMediaServeTotal = `seasonfill_media_serve_total`
	// MetricMediaServeLRUHits / Misses — in-process byteCappedLRU lookup outcome.
	MetricMediaServeLRUHits   = `seasonfill_media_serve_lru_hits_total`
	MetricMediaServeLRUMisses = `seasonfill_media_serve_lru_misses_total`
	// MetricMediaServeBytesTotal — egress bytes written to the client on the 200 asset
	// path. Distinct from store-side seasonfill_s3_bytes_total (download from S3).
	MetricMediaServeBytesTotal = `seasonfill_media_serve_bytes_total`
	// MetricMediaOnDemandTotal — coarse per-FetchSync outcome.
	// result ∈ {success,fail,cooldown_short_circuit}. Independent of the finer
	// seasonfill_media_fetch_total{result,error_kind}.
	MetricMediaOnDemandTotal = `seasonfill_media_ondemand_total`
	// MetricMediaOnDemandCooldownSize — current negative-cache (cooldown map) size,
	// Set under the fetcher's negMu on every add/remove.
	MetricMediaOnDemandCooldownSize = `seasonfill_media_ondemand_cooldown_size`
)

// IncMediaServeOutcome bumps the serve-mix counter for one terminal Serve branch.
// outcome is a compile-time literal — never the hash — so cardinality stays bounded.
func IncMediaServeOutcome(outcome string) {
	metrics.GetOrCreateCounter(`seasonfill_media_serve_total{outcome="` + outcome + `"}`).Inc()
}

// IncMediaServeLRU bumps the hit or miss counter for one in-process cache lookup.
func IncMediaServeLRU(hit bool) {
	if hit {
		metrics.GetOrCreateCounter(`seasonfill_media_serve_lru_hits_total`).Inc()
		return
	}
	metrics.GetOrCreateCounter(`seasonfill_media_serve_lru_misses_total`).Inc()
}

// AddMediaServeBytes adds n egress bytes written to the client on the 200 asset path.
// Callers guard n > 0.
func AddMediaServeBytes(n int) {
	metrics.GetOrCreateCounter(`seasonfill_media_serve_bytes_total`).Add(n)
}

// IncMediaOnDemand bumps the per-result FetchSync counter. result is a compile-time
// literal ∈ {success,fail,cooldown_short_circuit}.
func IncMediaOnDemand(result string) {
	metrics.GetOrCreateCounter(`seasonfill_media_ondemand_total{result="` + result + `"}`).Inc()
}

// SetMediaOnDemandCooldownSize records the current cooldown-map size. The caller MUST
// hold the fetcher's negMu so the len() read is consistent with the map mutation; the
// gauge itself is internally synchronized. In prod a single onDemandFetcher owns it.
func SetMediaOnDemandCooldownSize(n int) {
	metrics.GetOrCreateGauge(`seasonfill_media_ondemand_cooldown_size`, nil).Set(float64(n))
}
