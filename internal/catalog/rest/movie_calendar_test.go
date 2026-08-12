package rest

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/internal/catalog/app/moviecalendar"
	appmedia "github.com/alexmorbo/seasonfill/internal/mediaproxy/app"
	"github.com/alexmorbo/seasonfill/internal/shared/http/dto"
	"github.com/alexmorbo/seasonfill/internal/shared/media"
)

// fakeMovieCalendarRepo returns canned release rows for the calendar usecase.
type fakeMovieCalendarRepo struct {
	rows []moviecalendar.Row
}

func (f *fakeMovieCalendarRepo) Events(_ context.Context, _, _ time.Time) ([]moviecalendar.Row, error) {
	return f.rows, nil
}

// TestMovieCalendarHandler_Get_ResolvesPoster is the M-FIX-1 regression: each
// event's poster must be the resolved sha256 media hash, NOT the raw TMDB path.
func TestMovieCalendarHandler_Get_ResolvesPoster(t *testing.T) {
	const posterHash = "ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff"
	rawPoster := "/2cxhvw.jpg"

	newHandler := func(resolver *media.Resolver) *MovieCalendarHandler {
		repo := &fakeMovieCalendarRepo{rows: []moviecalendar.Row{{
			MovieID: 1, TMDBID: new(int64(438631)), Title: "Dune",
			Poster: &rawPoster, Milestone: "theatrical", Date: time.Now().UTC(),
		}}}
		return NewMovieCalendarHandler(moviecalendar.NewUseCase(repo), resolver, nil)
	}

	serve := func(h *MovieCalendarHandler) *httptest.ResponseRecorder {
		r := gin.New()
		r.GET("/api/v1/movies/calendar", h.Get)
		req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/movies/calendar", nil)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		return w
	}

	firstPoster := func(t *testing.T, w *httptest.ResponseRecorder) string {
		t.Helper()
		require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())
		var body dto.MovieCalendarDTO
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
		require.NotEmpty(t, body.Days)
		require.NotEmpty(t, body.Days[0].Events)
		require.NotNil(t, body.Days[0].Events[0].Poster)
		return *body.Days[0].Events[0].Poster
	}

	t.Run("resolver hit replaces raw path with media hash", func(t *testing.T) {
		lookup := movieMediaLookupStub{byURL: map[string]string{
			appmedia.BuildTMDBImageURL("w342", rawPoster): posterHash,
		}}
		resolver := media.NewResolver(lookup, nil, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
		got := firstPoster(t, serve(newHandler(resolver)))
		assert.Equal(t, posterHash, got)
		assert.NotEqual(t, rawPoster, got, "must NOT emit the raw /xxxx.jpg path")
	})

	t.Run("nil resolver preserves raw path (legacy)", func(t *testing.T) {
		got := firstPoster(t, serve(newHandler(nil)))
		assert.Equal(t, rawPoster, got)
	})
}
