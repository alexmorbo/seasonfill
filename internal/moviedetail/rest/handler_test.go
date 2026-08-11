package rest

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
	enrichpersistence "github.com/alexmorbo/seasonfill/internal/enrichment/persistence"
	mdapp "github.com/alexmorbo/seasonfill/internal/moviedetail/app"
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

type stubI18n struct{}

func (stubI18n) Get(_ context.Context, _ domain.MovieID, _ string) (enrichpersistence.MovieI18nRow, error) {
	return enrichpersistence.MovieI18nRow{}, ports.ErrNotFound
}

type stubCollection struct{}

func (stubCollection) GetByTMDBCollectionID(_ context.Context, _ int) (movie.CollectionCanon, error) {
	return movie.CollectionCanon{}, ports.ErrNotFound
}

type stubMembership struct{ states []movie.StateEntry }

func (s stubMembership) ListActiveByMovieID(_ context.Context, _ domain.MovieID) ([]movie.StateEntry, error) {
	return s.states, nil
}

func newTestHandler(canon movie.Canon, canonErr error, states []movie.StateEntry) *Handler {
	uc := mdapp.New(
		stubCanon{canon: canon, err: canonErr},
		stubI18n{},
		stubCollection{},
		stubMembership{states: states},
	)
	return NewHandler(uc, nil)
}

func doGet(h *Handler, param string) *httptest.ResponseRecorder {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/movies/"+param+"?lang=ru-RU", nil)
	c.Params = gin.Params{{Key: "tmdb_id", Value: param}}
	h.Get(c)
	return w
}

func TestHandler_Get_OK(t *testing.T) {
	t.Parallel()
	tid := domain.TMDBID(693134)
	avail := "released"
	canon := movie.Canon{
		ID: domain.MovieID(42), TMDBID: &tid, Title: "Dune: Part Two",
		PosterAsset: new("/p.jpg"),
	}
	h := newTestHandler(canon, nil, []movie.StateEntry{
		{InstanceName: "radarr-alpha", RadarrMovieID: 7, MovieID: 42, Monitored: true, HasFile: true, Availability: &avail, SizeOnDiskBytes: 5},
	})

	w := doGet(h, "693134")
	require.Equal(t, http.StatusOK, w.Code)

	var body dto.MovieDetailResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.Equal(t, 693134, body.TMDBID)
	assert.Equal(t, "Dune: Part Two", body.Title)
	require.Len(t, body.Library, 1)
	assert.Equal(t, "radarr-alpha", body.Library[0].InstanceName)
	assert.Equal(t, int64(5), body.Library[0].SizeOnDisk)
	assert.Contains(t, body.Degraded, "movie_i18n")
}

func TestHandler_Get_BadRequestNonNumeric(t *testing.T) {
	t.Parallel()
	h := newTestHandler(movie.Canon{}, nil, nil)

	w := doGet(h, "not-a-number")
	require.Equal(t, http.StatusBadRequest, w.Code)

	var body dto.ErrorResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.Equal(t, "BAD_REQUEST", body.Code)
}

func TestHandler_Get_NotFound(t *testing.T) {
	t.Parallel()
	h := newTestHandler(movie.Canon{}, ports.ErrNotFound, nil)

	w := doGet(h, "999999")
	require.Equal(t, http.StatusNotFound, w.Code)

	var body dto.ErrorResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.Equal(t, "MOVIE_NOT_FOUND", body.Code)
}
