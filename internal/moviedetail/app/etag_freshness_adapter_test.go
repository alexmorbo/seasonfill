package app_test

import (
	"context"
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
	ports "github.com/alexmorbo/seasonfill/internal/shared/dataports"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
	edge "github.com/alexmorbo/seasonfill/internal/shared/http/edge"
)

// fakeMovieCanon is a stub mdapp.CanonReader: it returns a fixed canon (or a
// forced error) and records the tmdb id it was asked for.
type fakeMovieCanon struct {
	canon   movie.Canon
	err     error
	gotTMDB domain.TMDBID
}

func (f *fakeMovieCanon) GetByTMDBID(_ context.Context, id domain.TMDBID) (movie.Canon, error) {
	f.gotTMDB = id
	if f.err != nil {
		return movie.Canon{}, f.err
	}
	return f.canon, nil
}

func TestMovieETagFreshnessAdapter_SectionMapping(t *testing.T) {
	text := time.Unix(1_700_000_100, 0).UTC()
	cast := time.Unix(1_700_000_200, 0).UTC()
	recs := time.Unix(1_700_000_300, 0).UTC()
	canon := movie.Canon{
		EnrichmentTextSyncedAt: &text,
		EnrichmentCastSyncedAt: &cast,
		EnrichmentRecsSyncedAt: &recs,
	}
	fake := &fakeMovieCanon{canon: canon}
	a := mdapp.NewMovieETagFreshnessAdapter(fake)

	cases := []struct {
		section string
		want    *time.Time
	}{
		{"overview", &text},
		{"cast", &cast},
		{"recs", &recs},
		{"skeleton", nil},
		{"season", nil},
		{"media", nil},
		{"unknown", nil},
	}
	for _, tc := range cases {
		t.Run(tc.section, func(t *testing.T) {
			got, err := a.SectionSyncedAt(t.Context(), 603, tc.section, 0)
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}
	assert.Equal(t, domain.TMDBID(603), fake.gotTMDB, "adapter must resolve via tmdb id")
}

func TestMovieETagFreshnessAdapter_ErrorBubbles(t *testing.T) {
	fake := &fakeMovieCanon{err: ports.ErrNotFound}
	a := mdapp.NewMovieETagFreshnessAdapter(fake)

	got, err := a.SectionSyncedAt(t.Context(), 42, "cast", 0)
	require.ErrorIs(t, err, ports.ErrNotFound)
	assert.Nil(t, got)
}

// TestMovieETag_EndToEnd wires the REAL generalized middleware + REAL movie
// adapter onto a movie route and proves: (1) a stable ETag keyed off tmdb id +
// stamp + lang + section, (2) 304 on If-None-Match match, (3) the ETag CHANGES
// when the section stamp advances (old validator no longer 304s).
func TestMovieETag_EndToEnd(t *testing.T) {
	gin.SetMode(gin.TestMode)
	stamp := time.Unix(1_700_000_000, 0).UTC()
	fake := &fakeMovieCanon{canon: movie.Canon{EnrichmentCastSyncedAt: &stamp}}
	adapter := mdapp.NewMovieETagFreshnessAdapter(fake)

	var called bool
	r := gin.New()
	r.GET("/movies/:tmdb_id/cast", edge.ETagMiddleware("tmdb_id", adapter, nil), func(c *gin.Context) {
		called = true
		c.String(http.StatusOK, "CAST-BODY")
	})

	// miss → 200 + ETag keyed off tmdb id + stamp + lang + "cast"
	w := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/movies/603/cast?lang=ru", nil)
	r.ServeHTTP(w, req)
	require.True(t, called)
	assert.Equal(t, http.StatusOK, w.Code)
	wantETag := fmt.Sprintf(`W/"603-%d-ru-cast"`, stamp.Unix())
	assert.Equal(t, wantETag, w.Header().Get("ETag"))
	assert.Equal(t, "private, max-age=60, stale-while-revalidate=600", w.Header().Get("Cache-Control"))
	assert.Equal(t, domain.TMDBID(603), fake.gotTMDB)

	// If-None-Match match → 304, no body, handler skipped
	called = false
	w2 := httptest.NewRecorder()
	req2 := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/movies/603/cast?lang=ru", nil)
	req2.Header.Set("If-None-Match", wantETag)
	r.ServeHTTP(w2, req2)
	assert.Equal(t, http.StatusNotModified, w2.Code)
	assert.Empty(t, w2.Body.String(), "304 carries no body")
	assert.False(t, called, "handler must NOT run on 304")

	// stamp advances → different ETag; the old validator no longer 304s
	advanced := stamp.Add(time.Hour)
	fake.canon.EnrichmentCastSyncedAt = &advanced
	called = false
	w3 := httptest.NewRecorder()
	req3 := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/movies/603/cast?lang=ru", nil)
	req3.Header.Set("If-None-Match", wantETag) // stale validator
	r.ServeHTTP(w3, req3)
	require.True(t, called, "advanced stamp must invalidate the old ETag")
	assert.Equal(t, http.StatusOK, w3.Code)
	newETag := fmt.Sprintf(`W/"603-%d-ru-cast"`, advanced.Unix())
	assert.Equal(t, newETag, w3.Header().Get("ETag"))
	assert.NotEqual(t, wantETag, newETag, "ETag MUST change when the section stamp advances")
}
