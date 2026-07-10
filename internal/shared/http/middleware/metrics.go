package middleware

import (
	"strconv"
	"time"

	"github.com/VictoriaMetrics/metrics"
	"github.com/gin-gonic/gin"
)

// routeUnmatched is the single bucket every unrouted request (gin 404,
// FullPath()=="") collapses into, so probes hammering random paths can't
// mint an unbounded number of route-label series.
const routeUnmatched = "unmatched"

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
// constant "unmatched". All recording lives in one deferred closure so the
// in_flight gauge decrements — and duration/count still record — even if a
// downstream handler panics (gin.Recovery, registered outermost in server.go,
// turns the panic into a 500 for the client before its own recover runs).
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
			method := c.Request.Method
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
