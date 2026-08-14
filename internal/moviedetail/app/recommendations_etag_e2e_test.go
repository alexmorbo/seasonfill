package app_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/internal/catalog/domain/movie"
	mdapp "github.com/alexmorbo/seasonfill/internal/moviedetail/app"
	edge "github.com/alexmorbo/seasonfill/internal/shared/http/edge"
)

// TestMovieRecsETag_EndToEnd wires the REAL middleware + REAL movie adapter onto
// the /recommendations route and proves: (1) a stable ETag keyed off tmdb id +
// recs stamp + section "recs", (2) 304 on If-None-Match match.
func TestMovieRecsETag_EndToEnd(t *testing.T) {
	gin.SetMode(gin.TestMode)
	stamp := time.Unix(1_700_000_000, 0).UTC()
	fake := &fakeMovieCanon{canon: movie.Canon{EnrichmentRecsSyncedAt: &stamp}}
	adapter := mdapp.NewMovieETagFreshnessAdapter(fake)

	var called bool
	r := gin.New()
	r.GET("/movies/:tmdb_id/recommendations", edge.ETagMiddleware("tmdb_id", adapter, nil), func(c *gin.Context) {
		called = true
		c.String(http.StatusOK, "RECS-BODY")
	})

	// miss → 200 + ETag keyed off tmdb id + recs stamp + section "recs"
	w := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/movies/603/recommendations", nil)
	r.ServeHTTP(w, req)
	require.True(t, called)
	assert.Equal(t, http.StatusOK, w.Code)
	wantETag := fmt.Sprintf(`W/"603-%d--recs"`, stamp.Unix())
	assert.Equal(t, wantETag, w.Header().Get("ETag"))

	// If-None-Match match → 304, no body, handler skipped
	called = false
	w2 := httptest.NewRecorder()
	req2 := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/movies/603/recommendations", nil)
	req2.Header.Set("If-None-Match", wantETag)
	r.ServeHTTP(w2, req2)
	assert.Equal(t, http.StatusNotModified, w2.Code)
	assert.Empty(t, w2.Body.String())
	assert.False(t, called, "handler must NOT run on 304")
}
