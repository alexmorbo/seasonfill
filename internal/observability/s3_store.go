package observability

import (
	"strconv"

	"github.com/VictoriaMetrics/metrics"
)

// S3 media-store metrics (Story W19-M). Emitted by the meteredStore
// decorator in internal/mediaproxy/infrastructure — one family per
// aspect of a store operation (request outcome, latency, HTTP status,
// bytes moved, in-flight concurrency). The `op` label is a fixed
// closed set (get/put/stat/delete/list) and `outcome` is
// ok/not_found/timeout/error — both literals from the decorator, so
// there is no metric-name injection surface.
//
// Metric names are frozen: adding/removing a label key here breaks
// Grafana queries. New label *values* are fine.
const (
	MetricS3RequestsTotal     = `seasonfill_s3_requests_total`
	MetricS3RequestDuration   = `seasonfill_s3_request_duration_seconds`
	MetricS3ResponseCodeTotal = `seasonfill_s3_response_code_total`
	MetricS3BytesTotal        = `seasonfill_s3_bytes_total`
	MetricS3Inflight          = `seasonfill_s3_inflight`
)

// IncS3Request bumps the per-(op,outcome) request counter.
func IncS3Request(op, outcome string) {
	metrics.GetOrCreateCounter(`seasonfill_s3_requests_total{op="` + op + `",outcome="` + outcome + `"}`).Inc()
}

// ObserveS3Duration records the wall-clock latency of a single store
// operation in seconds.
func ObserveS3Duration(op string, seconds float64) {
	metrics.GetOrCreateHistogram(`seasonfill_s3_request_duration_seconds{op="` + op + `"}`).Update(seconds)
}

// IncS3ResponseCode bumps the per-(op,code) response-code counter. code
// is the HTTP status parsed from a minio.ErrorResponse (0 when the
// error carries no HTTP status — timeout / transport).
func IncS3ResponseCode(op string, code int) {
	metrics.GetOrCreateCounter(`seasonfill_s3_response_code_total{op="` + op + `",code="` + strconv.Itoa(code) + `"}`).Inc()
}

// AddS3Bytes adds n bytes moved by the op (get download / put upload).
// Callers guard n > 0 — a zero/negative delta is never recorded.
func AddS3Bytes(op string, n int64) {
	metrics.GetOrCreateCounter(`seasonfill_s3_bytes_total{op="` + op + `"}`).Add(int(n))
}

// IncS3Inflight bumps the per-op in-flight gauge at the start of an op.
func IncS3Inflight(op string) {
	metrics.GetOrCreateGauge(`seasonfill_s3_inflight{op="`+op+`"}`, nil).Add(1)
}

// DecS3Inflight decrements the per-op in-flight gauge (deferred at the
// start of an op so it fires on every return path).
func DecS3Inflight(op string) {
	metrics.GetOrCreateGauge(`seasonfill_s3_inflight{op="`+op+`"}`, nil).Add(-1)
}
