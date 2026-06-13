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
	"github.com/alexmorbo/seasonfill/domain/enrichment"
	"github.com/alexmorbo/seasonfill/domain/people"
	"github.com/alexmorbo/seasonfill/domain/series"
	"github.com/alexmorbo/seasonfill/domain/taxonomy"
	"github.com/alexmorbo/seasonfill/infrastructure/database"
	"github.com/alexmorbo/seasonfill/infrastructure/sonarr"
	"github.com/alexmorbo/seasonfill/interface/http/dto"
)

// --- minimal port fakes (mirror composer_test.go but inline) ---

type fakeCachePort struct {
	entries map[string]series.CacheEntry
	byCanon map[int64][]series.CacheEntry
}

func (f *fakeCachePort) Get(_ context.Context, instance string, sonarrID int) (series.CacheEntry, error) {
	k := instance + "|" + itoa(sonarrID)
	e, ok := f.entries[k]
	if !ok {
		return series.CacheEntry{}, ports.ErrNotFound
	}
	return e, nil
}

func (f *fakeCachePort) ListBySeriesID(_ context.Context, id int64) ([]series.CacheEntry, error) {
	return f.byCanon[id], nil
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	out := ""
	for n > 0 {
		out = string(rune('0'+n%10)) + out
		n /= 10
	}
	if neg {
		out = "-" + out
	}
	return out
}

type fakeSeriesPort struct{ rows map[int64]series.Canon }

func (f *fakeSeriesPort) Get(_ context.Context, id int64) (series.Canon, error) {
	c, ok := f.rows[id]
	if !ok {
		return series.Canon{}, ports.ErrNotFound
	}
	return c, nil
}

func (f *fakeSeriesPort) GetByTMDBID(_ context.Context, tmdbID int) (series.Canon, error) {
	for _, c := range f.rows {
		if c.TMDBID != nil && *c.TMDBID == tmdbID {
			return c, nil
		}
	}
	return series.Canon{}, ports.ErrNotFound
}

type fakeNoTexts struct{}

func (fakeNoTexts) GetWithFallback(_ context.Context, _ int64, _ string) (series.SeriesText, error) {
	return series.SeriesText{}, ports.ErrNotFound
}

type fakeNoEpTexts struct{}

func (fakeNoEpTexts) GetWithFallback(_ context.Context, _ int64, _ string) (series.EpisodeText, error) {
	return series.EpisodeText{}, ports.ErrNotFound
}

type emptyList struct{}

func (emptyList) ListBySeries(_ context.Context, _ int64) ([]series.CanonSeason, error) {
	return nil, nil
}

type emptyEpisodes struct{}

func (emptyEpisodes) ListBySeries(_ context.Context, _ int64) ([]series.CanonEpisode, error) {
	return nil, nil
}

type emptyStates struct{}

func (emptyStates) ListBySeries(_ context.Context, _ string, _ int64) ([]series.EpisodeState, error) {
	return nil, nil
}

type emptyPeople struct{}

func (emptyPeople) ListBySeries(_ context.Context, _ int64, _ people.SeriesCreditKind) ([]people.SeriesCredit, error) {
	return nil, nil
}
func (emptyPeople) ListByIDs(_ context.Context, _ []int64) ([]people.Person, error) {
	return nil, nil
}

type emptyTaxRefs struct{}

func (emptyTaxRefs) ListBySeries(_ context.Context, _ int64) ([]int64, error) { return nil, nil }
func (emptyTaxRefs) Get(_ context.Context, id int64, lang string) (taxonomy.Genre, error) {
	return taxonomy.Genre{ID: id, Language: lang}, nil
}

type emptyKwRefs struct{}

func (emptyKwRefs) ListBySeries(_ context.Context, _ int64) ([]int64, error) { return nil, nil }
func (emptyKwRefs) Get(_ context.Context, id int64, lang string) (taxonomy.Keyword, error) {
	return taxonomy.Keyword{ID: id, Language: lang}, nil
}

type emptyNetCo struct{}

func (emptyNetCo) ListBySeries(_ context.Context, _ int64) ([]int64, error) { return nil, nil }
func (emptyNetCo) ListByIDs(_ context.Context, _ []int64) ([]taxonomy.Network, error) {
	return nil, nil
}

type emptyCompanies struct{}

func (emptyCompanies) ListBySeries(_ context.Context, _ int64) ([]int64, error) { return nil, nil }
func (emptyCompanies) ListByIDs(_ context.Context, _ []int64) ([]taxonomy.ProductionCompany, error) {
	return nil, nil
}

type emptyVideos struct{}

func (emptyVideos) ListBySeriesAndType(_ context.Context, _ int64, _ string) ([]database.VideoModel, error) {
	return nil, nil
}

type emptyRatings struct{}

func (emptyRatings) ListBySeries(_ context.Context, _ int64) ([]database.ContentRatingModel, error) {
	return nil, nil
}

type emptyExtIDs struct{}

func (emptyExtIDs) ListByEntity(_ context.Context, _ enrichment.EntityType, _ int64) ([]database.ExternalIDModel, error) {
	return nil, nil
}

type emptyRecs struct{}

func (emptyRecs) ListBySeries(_ context.Context, _ int64) ([]int64, error) { return nil, nil }

type emptySyncLog struct{}

func (emptySyncLog) GetLastSync(_ context.Context, _ enrichment.EntityType, _ int64, _ enrichment.Source) (enrichment.SyncLog, error) {
	return enrichment.SyncLog{}, ports.ErrNotFound
}

func i64p(v int64) *int64 { return &v }

func newComposerForHandlerTest(canon series.Canon, cacheEntries map[string]series.CacheEntry) *seriesdetail.Composer {
	return seriesdetail.NewComposer(seriesdetail.Deps{
		SeriesCache:       &fakeCachePort{entries: cacheEntries, byCanon: map[int64][]series.CacheEntry{}},
		SeriesCacheLookup: &fakeCachePort{entries: cacheEntries, byCanon: map[int64][]series.CacheEntry{}},
		Series:            &fakeSeriesPort{rows: map[int64]series.Canon{canon.ID: canon}},
		SeriesTexts:       fakeNoTexts{},
		Seasons:           emptyList{},
		Episodes:          emptyEpisodes{},
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
		SonarrFor: func(_ string) (seriesdetail.SonarrQueueLister, bool) {
			return fakeSonarrQ{}, true
		},
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		Now:    func() time.Time { return time.Now().UTC() },
	})
}

type fakeSonarrQ struct{}

func (fakeSonarrQ) Queue(_ context.Context, _ int) (sonarr.QueuePayload, error) {
	return sonarr.QueuePayload{}, nil
}

// --- tests ---

func TestSeriesDetailHandler_Get_200(t *testing.T) {
	gin.SetMode(gin.TestMode)
	composer := newComposerForHandlerTest(
		series.Canon{ID: 42, Title: "Breaking Bad"},
		map[string]series.CacheEntry{
			"alpha|1": {InstanceName: "alpha", SonarrSeriesID: 1, SeriesID: i64p(42), Title: "Breaking Bad"},
		},
	)
	h := NewSeriesDetailHandler(composer, slog.New(slog.NewTextHandler(io.Discard, nil)))
	r := gin.New()
	r.GET("/api/v1/instances/:name/series/:id", h.Get)

	rec := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/v1/instances/alpha/series/1?lang=en-US", nil)
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	var body dto.SeriesDetailResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	require.Equal(t, "alpha", body.Instance)
	require.Equal(t, 1, body.SonarrSeriesID)
	require.Equal(t, int64(42), body.SeriesID)
	require.Equal(t, "en-US", body.Lang)
	require.Equal(t, "Breaking Bad", body.Hero.Title)
	require.True(t, body.Torrents.SyncPending)
}

func TestSeriesDetailHandler_Get_400_BadID(t *testing.T) {
	gin.SetMode(gin.TestMode)
	composer := newComposerForHandlerTest(
		series.Canon{ID: 42},
		map[string]series.CacheEntry{},
	)
	h := NewSeriesDetailHandler(composer, slog.New(slog.NewTextHandler(io.Discard, nil)))
	r := gin.New()
	r.GET("/api/v1/instances/:name/series/:id", h.Get)

	rec := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/v1/instances/alpha/series/abc", nil)
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestSeriesDetailHandler_Get_404_Unknown(t *testing.T) {
	gin.SetMode(gin.TestMode)
	composer := newComposerForHandlerTest(
		series.Canon{ID: 42},
		map[string]series.CacheEntry{},
	)
	h := NewSeriesDetailHandler(composer, slog.New(slog.NewTextHandler(io.Discard, nil)))
	r := gin.New()
	r.GET("/api/v1/instances/:name/series/:id", h.Get)

	rec := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/v1/instances/alpha/series/999", nil)
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusNotFound, rec.Code)
}

func TestSeriesDetailHandler_StatusPillMapping(t *testing.T) {
	cases := []struct {
		status       string
		inProduction bool
		want         string
	}{
		{"Ended", false, "ended"},
		{"Canceled", false, "canceled"},
		{"Returning Series", false, "continuing"},
		{"Continuing", false, "continuing"},
		{"In Production", false, "in_production"},
		{"Upcoming", false, "upcoming"},
		{"", true, "in_production"},
		{"", false, "unknown"},
	}
	for _, tc := range cases {
		got := mapStatusPill(&tc.status, tc.inProduction)
		require.Equalf(t, tc.want, got, "status=%q in_production=%v", tc.status, tc.inProduction)
	}
}
