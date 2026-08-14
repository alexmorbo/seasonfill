package rest

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/internal/catalog/domain/movie"
	mdapp "github.com/alexmorbo/seasonfill/internal/moviedetail/app"
	ports "github.com/alexmorbo/seasonfill/internal/shared/dataports"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
	"github.com/alexmorbo/seasonfill/internal/shared/http/dto"
)

func buildRatingsRouter(t *testing.T, canon stubCanon) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	uc := mdapp.NewRatingsUseCase(canon)
	h := NewMovieRatingsHandler(uc, nil)
	r := gin.New()
	r.GET("/movies/:tmdb_id/ratings", h.Get)
	return r
}

func doRatingsGet(t *testing.T, r *gin.Engine, url string) (*httptest.ResponseRecorder, dto.MovieRatingsResponse) {
	t.Helper()
	w := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, url, nil)
	r.ServeHTTP(w, req)
	var body dto.MovieRatingsResponse
	if w.Code == http.StatusOK {
		b, _ := io.ReadAll(w.Body)
		require.NoError(t, json.Unmarshal(b, &body))
	}
	return w, body
}

func TestMovieRatingsHandler_HappyPathShape(t *testing.T) {
	tid := domain.TMDBID(603)
	canon := stubCanon{canon: movie.Canon{
		ID: 7, TMDBID: &tid, Title: "The Matrix",
		TMDBRating: new(8.2), TMDBVotes: new(24000),
		IMDBRating: new(8.7), IMDBVotes: new(1900000),
		OMDBRated: new("R"), OMDBAwards: new("Won 4 Oscars."),
	}}
	w, body := doRatingsGet(t, buildRatingsRouter(t, canon), "/movies/603/ratings")
	require.Equal(t, http.StatusOK, w.Code)
	require.NotNil(t, body.TMDBRating)
	assert.InDelta(t, 8.2, *body.TMDBRating, 1e-9)
	require.NotNil(t, body.IMDBRating)
	assert.InDelta(t, 8.7, *body.IMDBRating, 1e-9)
	require.NotNil(t, body.Rated)
	assert.Equal(t, "R", *body.Rated)
	require.NotNil(t, body.Awards)
	assert.Equal(t, "Won 4 Oscars.", *body.Awards)
	assert.Equal(t, dto.RatingStatusFresh, body.Sources.TMDB)
	assert.Equal(t, dto.RatingStatusFresh, body.Sources.OMDb)
}

func TestMovieRatingsHandler_AbsentSourcesUnavailable(t *testing.T) {
	tid := domain.TMDBID(603)
	canon := stubCanon{canon: movie.Canon{ID: 7, TMDBID: &tid, Title: "Canon Title"}}
	w, body := doRatingsGet(t, buildRatingsRouter(t, canon), "/movies/603/ratings")
	require.Equal(t, http.StatusOK, w.Code)
	assert.Nil(t, body.TMDBRating)
	assert.Nil(t, body.IMDBRating)
	assert.Equal(t, dto.RatingStatusUnavailable, body.Sources.TMDB)
	assert.Equal(t, dto.RatingStatusUnavailable, body.Sources.OMDb)
}

func TestMovieRatingsHandler_MixedPresence(t *testing.T) {
	tid := domain.TMDBID(603)
	canon := stubCanon{canon: movie.Canon{
		ID: 7, TMDBID: &tid, Title: "The Matrix",
		TMDBRating: new(8.2), TMDBVotes: new(24000),
	}}
	_, body := doRatingsGet(t, buildRatingsRouter(t, canon), "/movies/603/ratings")
	assert.Equal(t, dto.RatingStatusFresh, body.Sources.TMDB)
	assert.Equal(t, dto.RatingStatusUnavailable, body.Sources.OMDb)
	assert.Nil(t, body.IMDBRating)
}

func TestMovieRatingsHandler_BadID(t *testing.T) {
	canon := stubCanon{canon: movie.Canon{}}
	w, _ := doRatingsGet(t, buildRatingsRouter(t, canon), "/movies/0/ratings")
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestMovieRatingsHandler_NotFound(t *testing.T) {
	canon := stubCanon{err: ports.ErrNotFound}
	w, _ := doRatingsGet(t, buildRatingsRouter(t, canon), "/movies/603/ratings")
	assert.Equal(t, http.StatusNotFound, w.Code)
}
