package rest_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"

	catalogrest "github.com/alexmorbo/seasonfill/internal/catalog/rest"
)

// The GlobalCatalogHandler delegates to the per-instance InstancesHandler
// when ?instance= is supplied. Direct unit tests of the delegation path
// require constructing an InstancesHandler with all its ports — that's
// covered end-to-end by the BE live-curl smoke (Verify plan). Here we
// pin the 400-on-missing-instance gate which is the only logic owned by
// the wrapper itself.

func TestGlobalCatalogHandler_List_MissingInstance_400(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := catalogrest.NewGlobalCatalogHandler(nil, nil)
	r := gin.New()
	r.GET("/api/v1/series", h.List)

	rec := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/v1/series", nil)
	r.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Contains(t, rec.Body.String(), "instance query param required")
}

func TestGlobalCatalogHandler_Networks_MissingInstance_400(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := catalogrest.NewGlobalCatalogHandler(nil, nil)
	r := gin.New()
	r.GET("/api/v1/series/networks", h.Networks)

	rec := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/v1/series/networks", nil)
	r.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Contains(t, rec.Body.String(), "instance query param required")
}

func TestGlobalCatalogHandler_List_NilInner_500WithInstance(t *testing.T) {
	// inner=nil + instance set → 500 (defensive guard); production wiring
	// always passes a non-nil inner.
	gin.SetMode(gin.TestMode)
	h := catalogrest.NewGlobalCatalogHandler(nil, nil)
	r := gin.New()
	r.GET("/api/v1/series", h.List)

	rec := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/v1/series?instance=homelab", nil)
	r.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusInternalServerError, rec.Code)
}

func TestGlobalCatalogHandler_Networks_NilInner_500WithInstance(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := catalogrest.NewGlobalCatalogHandler(nil, nil)
	r := gin.New()
	r.GET("/api/v1/series/networks", h.Networks)

	rec := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/v1/series/networks?instance=homelab", nil)
	r.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusInternalServerError, rec.Code)
}
