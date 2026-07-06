package rest_test

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

	seriesdetailrest "github.com/alexmorbo/seasonfill/internal/seriesdetail/rest"
	ports "github.com/alexmorbo/seasonfill/internal/shared/dataports"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
	"github.com/alexmorbo/seasonfill/internal/shared/http/dto"
)

type stubRatingsUC struct {
	resp  *dto.SeriesRatingsResponse
	err   error
	gotID domain.SeriesID
}

func (s *stubRatingsUC) GetRatings(_ context.Context, id domain.SeriesID) (*dto.SeriesRatingsResponse, error) {
	s.gotID = id
	return s.resp, s.err
}

func newRatingsCtx(t *testing.T, idParam string) (*gin.Context, *httptest.ResponseRecorder) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/v1/series/"+idParam+"/ratings", nil)
	c.Params = gin.Params{{Key: "id", Value: idParam}}
	return c, w
}

func TestRatingsHandler_InvalidID_400(t *testing.T) {
	h := seriesdetailrest.NewGlobalSeriesRatingsHandler(&stubRatingsUC{}, nil)
	c, w := newRatingsCtx(t, "abc")
	h.Get(c)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestRatingsHandler_UnknownSeries_404(t *testing.T) {
	h := seriesdetailrest.NewGlobalSeriesRatingsHandler(&stubRatingsUC{err: ports.ErrNotFound}, nil)
	c, w := newRatingsCtx(t, "42")
	h.Get(c)
	assert.Equal(t, http.StatusNotFound, w.Code)
	body, _ := io.ReadAll(w.Body)
	assert.Contains(t, string(body), "series_not_found")
}

func TestRatingsHandler_OK_200_PassesThroughStatuses(t *testing.T) {
	want := &dto.SeriesRatingsResponse{
		TMDBRating: new(8.4),
		Sources:    dto.SeriesRatingsSources{TMDB: dto.RatingStatusFresh, OMDb: dto.RatingStatusRevalidating},
	}
	stub := &stubRatingsUC{resp: want}
	h := seriesdetailrest.NewGlobalSeriesRatingsHandler(stub, nil)
	c, w := newRatingsCtx(t, "42")
	h.Get(c)
	require.Equal(t, http.StatusOK, w.Code)
	assert.EqualValues(t, 42, stub.gotID)
	var got dto.SeriesRatingsResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &got))
	assert.Equal(t, dto.RatingStatusFresh, got.Sources.TMDB)
	assert.Equal(t, dto.RatingStatusRevalidating, got.Sources.OMDb)
	require.NotNil(t, got.TMDBRating)
	assert.Equal(t, 8.4, *got.TMDBRating)
}
