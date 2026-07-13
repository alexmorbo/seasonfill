package middleware

import (
	"net/http"
	"strconv"
	"time"

	"github.com/VictoriaMetrics/metrics"
	"github.com/gin-gonic/gin"
)

// routeUnmatched is the single bucket every unrouted request (gin 404,
// FullPath()=="") collapses into, so probes hammering random paths can't
// mint an unbounded number of route-label series.
const routeUnmatched = "unmatched"

// methodOther is the single bucket every non-standard HTTP verb collapses into.
// The route label already collapses unknown paths to routeUnmatched, but a raw
// c.Request.Method is stamped verbatim — so a pre-auth probe (curl -X FOOBAR)
// could otherwise mint an unbounded number of {method=...} series (cardinality
// DoS).
const methodOther = "other"

// standardMethods is the whitelist of HTTP verbs that keep their label value
// verbatim. Anything outside this set collapses to methodOther. CONNECT and
// TRACE are intentionally excluded: the app registers no handler for them, so
// they can only ever arrive as probe traffic. For every real request the
// emitted method label is byte-identical to before this guard existed.
var standardMethods = map[string]struct{}{
	http.MethodGet:     {},
	http.MethodPost:    {},
	http.MethodPut:     {},
	http.MethodPatch:   {},
	http.MethodDelete:  {},
	http.MethodHead:    {},
	http.MethodOptions: {},
}

// methodLabel returns m unchanged when it is a standard HTTP method, otherwise
// the constant methodOther. It never upper-cases: net/http passes the
// request-line verb through verbatim and every real client sends canonical
// upper-case, so matching exactly preserves today's real-traffic labels while
// bounding cardinality for junk verbs.
func methodLabel(m string) string {
	if _, ok := standardMethods[m]; ok {
		return m
	}
	return methodOther
}

// MetricsMiddleware records inbound RED metrics for the app's own Gin API:
//
//   - seasonfill_http_requests_total{route,method,status}
//   - seasonfill_http_request_duration_seconds{route,method}
//   - seasonfill_http_requests_in_flight
//
// Metrics-only: it ALWAYS calls c.Next() and never alters request handling.
//
// route is the matched gin template (c.FullPath()), read AFTER c.Next() inside
// the defer so routing has populated it; unmatched requests collapse to the
// constant "unmatched". method is normalized through methodLabel so a bogus
// pre-auth verb collapses to "other" instead of minting an unbounded series.
// All recording lives in one deferred closure so the in_flight gauge decrements
// — and duration/count still record — even if a downstream handler panics
// (gin.Recovery, registered outermost in server.go, turns the panic into a 500
// for the client before its own recover runs).
//
// Mirrors the outbound httpx.MetricsTransport idiom: label values are
// concatenated straight into the VictoriaMetrics metric-name string.
func MetricsMiddleware() gin.HandlerFunc {
	inFlight := metrics.GetOrCreateGauge(`seasonfill_http_requests_in_flight`, nil)
	return func(c *gin.Context) {
		start := time.Now()
		inFlight.Inc()
		defer func() {
			inFlight.Dec()

			route := c.FullPath()
			if route == "" {
				route = routeUnmatched
			}
			method := methodLabel(c.Request.Method)
			status := strconv.Itoa(c.Writer.Status())

			metrics.GetOrCreateCounter(
				`seasonfill_http_requests_total{route="` + route +
					`",method="` + method +
					`",status="` + status + `"}`,
			).Inc()
			metrics.GetOrCreateHistogram(
				`seasonfill_http_request_duration_seconds{route="` + route +
					`",method="` + method + `"}`,
			).Update(time.Since(start).Seconds())
		}()

		c.Next()
	}
}
