package middleware

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/internal/observability"
)

// dump renders the global VictoriaMetrics set to a string via the same
// exposition path the /metrics endpoint uses.
func dump() string {
	buf := &bytes.Buffer{}
	observability.WritePrometheus(buf)
	return buf.String()
}

// counterValue parses the numeric value of the exposition line whose series
// (name+labels) is series. Returns 0 when the series is absent — so a
// before/after delta cleanly reads as +1 on first registration.
func counterValue(t *testing.T, body, series string) float64 {
	t.Helper()
	for line := range strings.SplitSeq(body, "\n") {
		if strings.HasPrefix(line, series+" ") {
			fields := strings.Fields(line)
			v, err := strconv.ParseFloat(fields[len(fields)-1], 64)
			require.NoError(t, err)
			return v
		}
	}
	return 0
}

func TestMetricsMiddleware_MatchedRoute(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(MetricsMiddleware())
	r.GET("/x/:id", func(c *gin.Context) { c.String(http.StatusOK, "ok") })

	const series = `seasonfill_http_requests_total{route="/x/:id",method="GET",status="200"}`
	before := counterValue(t, dump(), series)

	w := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/x/42", nil)
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)

	body := dump()
	// route label is the TEMPLATE, not the concrete /x/42.
	assert.Equal(t, before+1, counterValue(t, body, series),
		"requests_total{route=/x/:id,...,status=200} must increment by exactly 1")
	// duration histogram observed — _count carries the full label set.
	assert.Contains(t, body,
		`seasonfill_http_request_duration_seconds_count{route="/x/:id",method="GET"}`)
	// in_flight balanced back to 0 after the synchronous request completed.
	assert.Contains(t, body, "seasonfill_http_requests_in_flight 0")
}

func TestMetricsMiddleware_UnmatchedRoute(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(MetricsMiddleware())
	// No routes registered: gin still runs global middleware, then its default
	// 404. c.FullPath() == "" → route label collapses to "unmatched".

	const series = `seasonfill_http_requests_total{route="unmatched",method="GET",status="404"}`
	before := counterValue(t, dump(), series)

	w := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/no/such/path", nil)
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusNotFound, w.Code)

	body := dump()
	assert.Equal(t, before+1, counterValue(t, body, series),
		`unmatched request must land in route="unmatched", not the raw path`)
	// Guard against the raw path ever leaking as a route label.
	assert.NotContains(t, body, `route="/no/such/path"`)
}

func TestMetricsMiddleware_PanicSafeInFlight(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(gin.Recovery()) // outermost, mirrors server.go
	r.Use(MetricsMiddleware())
	r.GET("/boom", func(c *gin.Context) { panic("boom") })

	w := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/boom", nil)
	require.NotPanics(t, func() { r.ServeHTTP(w, req) })
	require.Equal(t, http.StatusInternalServerError, w.Code)

	// The deferred Dec ran during panic unwinding, so the gauge is back to 0.
	assert.Contains(t, dump(), "seasonfill_http_requests_in_flight 0")
}

func TestMethodLabel(t *testing.T) {
	for _, m := range []string{
		http.MethodGet, http.MethodPost, http.MethodPut, http.MethodPatch,
		http.MethodDelete, http.MethodHead, http.MethodOptions,
	} {
		assert.Equalf(t, m, methodLabel(m),
			"standard method %q must pass through verbatim", m)
	}
	for _, m := range []string{"FOOBAR", "get", "CONNECT", "TRACE", "", "PROPFIND"} {
		assert.Equalf(t, methodOther, methodLabel(m),
			"non-standard verb %q must collapse to %q", m, methodOther)
	}
}

// TestMetricsMiddleware_NonStandardMethodCollapsesToOther reproduces the F-02
// probe: a bogus verb on an unrouted path (curl -X FOOBAR /anything) must land
// in method="other", never minting a raw {method="FOOBAR"} series.
func TestMetricsMiddleware_NonStandardMethodCollapsesToOther(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(MetricsMiddleware())
	// No route registered: gin runs global middleware then its default 404.

	const other = `seasonfill_http_requests_total{route="unmatched",method="other",status="404"}`
	before := counterValue(t, dump(), other)

	w := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(context.Background(), "FOOBAR", "/anything", nil)
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusNotFound, w.Code)

	body := dump()
	assert.Equal(t, before+1, counterValue(t, body, other),
		`bogus verb must collapse to method="other"`)
	assert.NotContains(t, body, `method="FOOBAR"`,
		`raw verb must never be stamped as a method label`)
}

// TestMetricsMiddleware_StandardMethodVerbatim proves the guard is a no-op for
// real traffic: a POST stamps method="POST" byte-identically to today.
func TestMetricsMiddleware_StandardMethodVerbatim(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(MetricsMiddleware())
	r.POST("/y", func(c *gin.Context) { c.String(http.StatusOK, "ok") })

	const series = `seasonfill_http_requests_total{route="/y",method="POST",status="200"}`
	before := counterValue(t, dump(), series)

	w := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/y", nil)
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)

	assert.Equal(t, before+1, counterValue(t, dump(), series),
		`standard method label must be byte-identical to today`)
}
