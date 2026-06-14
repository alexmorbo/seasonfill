package seriesdetail

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/application/ports"
	"github.com/alexmorbo/seasonfill/domain/enrichment"
	"github.com/alexmorbo/seasonfill/domain/people"
	"github.com/alexmorbo/seasonfill/domain/series"
	"github.com/alexmorbo/seasonfill/domain/taxonomy"
	"github.com/alexmorbo/seasonfill/infrastructure/database"
	"github.com/alexmorbo/seasonfill/infrastructure/sonarr"
)

// --- fakes ---

type fakeSeriesCache struct {
	entries map[string]series.CacheEntry // key = "instance|sonarrID"
	byCanon map[int64][]series.CacheEntry
	getErr  error
	listErr error
}

func cacheKey(instance string, sonarrID int) string {
	return instance + "|" + intToStr(sonarrID)
}

func intToStr(i int) string {
	return string(rune('0' + i)) // tiny — tests only use small ids
}

func (f *fakeSeriesCache) Get(_ context.Context, instance string, sonarrID int) (series.CacheEntry, error) {
	if f.getErr != nil {
		return series.CacheEntry{}, f.getErr
	}
	e, ok := f.entries[cacheKey(instance, sonarrID)]
	if !ok {
		return series.CacheEntry{}, ports.ErrNotFound
	}
	return e, nil
}

func (f *fakeSeriesCache) ListBySeriesID(_ context.Context, seriesID int64) ([]series.CacheEntry, error) {
	if f.listErr != nil {
		return nil, f.listErr
	}
	return f.byCanon[seriesID], nil
}

type fakeSeries struct {
	rows map[int64]series.Canon
	err  error
}

func (f *fakeSeries) Get(_ context.Context, id int64) (series.Canon, error) {
	if f.err != nil {
		return series.Canon{}, f.err
	}
	c, ok := f.rows[id]
	if !ok {
		return series.Canon{}, ports.ErrNotFound
	}
	return c, nil
}

func (f *fakeSeries) GetByTMDBID(_ context.Context, tmdbID int) (series.Canon, error) {
	if f.err != nil {
		return series.Canon{}, f.err
	}
	for _, c := range f.rows {
		if c.TMDBID != nil && *c.TMDBID == tmdbID {
			return c, nil
		}
	}
	return series.Canon{}, ports.ErrNotFound
}

type fakeSeriesTexts struct {
	rows map[string]series.SeriesText // key="seriesID|lang"
	err  error
}

func (f *fakeSeriesTexts) GetWithFallback(_ context.Context, sid int64, lang string) (series.SeriesText, error) {
	if f.err != nil {
		return series.SeriesText{}, f.err
	}
	if t, ok := f.rows[seriesTextKey(sid, lang)]; ok {
		return t, nil
	}
	if t, ok := f.rows[seriesTextKey(sid, "en-US")]; ok {
		return t, nil
	}
	return series.SeriesText{}, ports.ErrNotFound
}

func seriesTextKey(id int64, lang string) string { return lang + "|" + intToStr(int(id)) }

type fakeSeasons struct {
	rows []series.CanonSeason
	err  error
}

func (f *fakeSeasons) ListBySeries(_ context.Context, _ int64) ([]series.CanonSeason, error) {
	return f.rows, f.err
}

type fakeEpisodes struct {
	rows []series.CanonEpisode
	err  error
}

func (f *fakeEpisodes) ListBySeries(_ context.Context, _ int64) ([]series.CanonEpisode, error) {
	return f.rows, f.err
}

type fakeEpisodeStates struct {
	rows []series.EpisodeState
	err  error
}

func (f *fakeEpisodeStates) ListBySeries(_ context.Context, _ string, _ int64) ([]series.EpisodeState, error) {
	return f.rows, f.err
}

type fakeEpisodeTexts struct{}

func (fakeEpisodeTexts) GetWithFallback(_ context.Context, _ int64, _ string) (series.EpisodeText, error) {
	return series.EpisodeText{}, ports.ErrNotFound
}

type fakeSeriesPeople struct {
	rows []people.SeriesCredit
	err  error
}

func (f *fakeSeriesPeople) ListBySeries(_ context.Context, _ int64, _ people.SeriesCreditKind) ([]people.SeriesCredit, error) {
	return f.rows, f.err
}

type fakePeople struct {
	rows []people.Person
}

func (f *fakePeople) ListByIDs(_ context.Context, _ []int64) ([]people.Person, error) {
	return f.rows, nil
}

type fakeGenres struct {
	ids []int64
}

func (f *fakeGenres) ListBySeries(_ context.Context, _ int64) ([]int64, error) { return f.ids, nil }
func (f *fakeGenres) Get(_ context.Context, id int64, lang string) (taxonomy.Genre, error) {
	return taxonomy.Genre{ID: id, Name: "Drama", Language: lang}, nil
}

type fakeKeywords struct{ ids []int64 }

func (f *fakeKeywords) ListBySeries(_ context.Context, _ int64) ([]int64, error) { return f.ids, nil }
func (f *fakeKeywords) Get(_ context.Context, id int64, lang string) (taxonomy.Keyword, error) {
	return taxonomy.Keyword{ID: id, Name: "kw", Language: lang}, nil
}

type fakeNetworks struct{ ids []int64 }

func (f *fakeNetworks) ListBySeries(_ context.Context, _ int64) ([]int64, error) { return f.ids, nil }
func (f *fakeNetworks) ListByIDs(_ context.Context, ids []int64) ([]taxonomy.Network, error) {
	out := make([]taxonomy.Network, 0, len(ids))
	for _, id := range ids {
		out = append(out, taxonomy.Network{ID: id, Name: "AMC"})
	}
	return out, nil
}

type fakeCompanies struct{}

func (fakeCompanies) ListBySeries(_ context.Context, _ int64) ([]int64, error) { return nil, nil }
func (fakeCompanies) ListByIDs(_ context.Context, _ []int64) ([]taxonomy.ProductionCompany, error) {
	return nil, nil
}

type fakeVideos struct {
	rows []database.VideoModel
}

func (f *fakeVideos) ListBySeriesAndType(_ context.Context, _ int64, _ string) ([]database.VideoModel, error) {
	return f.rows, nil
}

type fakeContentRatings struct {
	rows []database.ContentRatingModel
}

func (f *fakeContentRatings) ListBySeries(_ context.Context, _ int64) ([]database.ContentRatingModel, error) {
	return f.rows, nil
}

type fakeExternalIDs struct {
	rows []database.ExternalIDModel
}

func (f *fakeExternalIDs) ListByEntity(_ context.Context, _ enrichment.EntityType, _ int64) ([]database.ExternalIDModel, error) {
	return f.rows, nil
}

type fakeRecommendations struct {
	ids []int64
}

func (f *fakeRecommendations) ListBySeries(_ context.Context, _ int64) ([]int64, error) {
	return f.ids, nil
}

type fakeSyncLog struct {
	rows map[string]enrichment.SyncLog // key = source
}

func (f *fakeSyncLog) GetLastSync(_ context.Context, _ enrichment.EntityType, _ int64, src enrichment.Source) (enrichment.SyncLog, error) {
	if l, ok := f.rows[string(src)]; ok {
		return l, nil
	}
	return enrichment.SyncLog{}, ports.ErrNotFound
}

type fakeSonarrQueueLister struct {
	payload sonarr.QueuePayload
	err     error
}

func (f fakeSonarrQueueLister) Queue(_ context.Context, _ int) (sonarr.QueuePayload, error) {
	if f.err != nil {
		return sonarr.QueuePayload{}, f.err
	}
	return f.payload, nil
}

// --- helpers ---

func newSilentLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelError}))
}

func i64ptr(v int64) *int64        { return &v }
func intPtr(v int) *int            { return &v }
func strPtr(v string) *string      { return &v }
func tmPtr(v time.Time) *time.Time { return &v }

func baseDeps(t *testing.T) (Deps, *fakeSeriesCache, *fakeSeries) {
	t.Helper()
	now := time.Now().UTC()
	cache := &fakeSeriesCache{
		entries: map[string]series.CacheEntry{
			cacheKey("alpha", 1): {
				InstanceName:   "alpha",
				SonarrSeriesID: 1,
				SeriesID:       i64ptr(42),
				Title:          "Breaking Bad",
				Monitored:      true,
				MissingCount:   3,
			},
		},
		byCanon: map[int64][]series.CacheEntry{
			99: {{InstanceName: "alpha", SonarrSeriesID: 5, SeriesID: i64ptr(99)}},
		},
	}
	canon := &fakeSeries{
		rows: map[int64]series.Canon{
			42: {ID: 42, Title: "Breaking Bad", Year: intPtr(2008), Status: strPtr("Ended")},
			99: {ID: 99, Title: "Recommended Show"},
		},
	}
	deps := Deps{
		SeriesCache:       cache,
		SeriesCacheLookup: cache,
		Series:            canon,
		SeriesTexts:       &fakeSeriesTexts{},
		Seasons:           &fakeSeasons{},
		Episodes:          &fakeEpisodes{},
		EpisodeStates:     &fakeEpisodeStates{},
		EpisodeTexts:      fakeEpisodeTexts{},
		SeriesPeople:      &fakeSeriesPeople{},
		People:            &fakePeople{},
		Genres:            &fakeGenres{},
		Keywords:          &fakeKeywords{},
		Networks:          &fakeNetworks{},
		Companies:         fakeCompanies{},
		Videos:            &fakeVideos{},
		ContentRatings:    &fakeContentRatings{},
		ExternalIDs:       &fakeExternalIDs{},
		Recommendations:   &fakeRecommendations{},
		SyncLog:           &fakeSyncLog{rows: map[string]enrichment.SyncLog{}},
		SonarrFor: func(_ string) (SonarrQueueLister, bool) {
			return fakeSonarrQueueLister{payload: sonarr.QueuePayload{}}, true
		},
		Logger: newSilentLogger(),
		Now:    func() time.Time { return now },
	}
	return deps, cache, canon
}

// --- tests ---

func TestComposer_Get_HappyPath(t *testing.T) {
	deps, _, _ := baseDeps(t)
	// All four enrichment sources fresh.
	now := time.Now().UTC()
	deps.SyncLog = &fakeSyncLog{rows: map[string]enrichment.SyncLog{
		"tmdb_series": {SyncedAt: &now, Outcome: enrichment.OutcomeOK},
		"tmdb_season": {SyncedAt: &now, Outcome: enrichment.OutcomeOK},
		"tmdb_person": {SyncedAt: &now, Outcome: enrichment.OutcomeOK},
		"omdb":        {SyncedAt: &now, Outcome: enrichment.OutcomeOK},
	}}
	c := NewComposer(deps)
	d, err := c.Get(context.Background(), "alpha", 1, "en-US")
	require.NoError(t, err)
	require.Equal(t, int64(42), d.SeriesID)
	require.Equal(t, "Breaking Bad", d.Canon.Title)
	require.Empty(t, d.Degraded)
}

func TestComposer_Get_ColdSeries_DegradedAllTMDB(t *testing.T) {
	deps, _, _ := baseDeps(t)
	// SyncLog has no rows for any source → all four degraded.
	c := NewComposer(deps)
	d, err := c.Get(context.Background(), "alpha", 1, "en-US")
	require.NoError(t, err)
	deg := map[enrichment.Source]bool{}
	for _, s := range d.Degraded {
		deg[s] = true
	}
	require.True(t, deg[enrichment.SourceTMDBSeries])
	require.True(t, deg[enrichment.SourceTMDBSeason])
	require.True(t, deg[enrichment.SourceTMDBPerson])
	require.True(t, deg[enrichment.SourceOMDb])
}

func TestComposer_Get_404_MissingCache(t *testing.T) {
	deps, _, _ := baseDeps(t)
	c := NewComposer(deps)
	_, err := c.Get(context.Background(), "alpha", 999, "en-US")
	require.ErrorIs(t, err, ports.ErrNotFound)
}

func TestComposer_Get_404_NilSeriesIDInCache(t *testing.T) {
	deps, cache, _ := baseDeps(t)
	cache.entries[cacheKey("alpha", 2)] = series.CacheEntry{InstanceName: "alpha", SonarrSeriesID: 2, SeriesID: nil}
	c := NewComposer(deps)
	_, err := c.Get(context.Background(), "alpha", 2, "en-US")
	require.ErrorIs(t, err, ports.ErrNotFound)
}

func TestComposer_Get_SonarrUnreachable_DownloadNil_DegradedSonarr(t *testing.T) {
	deps, _, _ := baseDeps(t)
	deps.SonarrFor = func(_ string) (SonarrQueueLister, bool) { return nil, false }
	c := NewComposer(deps)
	d, err := c.Get(context.Background(), "alpha", 1, "en-US")
	require.NoError(t, err)
	require.Nil(t, d.Queue)
	deg := map[enrichment.Source]bool{}
	for _, s := range d.Degraded {
		deg[s] = true
	}
	require.True(t, deg[enrichment.SourceSonarr], "expected sonarr in degraded")
}

func TestComposer_Get_BranchFailureNeverBubbles(t *testing.T) {
	deps, _, _ := baseDeps(t)
	// Force the cast branch to fail.
	deps.SeriesPeople = &fakeSeriesPeople{err: errors.New("boom")}
	// Force taxonomy branch.
	deps.Genres = failingGenres{}
	c := NewComposer(deps)
	d, err := c.Get(context.Background(), "alpha", 1, "en-US")
	require.NoError(t, err)
	require.Equal(t, int64(42), d.SeriesID)
	// Degraded includes tmdb_series due to the OR-in rule.
	require.Contains(t, d.Degraded, enrichment.SourceTMDBSeries)
}

type failingGenres struct{}

func (failingGenres) ListBySeries(_ context.Context, _ int64) ([]int64, error) {
	return nil, errors.New("genres boom")
}
func (failingGenres) Get(_ context.Context, _ int64, _ string) (taxonomy.Genre, error) {
	return taxonomy.Genre{}, errors.New("genres boom")
}

func TestComposer_Get_RecommendationsInLibrary(t *testing.T) {
	deps, _, _ := baseDeps(t)
	deps.Recommendations = &fakeRecommendations{ids: []int64{99}}
	c := NewComposer(deps)
	d, err := c.Get(context.Background(), "alpha", 1, "en-US")
	require.NoError(t, err)
	require.Len(t, d.Recommendations, 1)
	require.True(t, d.Recommendations[0].InLibrary)
	require.Equal(t, "alpha", d.Recommendations[0].InstanceName)
	require.Equal(t, 5, d.Recommendations[0].SonarrSeriesID)
}

func TestComposer_GetSeason_FiltersToSeason(t *testing.T) {
	deps, _, _ := baseDeps(t)
	deps.Seasons = &fakeSeasons{rows: []series.CanonSeason{
		{ID: 1, SeriesID: 42, SeasonNumber: 1},
		{ID: 2, SeriesID: 42, SeasonNumber: 2},
	}}
	deps.Episodes = &fakeEpisodes{rows: []series.CanonEpisode{
		{ID: 10, SeriesID: 42, SeasonNumber: 1, EpisodeNumber: 1},
		{ID: 11, SeriesID: 42, SeasonNumber: 2, EpisodeNumber: 1},
	}}
	c := NewComposer(deps)
	d, err := c.GetSeason(context.Background(), "alpha", 1, 2, "en-US")
	require.NoError(t, err)
	require.Len(t, d.Seasons, 1)
	require.Equal(t, 2, d.Seasons[0].Canon.SeasonNumber)
}

func TestComposer_GetSeason_UnknownSeason_404(t *testing.T) {
	deps, _, _ := baseDeps(t)
	deps.Seasons = &fakeSeasons{rows: []series.CanonSeason{
		{ID: 1, SeriesID: 42, SeasonNumber: 1},
	}}
	c := NewComposer(deps)
	_, err := c.GetSeason(context.Background(), "alpha", 1, 99, "en-US")
	require.ErrorIs(t, err, ports.ErrNotFound)
}

func TestResolveLang(t *testing.T) {
	require.Equal(t, "en-US", resolveLang(""))
	require.Equal(t, "en-US", resolveLang("   "))
	require.Equal(t, "ru-RU", resolveLang("ru-RU"))
	require.Equal(t, "en-US", resolveLang(stringOfLen(36)))
}

func stringOfLen(n int) string {
	b := make([]byte, n)
	for i := range b {
		b[i] = 'x'
	}
	return string(b)
}

func TestPickContentRating_LocalePreference(t *testing.T) {
	rs := []database.ContentRatingModel{
		{CountryCode: "US", Rating: "TV-MA"},
		{CountryCode: "RU", Rating: "16+"},
	}
	pick := pickContentRating(rs, "ru-RU")
	require.NotNil(t, pick)
	require.Equal(t, "RU", pick.CountryCode)

	pick = pickContentRating(rs, "en-US")
	require.NotNil(t, pick)
	require.Equal(t, "US", pick.CountryCode)

	pick = pickContentRating(rs, "fr-FR")
	require.NotNil(t, pick)
	require.Equal(t, "US", pick.CountryCode) // US fallback

	pick = pickContentRating(nil, "en-US")
	require.Nil(t, pick)
}

func TestClassifyKind(t *testing.T) {
	require.Equal(t, enrichment.KindSeriesEnded, classifyKind(series.Canon{Status: strPtr("Ended")}))
	require.Equal(t, enrichment.KindSeriesContinuing, classifyKind(series.Canon{Status: strPtr("Continuing")}))
	require.Equal(t, enrichment.KindSeriesContinuing, classifyKind(series.Canon{InProduction: true}))
	require.Equal(t, enrichment.KindSeriesContinuing, classifyKind(series.Canon{}))
}

// silence unused-helper warning if any
var _ = tmPtr

// --- story 312: media resolver integration ---

type fakeMediaLookupIntegration struct {
	byURL map[string]string
}

func (f *fakeMediaLookupIntegration) HashForSourceURL(_ context.Context, url string) (string, error) {
	if h, ok := f.byURL[url]; ok {
		return h, nil
	}
	return "", ports.ErrNotFound
}

// networksWithLogo is a per-test fake that exposes a logo path on a single
// network. Used by TestComposer_Get_ResolvesAllAssetFields to drive the
// network logo resolution branch.
type networksWithLogo struct {
	logo string
	id   int64
}

func (n networksWithLogo) ListBySeries(_ context.Context, _ int64) ([]int64, error) {
	return []int64{n.id}, nil
}
func (n networksWithLogo) ListByIDs(_ context.Context, _ []int64) ([]taxonomy.Network, error) {
	v := n.logo
	return []taxonomy.Network{{ID: n.id, Name: "AMC", LogoAsset: &v}}, nil
}

func TestComposer_Get_ResolvesPosterToHash(t *testing.T) {
	deps, _, canon := baseDeps(t)
	rawPath := "/abc.jpg"
	canon.rows[42] = series.Canon{
		ID: 42, Title: "Breaking Bad", PosterAsset: strPtr(rawPath),
	}
	const wantHash = "ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff"
	deps.MediaResolver = NewMediaResolver(&fakeMediaLookupIntegration{byURL: map[string]string{
		"https://image.tmdb.org/t/p/w342/abc.jpg": wantHash,
	}}, nil, nil, newSilentLogger())
	c := NewComposer(deps)
	d, err := c.Get(context.Background(), "alpha", 1, "en-US")
	require.NoError(t, err)
	require.NotNil(t, d.Canon.PosterAsset)
	require.Equal(t, wantHash, *d.Canon.PosterAsset)
}

func TestComposer_Get_PosterMissResolvesToNil(t *testing.T) {
	deps, _, canon := baseDeps(t)
	rawPath := "/abc.jpg"
	canon.rows[42] = series.Canon{ID: 42, Title: "Breaking Bad", PosterAsset: strPtr(rawPath)}
	// Empty lookup table → miss → nil.
	deps.MediaResolver = NewMediaResolver(&fakeMediaLookupIntegration{byURL: map[string]string{}}, nil, nil, newSilentLogger())
	c := NewComposer(deps)
	d, err := c.Get(context.Background(), "alpha", 1, "en-US")
	require.NoError(t, err)
	require.Nil(t, d.Canon.PosterAsset)
}

func TestComposer_Get_NopResolver_KeepsNil(t *testing.T) {
	deps, _, canon := baseDeps(t)
	rawPath := "/abc.jpg"
	canon.rows[42] = series.Canon{ID: 42, Title: "Breaking Bad", PosterAsset: strPtr(rawPath)}
	deps.MediaResolver = nil // → NewComposer fills with nop
	c := NewComposer(deps)
	d, err := c.Get(context.Background(), "alpha", 1, "en-US")
	require.NoError(t, err)
	require.Nil(t, d.Canon.PosterAsset, "nop resolver must wipe raw path to nil")
}

func TestComposer_Get_ResolvesAllAssetFields(t *testing.T) {
	deps, _, canon := baseDeps(t)
	canon.rows[42] = series.Canon{
		ID: 42, Title: "Breaking Bad",
		PosterAsset:   strPtr("/poster.jpg"),
		BackdropAsset: strPtr("/back.jpg"),
	}
	// Network with a logo.
	deps.Networks = networksWithLogo{logo: "/logo.png", id: 7}
	deps.Seasons = &fakeSeasons{rows: []series.CanonSeason{
		{ID: 1, SeriesID: 42, SeasonNumber: 1, PosterAsset: strPtr("/s1.jpg")},
	}}
	deps.SeriesPeople = &fakeSeriesPeople{rows: []people.SeriesCredit{
		{PersonID: 100, Kind: people.SeriesCreditCast, CreditOrder: intPtr(1)},
	}}
	deps.People = &fakePeople{rows: []people.Person{
		{ID: 100, Name: "Bryan Cranston", ProfileAsset: strPtr("/bryan.jpg")},
	}}
	deps.Recommendations = &fakeRecommendations{ids: []int64{99}}
	canon.rows[99] = series.Canon{ID: 99, Title: "Recommended Show", PosterAsset: strPtr("/rec.jpg")}

	const hashPoster = "1111111111111111111111111111111111111111111111111111111111111111"
	const hashBack = "2222222222222222222222222222222222222222222222222222222222222222"
	const hashLogo = "3333333333333333333333333333333333333333333333333333333333333333"
	const hashSeason = "4444444444444444444444444444444444444444444444444444444444444444"
	const hashProfile = "5555555555555555555555555555555555555555555555555555555555555555"
	const hashRec = "6666666666666666666666666666666666666666666666666666666666666666"
	deps.MediaResolver = NewMediaResolver(&fakeMediaLookupIntegration{byURL: map[string]string{
		"https://image.tmdb.org/t/p/w342/poster.jpg": hashPoster,
		"https://image.tmdb.org/t/p/w1280/back.jpg":  hashBack,
		"https://image.tmdb.org/t/p/w185/logo.png":   hashLogo,
		"https://image.tmdb.org/t/p/w154/s1.jpg":     hashSeason,
		"https://image.tmdb.org/t/p/w185/bryan.jpg":  hashProfile,
		"https://image.tmdb.org/t/p/w342/rec.jpg":    hashRec,
	}}, nil, nil, newSilentLogger())

	c := NewComposer(deps)
	d, err := c.Get(context.Background(), "alpha", 1, "en-US")
	require.NoError(t, err)
	require.NotNil(t, d.Canon.PosterAsset)
	require.Equal(t, hashPoster, *d.Canon.PosterAsset)
	require.NotNil(t, d.Canon.BackdropAsset)
	require.Equal(t, hashBack, *d.Canon.BackdropAsset)
	require.Len(t, d.Networks, 1)
	require.NotNil(t, d.Networks[0].LogoAsset)
	require.Equal(t, hashLogo, *d.Networks[0].LogoAsset)
	require.Len(t, d.Seasons, 1)
	require.NotNil(t, d.Seasons[0].Canon.PosterAsset)
	require.Equal(t, hashSeason, *d.Seasons[0].Canon.PosterAsset)
	require.Len(t, d.Cast, 1)
	require.NotNil(t, d.Cast[0].Person.ProfileAsset)
	require.Equal(t, hashProfile, *d.Cast[0].Person.ProfileAsset)
	require.Len(t, d.Recommendations, 1)
	require.NotNil(t, d.Recommendations[0].Series.PosterAsset)
	require.Equal(t, hashRec, *d.Recommendations[0].Series.PosterAsset)
}
