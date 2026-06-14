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
	"github.com/alexmorbo/seasonfill/domain/people"
	"github.com/alexmorbo/seasonfill/domain/series"
	"github.com/alexmorbo/seasonfill/interface/http/dto"
)

// --- minimal fakes for the cast composer (inline) ---

type castFakePeoplePort struct {
	rows map[int64]people.Person
}

func (f castFakePeoplePort) ListByIDs(_ context.Context, ids []int64) ([]people.Person, error) {
	out := make([]people.Person, 0, len(ids))
	for _, id := range ids {
		if p, ok := f.rows[id]; ok {
			out = append(out, p)
		}
	}
	return out, nil
}

type castFakeSeriesPeople struct {
	cast []people.SeriesCredit
	crew []people.SeriesCredit
}

func (f castFakeSeriesPeople) ListBySeries(_ context.Context, _ int64, kind people.SeriesCreditKind) ([]people.SeriesCredit, error) {
	if kind == people.SeriesCreditCast {
		return f.cast, nil
	}
	return f.crew, nil
}

type castFakePersonCredits struct {
	rows map[int64][]seriesdetail.PersonCreditRef
}

func (f castFakePersonCredits) ListByPerson(_ context.Context, personID int64) ([]seriesdetail.PersonCreditRef, error) {
	return f.rows[personID], nil
}

type castFakeEpisodesCount struct {
	count int
}

func (f castFakeEpisodesCount) CountBySeries(_ context.Context, _ int64) (int, error) {
	return f.count, nil
}

// castHandlerTestMediaLookup is a passthrough media-hash lookup for handler
// tests: it echoes the raw path back as the hash so existing assertions
// (story 312) on the wire field continue to hold. Production wires the real
// repo; tests that don't care about hash semantics get an identity mapping.
type castHandlerTestMediaLookup struct{}

func (castHandlerTestMediaLookup) EnsurePending(_ context.Context, _, _, _ string) error {
	return nil
}

func (castHandlerTestMediaLookup) HashForSourceURL(_ context.Context, url string) (string, error) {
	// URL shape: https://image.tmdb.org/t/p/<size>/<path>
	// The test seeds canon with PosterAsset = "poster-hash"; the resolver
	// constructs https://image.tmdb.org/t/p/w342/poster-hash. Echo the
	// trailing segment so the existing assertion ("poster-hash") holds.
	const prefix = "https://image.tmdb.org/t/p/"
	if len(url) <= len(prefix) {
		return "", ports.ErrNotFound
	}
	rest := url[len(prefix):]
	// Skip the size token.
	slash := -1
	for i := 0; i < len(rest); i++ {
		if rest[i] == '/' {
			slash = i
			break
		}
	}
	if slash < 0 {
		return "", ports.ErrNotFound
	}
	return rest[slash+1:], nil
}

func newCastComposerForHandlerTest(canon series.Canon, cacheEntries map[string]series.CacheEntry,
	cast []people.SeriesCredit, persons map[int64]people.Person, total int,
) *seriesdetail.CastComposer {
	return seriesdetail.NewCastComposer(seriesdetail.CastDeps{
		SeriesCache:       &fakeCachePort{entries: cacheEntries, byCanon: map[int64][]series.CacheEntry{}},
		SeriesCacheLookup: &fakeCachePort{entries: cacheEntries, byCanon: map[int64][]series.CacheEntry{}},
		Series:            &fakeSeriesPort{rows: map[int64]series.Canon{canon.ID: canon}},
		SeriesPeople:      castFakeSeriesPeople{cast: cast},
		People:            castFakePeoplePort{rows: persons},
		PersonCredits:     castFakePersonCredits{rows: map[int64][]seriesdetail.PersonCreditRef{}},
		EpisodesCount:     castFakeEpisodesCount{count: total},
		Logger:            slog.New(slog.NewTextHandler(io.Discard, nil)),
		Now:               func() time.Time { return time.Now().UTC() },
		MediaResolver:     seriesdetail.NewMediaResolver(castHandlerTestMediaLookup{}, nil, nil, slog.New(slog.NewTextHandler(io.Discard, nil))),
	})
}

func TestSeriesCastHandler_Get_200(t *testing.T) {
	gin.SetMode(gin.TestMode)
	tmdbID := 1001
	character := "Joel Miller"
	order := 0
	episodes := 9
	cast := []people.SeriesCredit{
		{PersonID: 1, Kind: people.SeriesCreditCast, CharacterName: &character, CreditOrder: &order, EpisodeCount: &episodes},
	}
	persons := map[int64]people.Person{
		1: {ID: 1, Name: "Pedro Pascal", TMDBID: &tmdbID},
	}
	poster := "poster-hash"
	status := "Returning Series"
	year := 2023
	lastAir := time.Date(2025, 4, 13, 0, 0, 0, 0, time.UTC)
	composer := newCastComposerForHandlerTest(
		series.Canon{
			ID:          42,
			Title:       "The Last of Us",
			PosterAsset: &poster,
			Status:      &status,
			Year:        &year,
			LastAirDate: &lastAir,
		},
		map[string]series.CacheEntry{
			"alpha|1": {InstanceName: "alpha", SonarrSeriesID: 1, SeriesID: i64p(42)},
		},
		cast, persons, 10,
	)
	h := NewSeriesCastHandler(composer, slog.New(slog.NewTextHandler(io.Discard, nil)))
	r := gin.New()
	r.GET("/api/v1/instances/:name/series/:id/cast", h.Get)

	rec := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/v1/instances/alpha/series/1/cast?lang=en-US", nil)
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	var body dto.SeriesCastResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	require.Equal(t, "alpha", body.Instance)
	require.Equal(t, 1, body.SonarrSeriesID)
	require.Equal(t, int64(42), body.SeriesID)
	require.Equal(t, "en-US", body.Lang)
	require.Equal(t, 10, body.TotalEpisodeCount)
	require.Equal(t, 1, len(body.Cast))
	require.Equal(t, "Pedro Pascal", body.Cast[0].Name)
	require.Equal(t, &tmdbID, body.Cast[0].TMDBID)
	require.False(t, body.Cast[0].InLibrary)
	// in_library boolean must surface even when false (not omitted).
	require.Contains(t, rec.Body.String(), `"in_library":false`)

	// series_summary projection — story 303.
	require.Equal(t, "The Last of Us", body.SeriesSummary.Title)
	require.NotNil(t, body.SeriesSummary.PosterURL)
	require.Equal(t, "poster-hash", *body.SeriesSummary.PosterURL)
	require.Equal(t, "continuing", body.SeriesSummary.Status, "Returning Series → continuing")
	require.NotNil(t, body.SeriesSummary.FirstAiredYear)
	require.Equal(t, 2023, *body.SeriesSummary.FirstAiredYear)
	require.NotNil(t, body.SeriesSummary.LastAiredYear)
	require.Equal(t, 2025, *body.SeriesSummary.LastAiredYear)
}

func TestSeriesCastHandler_Get_400_BadID(t *testing.T) {
	gin.SetMode(gin.TestMode)
	composer := newCastComposerForHandlerTest(
		series.Canon{ID: 42},
		map[string]series.CacheEntry{},
		nil, nil, 0,
	)
	h := NewSeriesCastHandler(composer, slog.New(slog.NewTextHandler(io.Discard, nil)))
	r := gin.New()
	r.GET("/api/v1/instances/:name/series/:id/cast", h.Get)

	rec := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/v1/instances/alpha/series/abc/cast", nil)
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestSeriesCastHandler_Get_404_Unknown(t *testing.T) {
	gin.SetMode(gin.TestMode)
	composer := newCastComposerForHandlerTest(
		series.Canon{ID: 42},
		map[string]series.CacheEntry{},
		nil, nil, 0,
	)
	h := NewSeriesCastHandler(composer, slog.New(slog.NewTextHandler(io.Discard, nil)))
	r := gin.New()
	r.GET("/api/v1/instances/:name/series/:id/cast", h.Get)

	rec := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/v1/instances/alpha/series/999/cast", nil)
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusNotFound, rec.Code)
}

func TestSeriesCastHandler_Get_LangEcho(t *testing.T) {
	gin.SetMode(gin.TestMode)
	composer := newCastComposerForHandlerTest(
		series.Canon{ID: 42, Title: "X"},
		map[string]series.CacheEntry{
			"alpha|1": {InstanceName: "alpha", SonarrSeriesID: 1, SeriesID: i64p(42)},
		},
		nil, nil, 0,
	)
	h := NewSeriesCastHandler(composer, slog.New(slog.NewTextHandler(io.Discard, nil)))
	r := gin.New()
	r.GET("/api/v1/instances/:name/series/:id/cast", h.Get)

	rec := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/v1/instances/alpha/series/1/cast?lang=ru-RU", nil)
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	var body dto.SeriesCastResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	require.Equal(t, "ru-RU", body.Lang)
}

// confirm ports.ErrNotFound import is exercised (assertion above
// is via HTTP status, not the sentinel directly).
var _ = ports.ErrNotFound
