package handlers

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
	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/application/ports"
	"github.com/alexmorbo/seasonfill/application/seriesdetail"
	"github.com/alexmorbo/seasonfill/domain/series"
	"github.com/alexmorbo/seasonfill/infrastructure/sonarr"
	"github.com/alexmorbo/seasonfill/interface/http/dto"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
)

// twoSeasons returns a SeasonsPort fake yielding two seasons.
type twoSeasons struct{}

func (twoSeasons) ListBySeries(_ context.Context, _ domain.SeriesID) ([]series.CanonSeason, error) {
	return []series.CanonSeason{
		{ID: 1, SeriesID: 42, SeasonNumber: 1},
		{ID: 2, SeriesID: 42, SeasonNumber: 2},
	}, nil
}

type twoEpisodes struct{}

func (twoEpisodes) ListBySeries(_ context.Context, _ domain.SeriesID) ([]series.CanonEpisode, error) {
	return []series.CanonEpisode{
		{ID: 10, SeriesID: 42, SeasonNumber: 1, EpisodeNumber: 1},
		{ID: 11, SeriesID: 42, SeasonNumber: 2, EpisodeNumber: 1},
	}, nil
}

func newSeasonComposer() *seriesdetail.Composer {
	cache := &fakeCachePort{
		entries: map[string]series.CacheEntry{
			"alpha|1": {InstanceName: "alpha", SonarrSeriesID: 1, SeriesID: i64p(42), Title: "X"},
		},
		byCanon: map[domain.SeriesID][]series.CacheEntry{},
	}
	return seriesdetail.NewComposer(seriesdetail.Deps{
		SeriesCache:       cache,
		SeriesCacheLookup: cache,
		Series:            &fakeSeriesPort{rows: map[domain.SeriesID]series.Canon{42: {ID: 42, Title: "X"}}},
		SeriesTexts:       fakeNoTexts{},
		Seasons:           twoSeasons{},
		Episodes:          twoEpisodes{},
		EpisodeStates:     emptyStates{},
		EpisodeTexts:      fakeNoEpTexts{},
		SeriesPeople:      emptyPeople{},
		People:            emptyPeople{},
		Genres:            emptyTaxRefs{},
		Keywords:          emptyKwRefs{},
		Networks:          emptyNetCo{},
		Companies:         emptyCompanies{},
		Videos:            emptyVideos{},
		ContentRatings:    emptyRatings{},
		ExternalIDs:       emptyExtIDs{},
		Recommendations:   emptyRecs{},
		SyncLog:           emptySyncLog{},
		SonarrFor: func(_ domain.InstanceName) (seriesdetail.SonarrQueueLister, bool) {
			return fakeSonarrQ2{}, true
		},
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		Now:    func() time.Time { return time.Now().UTC() },
	})
}

type fakeSonarrQ2 struct{}

func (fakeSonarrQ2) Queue(_ context.Context, _ domain.SonarrSeriesID) (sonarr.QueuePayload, error) {
	return sonarr.QueuePayload{}, nil
}

func TestSeriesSeasonHandler_Get_200(t *testing.T) {
	t.Parallel()
	gin.SetMode(gin.TestMode)
	h := NewSeriesSeasonHandler(newSeasonComposer(), slog.New(slog.NewTextHandler(io.Discard, nil)))
	r := gin.New()
	r.GET("/api/v1/instances/:name/series/:id/season/:n", h.Get)

	rec := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/v1/instances/alpha/series/1/season/2", nil)
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	var body dto.SeasonDetailResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	require.Equal(t, 2, body.Season.SeasonNumber)
	require.Len(t, body.Season.Episodes, 1)
}

func TestSeriesSeasonHandler_Get_404_UnknownSeason(t *testing.T) {
	t.Parallel()
	gin.SetMode(gin.TestMode)
	h := NewSeriesSeasonHandler(newSeasonComposer(), slog.New(slog.NewTextHandler(io.Discard, nil)))
	r := gin.New()
	r.GET("/api/v1/instances/:name/series/:id/season/:n", h.Get)

	rec := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/v1/instances/alpha/series/1/season/99", nil)
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusNotFound, rec.Code)
}

func TestSeriesSeasonHandler_Get_400_BadInputs(t *testing.T) {
	t.Parallel()
	gin.SetMode(gin.TestMode)
	h := NewSeriesSeasonHandler(newSeasonComposer(), slog.New(slog.NewTextHandler(io.Discard, nil)))
	r := gin.New()
	r.GET("/api/v1/instances/:name/series/:id/season/:n", h.Get)

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/v1/instances/alpha/series/abc/season/1", nil))
	require.Equal(t, http.StatusBadRequest, rec.Code)

	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/v1/instances/alpha/series/1/season/-2", nil))
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

var _ = ports.ErrNotFound
