package rest

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	seriesdetail "github.com/alexmorbo/seasonfill/internal/seriesdetail/app"
	ports "github.com/alexmorbo/seasonfill/internal/shared/dataports"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
	sharedErrors "github.com/alexmorbo/seasonfill/internal/shared/errors"
	"github.com/alexmorbo/seasonfill/internal/shared/http/dto"
	"github.com/alexmorbo/seasonfill/internal/shared/http/middleware"
)

// stubSeasonsComposer implements SeasonsComposerPort and records the lang.
type stubSeasonsComposer struct {
	out      seriesdetail.SeasonsListDTO
	err      error
	gotLang  string
	gotID    domain.SeriesID
	calloted bool
}

func (s *stubSeasonsComposer) Compose(_ context.Context, seriesID domain.SeriesID, lang string) (seriesdetail.SeasonsListDTO, error) {
	s.calloted = true
	s.gotLang = lang
	s.gotID = seriesID
	return s.out, s.err
}

func seasonsQuietLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestSeasonsHandler_Get_400_InvalidID(t *testing.T) {
	t.Parallel()
	gin.SetMode(gin.TestMode)
	for _, id := range []string{"abc", "0", "-3"} {
		stub := &stubSeasonsComposer{}
		h := NewSeasonsHandler(stub, seasonsQuietLogger())
		r := gin.New()
		r.GET("/api/v1/series/:id/seasons", h.Get)

		req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/v1/series/"+id+"/seasons", nil)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		assert.Equalf(t, http.StatusBadRequest, w.Code, "id=%q", id)
		assert.Contains(t, w.Body.String(), "invalid series id")
		assert.Falsef(t, stub.calloted, "composer must not be called for id=%q", id)
	}
}

func TestSeasonsHandler_Get_404_NotFound(t *testing.T) {
	t.Parallel()
	gin.SetMode(gin.TestMode)
	stub := &stubSeasonsComposer{
		err: errors.Join(&sharedErrors.SeriesNotFoundError{ID: 9999}, ports.ErrNotFound),
	}
	h := NewSeasonsHandler(stub, seasonsQuietLogger())
	r := gin.New()
	r.GET("/api/v1/series/:id/seasons", h.Get)

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/v1/series/9999/seasons", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
	assert.Contains(t, w.Body.String(), `"series_not_found"`)
}

func TestSeasonsHandler_Get_200(t *testing.T) {
	t.Parallel()
	gin.SetMode(gin.TestMode)
	end := time.Date(2024, 3, 1, 0, 0, 0, 0, time.UTC)
	stub := &stubSeasonsComposer{out: seriesdetail.SeasonsListDTO{
		SeriesID: 42,
		SyncedAt: time.Date(2026, 6, 30, 0, 0, 0, 0, time.UTC),
		Seasons: []seriesdetail.SeasonSummary{
			{SeasonNumber: 1, Name: "Сезон 1", AirDateEnd: &end, EpisodeCount: 10},
		},
	}}
	h := NewSeasonsHandler(stub, seasonsQuietLogger())
	r := gin.New()
	r.GET("/api/v1/series/:id/seasons", h.Get)

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/v1/series/42/seasons?lang=ru-RU", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "ru-RU", stub.gotLang, "lang forwarded verbatim")
	assert.Equal(t, domain.SeriesID(42), stub.gotID)

	var body dto.SeriesSeasonsResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.Equal(t, domain.SeriesID(42), body.SeriesID)
	require.Len(t, body.Seasons, 1)
	assert.Equal(t, 1, body.Seasons[0].SeasonNumber)
	assert.Equal(t, "Сезон 1", body.Seasons[0].Name)
	require.NotNil(t, body.Seasons[0].AirDateEnd)
	assert.True(t, body.Seasons[0].AirDateEnd.Equal(end))
	assert.False(t, body.SyncedAt.IsZero())
}

func TestSeasonsHandler_Get_500_BareError(t *testing.T) {
	t.Parallel()
	gin.SetMode(gin.TestMode)
	stub := &stubSeasonsComposer{err: errors.New("boom")} //nolint:err113
	h := NewSeasonsHandler(stub, seasonsQuietLogger())
	r := gin.New()
	r.Use(middleware.ErrorResponseMiddleware(seasonsQuietLogger()))
	r.GET("/api/v1/series/:id/seasons", h.Get)

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/v1/series/42/seasons", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
}
