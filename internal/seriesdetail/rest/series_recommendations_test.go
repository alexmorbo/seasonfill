package rest

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/internal/catalog/domain/series"
	seriesdetail "github.com/alexmorbo/seasonfill/internal/seriesdetail/app"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
	"github.com/alexmorbo/seasonfill/internal/shared/http/dto"
	"github.com/alexmorbo/seasonfill/internal/shared/http/middleware"
)

// Reuses newComposerForHandlerTest + i64p from series_detail_test.go
// (same package).

func TestSeriesRecommendationsHandler_Get_200_Empty(t *testing.T) {
	t.Parallel()
	gin.SetMode(gin.TestMode)
	composer := newComposerForHandlerTest(
		series.Canon{ID: 42, Title: "Source"},
		map[string]series.CacheEntry{
			"alpha|1": {InstanceName: "alpha", SonarrSeriesID: 1, SeriesID: i64p(42), Title: "Source"},
		},
	)
	h := NewSeriesRecommendationsHandler(composer, slog.New(slog.NewTextHandler(io.Discard, nil)))
	r := gin.New()
	r.GET("/api/v1/instances/:name/series/:id/recommendations", h.Get)

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/v1/instances/alpha/series/1/recommendations", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	var body dto.SeriesRecommendationsResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	require.Equal(t, domain.InstanceName("alpha"), body.Instance)
	require.Equal(t, domain.SonarrSeriesID(1), body.SonarrSeriesID)
	require.Equal(t, 20, body.Limit, "default limit 20")
	require.Equal(t, 0, body.Offset)
	require.False(t, body.HasMore)
	require.NotNil(t, body.Items, "items slice must never be nil")
	require.Equal(t, 0, len(body.Items))
	require.NotNil(t, body.Degraded)
}

func TestSeriesRecommendationsHandler_Get_400_InvalidID(t *testing.T) {
	t.Parallel()
	gin.SetMode(gin.TestMode)
	composer := newComposerForHandlerTest(series.Canon{ID: 42}, map[string]series.CacheEntry{})
	h := NewSeriesRecommendationsHandler(composer, slog.New(slog.NewTextHandler(io.Discard, nil)))
	r := gin.New()
	r.GET("/api/v1/instances/:name/series/:id/recommendations", h.Get)

	for _, id := range []string{"0", "-3", "xyz"} {
		req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/v1/instances/alpha/series/"+id+"/recommendations", nil)
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		require.Equalf(t, http.StatusBadRequest, rec.Code, "id=%q", id)
	}
}

func TestSeriesRecommendationsHandler_Get_400_InvalidLimit(t *testing.T) {
	t.Parallel()
	gin.SetMode(gin.TestMode)
	composer := newComposerForHandlerTest(
		series.Canon{ID: 42},
		map[string]series.CacheEntry{"alpha|1": {InstanceName: "alpha", SonarrSeriesID: 1, SeriesID: i64p(42)}},
	)
	h := NewSeriesRecommendationsHandler(composer, slog.New(slog.NewTextHandler(io.Discard, nil)))
	r := gin.New()
	r.GET("/api/v1/instances/:name/series/:id/recommendations", h.Get)

	for _, q := range []string{"limit=0", "limit=-1", "limit=51", "limit=abc"} {
		req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/v1/instances/alpha/series/1/recommendations?"+q, nil)
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		require.Equalf(t, http.StatusBadRequest, rec.Code, "q=%q", q)
	}
}

func TestSeriesRecommendationsHandler_Get_400_InvalidOffset(t *testing.T) {
	t.Parallel()
	gin.SetMode(gin.TestMode)
	composer := newComposerForHandlerTest(
		series.Canon{ID: 42},
		map[string]series.CacheEntry{"alpha|1": {InstanceName: "alpha", SonarrSeriesID: 1, SeriesID: i64p(42)}},
	)
	h := NewSeriesRecommendationsHandler(composer, slog.New(slog.NewTextHandler(io.Discard, nil)))
	r := gin.New()
	r.GET("/api/v1/instances/:name/series/:id/recommendations", h.Get)

	for _, q := range []string{"offset=-1", "offset=abc"} {
		req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/v1/instances/alpha/series/1/recommendations?"+q, nil)
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		require.Equalf(t, http.StatusBadRequest, rec.Code, "q=%q", q)
	}
}

// TestSeriesRecommendationsHandler_Get_LangQueryPassedToComposer — Story
// 565 (B-recs-lang). Pins that ?lang=ru-RU is captured by the handler
// and threaded to the composer. The composer's SeriesTextsPort receives
// the value, and its localised row overrides canon.Title on the wire.
func TestSeriesRecommendationsHandler_Get_LangQueryPassedToComposer(t *testing.T) {
	t.Parallel()
	gin.SetMode(gin.TestMode)

	title := "Rek Á"
	texts := &spyTextsForLang{
		batch: map[domain.SeriesID]series.SeriesText{
			10: {SeriesID: 10, Language: "ru-RU", Title: &title},
		},
	}

	composer := seriesdetail.NewComposer(seriesdetail.Deps{
		SeriesCache: &fakeCachePort{
			entries: map[string]series.CacheEntry{
				"alpha|1": {InstanceName: "alpha", SonarrSeriesID: 1, SeriesID: i64p(42), Title: "Source"},
			},
			byCanon: map[domain.SeriesID][]series.CacheEntry{},
		},
		SeriesCacheLookup: &fakeCachePort{
			entries: map[string]series.CacheEntry{},
			byCanon: map[domain.SeriesID][]series.CacheEntry{},
		},
		Series: &fakeSeriesPort{rows: map[domain.SeriesID]series.Canon{
			42: {ID: 42, Title: "Source"},
			10: {ID: 10, Title: "Rec A"},
		}},
		SeriesTexts:     texts,
		Seasons:         emptyList{},
		Episodes:        emptyEpisodes{},
		EpisodeStates:   emptyStates{},
		EpisodeTexts:    fakeNoEpTexts{},
		SeriesPeople:    emptyPeople{},
		People:          emptyPeople{},
		Genres:          emptyTaxRefs{},
		Keywords:        emptyKwRefs{},
		Networks:        emptyNetCo{},
		Companies:       emptyCompanies{},
		Videos:          emptyVideos{},
		ContentRatings:  emptyRatings{},
		ExternalIDs:     emptyExtIDs{},
		Recommendations: recsPortFixed{ids: []domain.SeriesID{10}},
		Freshness:       emptyFreshness{},
		SonarrFor: func(_ domain.InstanceName) (seriesdetail.SonarrQueueLister, bool) {
			return fakeSonarrQ{}, true
		},
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		Now:    func() time.Time { return time.Now().UTC() },
	})

	h := NewSeriesRecommendationsHandler(composer, slog.New(slog.NewTextHandler(io.Discard, nil)))
	r := gin.New()
	r.GET("/api/v1/instances/:name/series/:id/recommendations", h.Get)

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/v1/instances/alpha/series/1/recommendations?lang=ru-RU", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	var body dto.SeriesRecommendationsResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	require.Equal(t, 1, len(body.Items))
	require.Equal(t, "Rek Á", body.Items[0].Title, "?lang=ru-RU must localise recommendation card title")
	require.Equal(t, "ru-RU", texts.LastLang(), "SeriesTextsPort must receive lang=ru-RU from ?lang= query")

	// Negative case: no ?lang= → canon title held (default en-US path, no ru-RU seed).
	req = httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/v1/instances/alpha/series/1/recommendations", nil)
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	require.Equal(t, 1, len(body.Items))
	require.Equal(t, "Rec A", body.Items[0].Title, "default lang path must keep canon title")
	require.Equal(t, "en-US", texts.LastLang(), "empty ?lang= must resolve to en-US downstream")
}

// spyTextsForLang implements seriesdetail.SeriesTextsPort and records
// the last lang argument it received via ListByIDsWithFallback.
type spyTextsForLang struct {
	batch    map[domain.SeriesID]series.SeriesText
	lastLang atomic.Value
}

func (s *spyTextsForLang) GetWithFallback(_ context.Context, _ domain.SeriesID, _ string) (series.SeriesText, error) {
	return series.SeriesText{}, nil
}

func (s *spyTextsForLang) ListByIDsWithFallback(_ context.Context, ids []domain.SeriesID, lang string) (map[domain.SeriesID]series.SeriesText, error) {
	s.lastLang.Store(lang)
	out := make(map[domain.SeriesID]series.SeriesText, len(ids))
	if lang == "ru-RU" {
		for _, id := range ids {
			if t, ok := s.batch[id]; ok {
				out[id] = t
			}
		}
	}
	return out, nil
}

func (s *spyTextsForLang) LastLang() string {
	v := s.lastLang.Load()
	if v == nil {
		return ""
	}
	return v.(string)
}

// recsPortFixed satisfies RecommendationsPort with a canned list.
type recsPortFixed struct{ ids []domain.SeriesID }

func (r recsPortFixed) ListBySeries(_ context.Context, _ domain.SeriesID) ([]domain.SeriesID, error) {
	return r.ids, nil
}

func TestSeriesRecommendationsHandler_Get_404_PropagatesComposerError(t *testing.T) {
	t.Parallel()
	gin.SetMode(gin.TestMode)
	composer := newComposerForHandlerTest(
		series.Canon{ID: 42},
		map[string]series.CacheEntry{}, // no cache row → composer returns ports.ErrNotFound
	)
	h := NewSeriesRecommendationsHandler(composer, slog.New(slog.NewTextHandler(io.Discard, nil)))
	r := gin.New()
	r.Use(middleware.ErrorResponseMiddleware(slog.New(slog.NewTextHandler(io.Discard, nil))))
	r.GET("/api/v1/instances/:name/series/:id/recommendations", h.Get)

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/v1/instances/alpha/series/999/recommendations", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusNotFound, rec.Code)
}
