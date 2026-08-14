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
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
	edge "github.com/alexmorbo/seasonfill/internal/shared/http/edge"
)

// TestMovieOverviewETag_EndToEnd proves the /movies/:tmdb_id/overview route
// keys its ETag off the TEXT stamp (enrichment_text_synced_at) — NOT cast/recs —
// via the shared adapter's "overview"→text mapping + edge.extractSection.
func TestMovieOverviewETag_EndToEnd(t *testing.T) {
	gin.SetMode(gin.TestMode)
	text := time.Unix(1_700_000_100, 0).UTC()
	// Distinct cast/recs stamps prove the ETag tracks TEXT specifically.
	cast := time.Unix(1_700_000_200, 0).UTC()
	recs := time.Unix(1_700_000_300, 0).UTC()
	fake := &fakeMovieCanon{canon: movie.Canon{
		EnrichmentTextSyncedAt: &text,
		EnrichmentCastSyncedAt: &cast,
		EnrichmentRecsSyncedAt: &recs,
	}}
	adapter := mdapp.NewMovieETagFreshnessAdapter(fake)

	var called bool
	r := gin.New()
	r.GET("/movies/:tmdb_id/overview", edge.ETagMiddleware("tmdb_id", adapter, nil), func(c *gin.Context) {
		called = true
		c.String(http.StatusOK, "OVERVIEW-BODY")
	})

	// miss → 200 + ETag keyed off the TEXT stamp + lang + "overview" section.
	w := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/movies/603/overview?lang=ru", nil)
	r.ServeHTTP(w, req)
	require.True(t, called)
	assert.Equal(t, http.StatusOK, w.Code)
	wantETag := fmt.Sprintf(`W/"603-%d-ru-overview"`, text.Unix())
	assert.Equal(t, wantETag, w.Header().Get("ETag"), "overview ETag must key off the TEXT stamp")
	assert.Equal(t, domain.TMDBID(603), fake.gotTMDB)

	// If-None-Match match → 304, handler skipped.
	called = false
	w2 := httptest.NewRecorder()
	req2 := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/movies/603/overview?lang=ru", nil)
	req2.Header.Set("If-None-Match", wantETag)
	r.ServeHTTP(w2, req2)
	assert.Equal(t, http.StatusNotModified, w2.Code)
	assert.False(t, called, "handler must NOT run on 304")

	// text stamp advances → different ETag; the old validator no longer 304s.
	advanced := text.Add(time.Hour)
	fake.canon.EnrichmentTextSyncedAt = &advanced
	called = false
	w3 := httptest.NewRecorder()
	req3 := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/movies/603/overview?lang=ru", nil)
	req3.Header.Set("If-None-Match", wantETag)
	r.ServeHTTP(w3, req3)
	require.True(t, called, "advanced text stamp must invalidate the old ETag")
	newETag := fmt.Sprintf(`W/"603-%d-ru-overview"`, advanced.Unix())
	assert.Equal(t, newETag, w3.Header().Get("ETag"))
	assert.NotEqual(t, wantETag, newETag)
}
