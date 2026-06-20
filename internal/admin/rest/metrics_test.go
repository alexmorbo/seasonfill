package rest

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"

	"github.com/alexmorbo/seasonfill/internal/observability"
)

func TestMetricsHandler_Returns200_WithPrometheusContentType(t *testing.T) {
	t.Parallel()
	r := gin.New()
	r.GET("/metrics", MetricsHandler())

	// Pre-register a metric so the body is non-trivial.
	observability.ScanCompleted("metrics_handler_test", "completed")

	w := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/metrics", nil)
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Header().Get("Content-Type"), "text/plain")
	assert.Contains(t, w.Body.String(), "seasonfill_scans_total")
}
