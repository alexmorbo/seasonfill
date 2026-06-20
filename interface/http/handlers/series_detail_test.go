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
	"github.com/alexmorbo/seasonfill/domain/taxonomy"
	"github.com/alexmorbo/seasonfill/infrastructure/database"
	"github.com/alexmorbo/seasonfill/infrastructure/sonarr"
	"github.com/alexmorbo/seasonfill/interface/http/dto"
	"github.com/alexmorbo/seasonfill/interface/http/middleware"
	"github.com/alexmorbo/seasonfill/internal/enrichment/domain/enrichment"
	"github.com/alexmorbo/seasonfill/internal/enrichment/domain/people"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
)

// --- minimal port fakes (mirror composer_test.go but inline) ---

type fakeCachePort struct {
	entries map[string]series.CacheEntry
	byCanon map[domain.SeriesID][]series.CacheEntry
}

func (f *fakeCachePort) Get(_ context.Context, instance domain.InstanceName, sonarrID domain.SonarrSeriesID) (series.CacheEntry, error) {
	k := string(instance) + "|" + itoa(int(sonarrID))
	e, ok := f.entries[k]
	if !ok {
		return series.CacheEntry{}, ports.ErrNotFound
	}
	return e, nil
}

func (f *fakeCachePort) ListBySeriesID(_ context.Context, id domain.SeriesID) ([]series.CacheEntry, error) {
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

type fakeSeriesPort struct {
	rows map[domain.SeriesID]series.Canon
}

func (f *fakeSeriesPort) Get(_ context.Context, id domain.SeriesID) (series.Canon, error) {
	c, ok := f.rows[id]
	if !ok {
		return series.Canon{}, ports.ErrNotFound
	}
	return c, nil
}

func (f *fakeSeriesPort) GetByTMDBID(_ context.Context, tmdbID domain.TMDBID) (series.Canon, error) {
	for _, c := range f.rows {
		if c.TMDBID != nil && *c.TMDBID == tmdbID {
			return c, nil
		}
	}
	return series.Canon{}, ports.ErrNotFound
}

type fakeNoTexts struct{}

func (fakeNoTexts) GetWithFallback(_ context.Context, _ domain.SeriesID, _ string) (series.SeriesText, error) {
	return series.SeriesText{}, ports.ErrNotFound
}

type fakeNoEpTexts struct{}

func (fakeNoEpTexts) GetWithFallback(_ context.Context, _ domain.EpisodeID, _ string) (series.EpisodeText, error) {
	return series.EpisodeText{}, ports.ErrNotFound
}

type emptyList struct{}

func (emptyList) ListBySeries(_ context.Context, _ domain.SeriesID) ([]series.CanonSeason, error) {
	return nil, nil
}

type emptyEpisodes struct{}

func (emptyEpisodes) ListBySeries(_ context.Context, _ domain.SeriesID) ([]series.CanonEpisode, error) {
	return nil, nil
}

type emptyStates struct{}

func (emptyStates) ListBySeries(_ context.Context, _ domain.InstanceName, _ domain.SeriesID) ([]series.EpisodeState, error) {
	return nil, nil
}

type emptyPeople struct{}

func (emptyPeople) ListBySeries(_ context.Context, _ domain.SeriesID, _ people.SeriesCreditKind) ([]people.SeriesCredit, error) {
	return nil, nil
}
func (emptyPeople) ListByIDs(_ context.Context, _ []int64) ([]people.Person, error) {
	return nil, nil
}

type emptyTaxRefs struct{}

func (emptyTaxRefs) ListBySeries(_ context.Context, _ domain.SeriesID) ([]int64, error) {
	return nil, nil
}
func (emptyTaxRefs) Get(_ context.Context, id int64, lang string) (taxonomy.Genre, error) {
	return taxonomy.Genre{ID: id, Language: lang}, nil
}

type emptyKwRefs struct{}

func (emptyKwRefs) ListBySeries(_ context.Context, _ domain.SeriesID) ([]int64, error) {
	return nil, nil
}
func (emptyKwRefs) Get(_ context.Context, id int64, lang string) (taxonomy.Keyword, error) {
	return taxonomy.Keyword{ID: id, Language: lang}, nil
}

type emptyNetCo struct{}

func (emptyNetCo) ListBySeries(_ context.Context, _ domain.SeriesID) ([]int64, error) {
	return nil, nil
}
func (emptyNetCo) ListByIDs(_ context.Context, _ []int64) ([]taxonomy.Network, error) {
	return nil, nil
}

type emptyCompanies struct{}

func (emptyCompanies) ListBySeries(_ context.Context, _ domain.SeriesID) ([]int64, error) {
	return nil, nil
}
func (emptyCompanies) ListByIDs(_ context.Context, _ []int64) ([]taxonomy.ProductionCompany, error) {
	return nil, nil
}

type emptyVideos struct{}

func (emptyVideos) ListBySeriesAndType(_ context.Context, _ domain.SeriesID, _ string) ([]database.VideoModel, error) {
	return nil, nil
}

type emptyRatings struct{}

func (emptyRatings) ListBySeries(_ context.Context, _ domain.SeriesID) ([]database.ContentRatingModel, error) {
	return nil, nil
}

type emptyExtIDs struct{}

func (emptyExtIDs) ListByEntity(_ context.Context, _ enrichment.EntityType, _ int64) ([]database.ExternalIDModel, error) {
	return nil, nil
}

type emptyRecs struct{}

func (emptyRecs) ListBySeries(_ context.Context, _ domain.SeriesID) ([]domain.SeriesID, error) {
	return nil, nil
}

type emptySyncLog struct{}

func (emptySyncLog) GetLastSync(_ context.Context, _ enrichment.EntityType, _ int64, _ enrichment.Source) (enrichment.SyncLog, error) {
	return enrichment.SyncLog{}, ports.ErrNotFound
}

func i64p(v int64) *domain.SeriesID { sid := domain.SeriesID(v); return &sid }

func newComposerForHandlerTest(canon series.Canon, cacheEntries map[string]series.CacheEntry) *seriesdetail.Composer {
	return seriesdetail.NewComposer(seriesdetail.Deps{
		SeriesCache:       &fakeCachePort{entries: cacheEntries, byCanon: map[domain.SeriesID][]series.CacheEntry{}},
		SeriesCacheLookup: &fakeCachePort{entries: cacheEntries, byCanon: map[domain.SeriesID][]series.CacheEntry{}},
		Series:            &fakeSeriesPort{rows: map[domain.SeriesID]series.Canon{canon.ID: canon}},
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
		SonarrFor: func(_ domain.InstanceName) (seriesdetail.SonarrQueueLister, bool) {
			return fakeSonarrQ{}, true
		},
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		Now:    func() time.Time { return time.Now().UTC() },
	})
}

type fakeSonarrQ struct{}

func (fakeSonarrQ) Queue(_ context.Context, _ domain.SonarrSeriesID) (sonarr.QueuePayload, error) {
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
	require.Equal(t, domain.InstanceName("alpha"), body.Instance)
	require.Equal(t, domain.SonarrSeriesID(1), body.SonarrSeriesID)
	require.Equal(t, domain.SeriesID(42), body.SeriesID)
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
	// F-2c-1: typed-error middleware so handler c.Error(err) flows
	// through to the JSON envelope writer.
	r.Use(middleware.ErrorResponseMiddleware(slog.New(slog.NewTextHandler(io.Discard, nil))))
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

func TestMapHero_StudioAndCountry(t *testing.T) {
	t.Parallel()

	studio := "Sony Pictures Television"
	country := "US"

	cases := []struct {
		name      string
		companies []taxonomy.ProductionCompany
		origin    *string
		wantStud  *string
		wantCty   *string
	}{
		{
			name:      "both present",
			companies: []taxonomy.ProductionCompany{{ID: 1, Name: studio}, {ID: 2, Name: "Tall Ship"}},
			origin:    &country,
			wantStud:  &studio,
			wantCty:   &country,
		},
		{
			name:      "studio only — no origin country",
			companies: []taxonomy.ProductionCompany{{ID: 1, Name: studio}},
			origin:    nil,
			wantStud:  &studio,
			wantCty:   nil,
		},
		{
			name:      "country only — no companies",
			companies: nil,
			origin:    &country,
			wantStud:  nil,
			wantCty:   &country,
		},
		{
			name:      "neither — both omitted",
			companies: []taxonomy.ProductionCompany{{ID: 1, Name: ""}}, // empty name treated as absent
			origin:    func() *string { s := ""; return &s }(),
			wantStud:  nil,
			wantCty:   nil,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			d := &seriesdetail.Detail{
				Canon:     series.Canon{Title: "X", OriginCountry: tc.origin},
				Companies: tc.companies,
			}
			h := mapHero(d)
			require.Equal(t, tc.wantStud, h.Studio, "studio")
			require.Equal(t, tc.wantCty, h.Country, "country")
		})
	}
}

func TestMapSeasons_PopulatesMediaMeta(t *testing.T) {
	t.Parallel()
	vc, ac, ach, rg := "HEVC", "DDP", "5.1", "RARBG"
	qn := "WEBDL-1080p"
	seasons := []seriesdetail.SeasonDetail{{
		Canon: series.CanonSeason{SeasonNumber: 5},
		Episodes: []seriesdetail.EpisodeDetail{{
			Canon: series.CanonEpisode{EpisodeNumber: 1, SeasonNumber: 5},
			State: &series.EpisodeState{
				HasFile:       true,
				Quality:       &qn,
				VideoCodec:    &vc,
				AudioCodec:    &ac,
				AudioChannels: &ach,
				ReleaseGroup:  &rg,
			},
		}},
	}}
	out := mapSeasons(&seriesdetail.Detail{Seasons: seasons})
	require.Len(t, out, 1)
	require.Len(t, out[0].Episodes, 1)
	ep := out[0].Episodes[0]
	require.Equal(t, &vc, ep.VideoCodec)
	require.Equal(t, &ac, ep.AudioCodec)
	require.Equal(t, &ach, ep.AudioChannels)
	require.Equal(t, &rg, ep.ReleaseGroup)
	require.Equal(t, &qn, ep.Quality)
}

func TestMapHero_PremiereLanguageCountries(t *testing.T) {
	t.Parallel()

	firstAir := time.Date(2026, 5, 28, 0, 0, 0, 0, time.UTC)
	lang := "en"
	countries := []string{"US", "CA"}

	cases := []struct {
		name           string
		firstAir       *time.Time
		lang           *string
		countries      []string
		originCountry  *string
		wantPremiere   *string
		wantLang       *string
		wantCountries  []string
		wantSingularCt *string
	}{
		{
			name:           "all present",
			firstAir:       &firstAir,
			lang:           &lang,
			countries:      countries,
			originCountry:  func() *string { s := "US"; return &s }(),
			wantPremiere:   func() *string { s := "2026-05-28"; return &s }(),
			wantLang:       &lang,
			wantCountries:  countries,
			wantSingularCt: func() *string { s := "US"; return &s }(),
		},
		{
			name:           "no first_air_date — premiere omitted",
			firstAir:       nil,
			lang:           &lang,
			countries:      countries,
			originCountry:  nil,
			wantPremiere:   nil,
			wantLang:       &lang,
			wantCountries:  countries,
			wantSingularCt: func() *string { s := "US"; return &s }(), // backfilled from countries[0]
		},
		{
			name:           "no original_language — language omitted",
			firstAir:       &firstAir,
			lang:           nil,
			countries:      nil,
			originCountry:  nil,
			wantPremiere:   func() *string { s := "2026-05-28"; return &s }(),
			wantLang:       nil,
			wantCountries:  nil,
			wantSingularCt: nil,
		},
		{
			name:           "empty language string — omitted",
			firstAir:       nil,
			lang:           func() *string { s := ""; return &s }(),
			countries:      nil,
			originCountry:  nil,
			wantPremiere:   nil,
			wantLang:       nil,
			wantCountries:  nil,
			wantSingularCt: nil,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			d := &seriesdetail.Detail{
				Canon: series.Canon{
					Title:            "X",
					FirstAirDate:     tc.firstAir,
					OriginalLanguage: tc.lang,
					OriginCountries:  tc.countries,
					OriginCountry:    tc.originCountry,
				},
			}
			h := mapHero(d)
			require.Equal(t, tc.wantPremiere, h.PremiereDate, "premiere_date")
			require.Equal(t, tc.wantLang, h.OriginalLanguage, "original_language")
			require.Equal(t, tc.wantCountries, h.Countries, "countries")
			require.Equal(t, tc.wantSingularCt, h.Country, "country singular")
		})
	}
}

// Story 374: mapLibrary reads EpisodesOnDisk + SizeOnDiskBytes from
// the cache row (Sonarr statistics) instead of summing episode_states.
func TestMapLibrary_ReadsOnDiskAndSizeFromCache(t *testing.T) {
	t.Parallel()
	d := &seriesdetail.Detail{
		CacheEntry: series.CacheEntry{
			Monitored:        true,
			MissingCount:     0,
			EpisodeFileCount: 128,
			SizeOnDiskBytes:  142_300_000_000,
		},
		Seasons: []seriesdetail.SeasonDetail{
			{
				Canon: series.CanonSeason{SeasonNumber: 1},
				Episodes: []seriesdetail.EpisodeDetail{
					{Canon: series.CanonEpisode{EpisodeNumber: 1, SeasonNumber: 1}},
					{Canon: series.CanonEpisode{EpisodeNumber: 2, SeasonNumber: 1}},
				},
			},
			{
				Canon: series.CanonSeason{SeasonNumber: 2},
				Episodes: []seriesdetail.EpisodeDetail{
					{Canon: series.CanonEpisode{EpisodeNumber: 1, SeasonNumber: 2}},
					{Canon: series.CanonEpisode{EpisodeNumber: 2, SeasonNumber: 2}},
					{Canon: series.CanonEpisode{EpisodeNumber: 3, SeasonNumber: 2}},
				},
			},
		},
	}
	lib := mapLibrary(d)
	require.Equal(t, 128, lib.EpisodesOnDisk)
	require.Equal(t, int64(142_300_000_000), lib.SizeOnDiskBytes)
	require.Equal(t, 5, lib.EpisodesTotal)
	require.True(t, lib.Monitored)
	require.Empty(t, lib.DominantQuality)
}

// Story 374: DominantQuality still derives from episode_states because
// per-episode quality strings are not cached on the row.
func TestMapLibrary_DominantQualityFromEpisodeStates(t *testing.T) {
	t.Parallel()
	qWEB := "WEB-DL 1080p"
	qBR := "Bluray-1080p"
	d := &seriesdetail.Detail{
		CacheEntry: series.CacheEntry{
			Monitored:        true,
			EpisodeFileCount: 3,
			SizeOnDiskBytes:  3_000_000_000,
		},
		Seasons: []seriesdetail.SeasonDetail{{
			Canon: series.CanonSeason{SeasonNumber: 1},
			Episodes: []seriesdetail.EpisodeDetail{
				{
					Canon: series.CanonEpisode{EpisodeNumber: 1, SeasonNumber: 1},
					State: &series.EpisodeState{HasFile: true, Quality: &qWEB},
				},
				{
					Canon: series.CanonEpisode{EpisodeNumber: 2, SeasonNumber: 1},
					State: &series.EpisodeState{HasFile: true, Quality: &qWEB},
				},
				{
					Canon: series.CanonEpisode{EpisodeNumber: 3, SeasonNumber: 1},
					State: &series.EpisodeState{HasFile: true, Quality: &qBR},
				},
			},
		}},
	}
	lib := mapLibrary(d)
	require.Equal(t, qWEB, lib.DominantQuality)
	require.Equal(t, 3, lib.EpisodesOnDisk)
}

// Story 376: mapLibrary projects AiredEpisodeCount onto the wire
// `episodes_aired` field so the FE can use it as the percentage
// denominator (covers FROM 38/38 case where 2 unaired episodes would
// otherwise depress the headline to 95%).
func TestMapLibrary_ProjectsAiredEpisodeCount(t *testing.T) {
	t.Parallel()
	d := &seriesdetail.Detail{
		CacheEntry: series.CacheEntry{
			Monitored:         true,
			MissingCount:      0,
			EpisodeFileCount:  38,
			SizeOnDiskBytes:   12_400_000_000,
			AiredEpisodeCount: 38,
		},
		Seasons: []seriesdetail.SeasonDetail{
			{
				Canon: series.CanonSeason{SeasonNumber: 4},
				Episodes: []seriesdetail.EpisodeDetail{
					{Canon: series.CanonEpisode{EpisodeNumber: 1, SeasonNumber: 4}},
					{Canon: series.CanonEpisode{EpisodeNumber: 2, SeasonNumber: 4}},
				},
			},
		},
	}
	lib := mapLibrary(d)
	require.Equal(t, 38, lib.EpisodesAired)
	require.Equal(t, 38, lib.EpisodesOnDisk)
}

// --- Story 377: mapSeasons SeasonStats branches ---

func TestMapSeasons_PrefersStatsOverEpisodeWalk(t *testing.T) {
	t.Parallel()
	stats := &series.SeasonStat{
		SeasonNumber:      1,
		EpisodeFileCount:  10,
		AiredEpisodeCount: 10,
		TotalEpisodeCount: 10,
		Monitored:         true,
	}
	d := []seriesdetail.SeasonDetail{{
		Canon: series.CanonSeason{SeasonNumber: 1},
		Stats: stats,
		// Episode walk would yield 0 on disk if we were reading State;
		// the stats branch must win.
		Episodes: []seriesdetail.EpisodeDetail{
			{Canon: series.CanonEpisode{EpisodeNumber: 1, SeasonNumber: 1}},
			{Canon: series.CanonEpisode{EpisodeNumber: 2, SeasonNumber: 1}},
		},
	}}
	out := mapSeasons(&seriesdetail.Detail{Seasons: d})
	require.Len(t, out, 1)
	require.Equal(t, 10, out[0].OnDiskCount)
	require.Equal(t, 0, out[0].MissingCount)
	require.Equal(t, 10, out[0].EpisodeCount)
	require.True(t, out[0].Monitored)
}

func TestMapSeasons_ClampsMissingNegative(t *testing.T) {
	t.Parallel()
	stats := &series.SeasonStat{
		SeasonNumber:      1,
		EpisodeFileCount:  12,
		AiredEpisodeCount: 10,
		TotalEpisodeCount: 10,
	}
	d := []seriesdetail.SeasonDetail{{
		Canon: series.CanonSeason{SeasonNumber: 1},
		Stats: stats,
	}}
	out := mapSeasons(&seriesdetail.Detail{Seasons: d})
	require.Len(t, out, 1)
	require.Equal(t, 12, out[0].OnDiskCount)
	require.Equal(t, 0, out[0].MissingCount, "missing must clamp to 0 when file_count > aired")
}

func TestMapSeasons_FallsBackToEpisodeWalkWhenStatsNil(t *testing.T) {
	t.Parallel()
	qWEB := "WEB-DL 1080p"
	d := []seriesdetail.SeasonDetail{{
		Canon: series.CanonSeason{SeasonNumber: 1},
		Episodes: []seriesdetail.EpisodeDetail{
			{
				Canon: series.CanonEpisode{EpisodeNumber: 1, SeasonNumber: 1},
				State: &series.EpisodeState{HasFile: true, Quality: &qWEB},
			},
			{
				Canon: series.CanonEpisode{EpisodeNumber: 2, SeasonNumber: 1},
				State: &series.EpisodeState{HasFile: false},
			},
			{
				Canon: series.CanonEpisode{EpisodeNumber: 3, SeasonNumber: 1},
				// no State — counts toward missing
			},
		},
	}}
	out := mapSeasons(&seriesdetail.Detail{Seasons: d})
	require.Len(t, out, 1)
	require.Equal(t, 1, out[0].OnDiskCount)
	require.Equal(t, 2, out[0].MissingCount)
	require.Equal(t, 3, out[0].EpisodeCount)
}

func TestMapSeasons_PartialPack_FROM(t *testing.T) {
	t.Parallel()
	// Acceptance smoke shape: FROM S4 with 8 aired / 10 total / 8 on disk.
	stats := &series.SeasonStat{
		SeasonNumber:      4,
		EpisodeFileCount:  8,
		AiredEpisodeCount: 8,
		TotalEpisodeCount: 10,
		Monitored:         true,
	}
	d := []seriesdetail.SeasonDetail{{
		Canon: series.CanonSeason{SeasonNumber: 4},
		Stats: stats,
	}}
	out := mapSeasons(&seriesdetail.Detail{Seasons: d})
	require.Len(t, out, 1)
	require.Equal(t, 8, out[0].OnDiskCount)
	require.Equal(t, 0, out[0].MissingCount, "all aired episodes are on disk")
	require.Equal(t, 10, out[0].EpisodeCount, "EpisodeCount must surface TotalEpisodeCount")
}
