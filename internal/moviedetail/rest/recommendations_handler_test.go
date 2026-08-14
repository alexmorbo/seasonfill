package rest_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/internal/catalog/domain/movie"
	mdapp "github.com/alexmorbo/seasonfill/internal/moviedetail/app"
	mdrest "github.com/alexmorbo/seasonfill/internal/moviedetail/rest"
	ports "github.com/alexmorbo/seasonfill/internal/shared/dataports"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
	"github.com/alexmorbo/seasonfill/internal/shared/http/dto"
)

type stubCanon struct {
	canon movie.Canon
	err   error
}

func (s stubCanon) GetByTMDBID(_ context.Context, _ domain.TMDBID) (movie.Canon, error) {
	return s.canon, s.err
}

type stubRecs struct{ ids []domain.MovieID }

func (s stubRecs) ListByMovie(_ context.Context, _ domain.MovieID) ([]domain.MovieID, error) {
	return s.ids, nil
}

type stubBatch struct{ canons []movie.Canon }

func (s stubBatch) ListByIDs(_ context.Context, _ []domain.MovieID) ([]movie.Canon, error) {
	return s.canons, nil
}

func tid(v int) *domain.TMDBID { p := domain.TMDBID(v); return &p }

func newRecsRouter(canon mdapp.CanonReader, recs mdapp.MovieRecsReader, batch mdapp.MovieCanonBatchReader) *gin.Engine {
	uc := mdapp.NewRecommendationsUseCase(canon, recs, batch)
	// nil resolver → raw poster paths flow through unchanged (asserted below).
	h := mdrest.NewMovieRecommendationsHandler(uc, nil, nil)
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/movies/:tmdb_id/recommendations", h.Get)
	return r
}

func TestMovieRecommendationsHandler_OK(t *testing.T) {
	base := movie.Canon{ID: 1, TMDBID: tid(603)}
	p := "/p604.jpg"
	r := newRecsRouter(
		stubCanon{canon: base},
		stubRecs{ids: []domain.MovieID{40}},
		stubBatch{canons: []movie.Canon{{ID: 40, TMDBID: tid(604), Title: "Reloaded", PosterAsset: &p}}},
	)

	w := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/movies/603/recommendations", nil)
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)

	var resp dto.MovieRecommendationsResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, domain.TMDBID(603), resp.TMDBID)
	require.Len(t, resp.Items, 1)
	assert.Equal(t, domain.TMDBID(604), resp.Items[0].TMDBID)
	assert.Equal(t, "Reloaded", resp.Items[0].Title)
	require.NotNil(t, resp.Items[0].PosterAsset)
	assert.Equal(t, "/p604.jpg", *resp.Items[0].PosterAsset) // nil resolver → raw path
	assert.NotNil(t, resp.Degraded)                          // never null in JSON
}

func TestMovieRecommendationsHandler_NotFound(t *testing.T) {
	r := newRecsRouter(stubCanon{err: ports.ErrNotFound}, stubRecs{}, stubBatch{})
	w := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/movies/999/recommendations", nil)
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestMovieRecommendationsHandler_BadLimit(t *testing.T) {
	r := newRecsRouter(stubCanon{canon: movie.Canon{ID: 1, TMDBID: tid(603)}}, stubRecs{}, stubBatch{})
	w := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/movies/603/recommendations?limit=999", nil)
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}
