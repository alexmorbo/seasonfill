// Package httpx provides shared HTTP plumbing for outbound clients —
// today the Prometheus MetricsTransport (Story 351), but the package
// is the natural home for future retry/logging middleware that should
// NOT be coupled to a specific client.
//
// Dependency direction: infrastructure → VictoriaMetrics/metrics only.
// Never imports application/domain (would invert the layer rule) and
// deliberately does NOT import internal/observability — the metric
// names are documented there but written here inline via
// metrics.GetOrCreate* to keep the hot path one allocation.
package httpx

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/VictoriaMetrics/metrics"
)

// EndpointFunc maps an outbound *http.Request to a low-cardinality
// endpoint label. Implementations MUST return a stable string for a
// given logical endpoint (e.g. /tv/{id} for any /tv/123, /tv/456, …).
// Fall-through MUST return "/unknown" so a new client path surfaces
// as a single bucket rather than an unbounded series.
type EndpointFunc func(r *http.Request) string

// MetricsTransport is a thin http.RoundTripper that records:
//
//   - seasonfill_external_http_requests_total{client,endpoint,method,status}
//   - seasonfill_external_http_request_duration_seconds{client,endpoint,method,status}
//   - seasonfill_external_http_requests_in_flight{client}
//
// Construct via NewMetricsTransport. The transport wraps an inner
// RoundTripper (typically http.DefaultTransport or a proxy-aware
// transport from infrastructure/externalservices) and is goroutine-safe:
// no mutable state in the receiver, the metrics package handles
// concurrent registry access internally.
type MetricsTransport struct {
	client       string
	endpointFunc EndpointFunc
	inner        http.RoundTripper
	clock        func() time.Time
}

// NewMetricsTransport wraps inner. inner=nil falls back to
// http.DefaultTransport; endpointFunc=nil falls back to a constant
// "/unknown" so a misconfiguration surfaces loudly without panicking
// boot.
func NewMetricsTransport(client string, endpointFunc EndpointFunc, inner http.RoundTripper) *MetricsTransport {
	if inner == nil {
		inner = http.DefaultTransport
	}
	if endpointFunc == nil {
		endpointFunc = func(*http.Request) string { return "/unknown" }
	}
	return &MetricsTransport{
		client:       client,
		endpointFunc: endpointFunc,
		inner:        inner,
		clock:        time.Now,
	}
}

// RoundTrip implements http.RoundTripper. Writes to the global
// VictoriaMetrics set on every return path (2xx, non-2xx, transport
// error, ctx cancel).
func (m *MetricsTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	endpoint := m.endpointFunc(req)
	method := req.Method
	if method == "" {
		method = http.MethodGet
	}

	inFlight := metrics.GetOrCreateGauge(
		`seasonfill_external_http_requests_in_flight{client="`+m.client+`"}`,
		nil,
	)
	inFlight.Inc()
	defer inFlight.Dec()

	start := m.clock()
	resp, err := m.inner.RoundTrip(req)
	elapsed := m.clock().Sub(start).Seconds()

	status := normalizeStatus(resp, err)

	metrics.GetOrCreateCounter(
		`seasonfill_external_http_requests_total{client="` + m.client +
			`",endpoint="` + endpoint +
			`",method="` + method +
			`",status="` + status + `"}`,
	).Inc()
	metrics.GetOrCreateHistogram(
		`seasonfill_external_http_request_duration_seconds{client="` + m.client +
			`",endpoint="` + endpoint +
			`",method="` + method +
			`",status="` + status + `"}`,
	).Update(elapsed)

	return resp, err
}

// normalizeStatus maps a (resp, err) pair to the CLOSED SET of label
// values for the `status` dimension:
//
//	"200" "304" "401" "404" "429" "500" "502" "503" "504" "other" "error"
//
// A raw strconv.Itoa(resp.StatusCode) would risk unbounded cardinality
// (a misbehaving upstream emitting 418/599/999 would each become a
// fresh series); the closed set keeps the dimension to 11 values total
// while still giving the operator first-class per-code alerts on the
// codes that matter (429 rate-limit, 504 timeout, 401 token expiry).
func normalizeStatus(resp *http.Response, err error) string {
	if err != nil {
		return "error"
	}
	if resp == nil {
		return "error"
	}
	switch resp.StatusCode {
	case 200, 304, 401, 404, 429, 500, 502, 503, 504:
		return strconv.Itoa(resp.StatusCode)
	default:
		return "other"
	}
}

// IsTimeout reports whether err is a deadline/timeout. Exposed for
// callers that want to distinguish ctx-cancel from timeout in their
// own logs; the metric writes collapse both to "error".
func IsTimeout(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	var te interface{ Timeout() bool }
	if errors.As(err, &te) && te.Timeout() {
		return true
	}
	return false
}
