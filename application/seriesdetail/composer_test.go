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
	appmedia "github.com/alexmorbo/seasonfill/internal/mediaproxy/app"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
)

// --- fakes ---

type fakeSeriesCache struct {
	entries map[string]series.CacheEntry // key = "instance|sonarrID"
	byCanon map[domain.SeriesID][]series.CacheEntry
	getErr  error
	listErr error
}

func cacheKey(instance domain.InstanceName, sonarrID domain.SonarrSeriesID) string {
	return string(instance) + "|" + intToStr(int(sonarrID))
}

func intToStr(i int) string {
	return string(rune('0' + i)) // tiny — tests only use small ids
}

func (f *fakeSeriesCache) Get(_ context.Context, instance domain.InstanceName, sonarrID domain.SonarrSeriesID) (series.CacheEntry, error) {
	if f.getErr != nil {
		return series.CacheEntry{}, f.getErr
	}
	e, ok := f.entries[cacheKey(instance, sonarrID)]
	if !ok {
		return series.CacheEntry{}, ports.ErrNotFound
	}
	return e, nil
}

func (f *fakeSeriesCache) ListBySeriesID(_ context.Context, seriesID domain.SeriesID) ([]series.CacheEntry, error) {
	if f.listErr != nil {
		return nil, f.listErr
	}
	return f.byCanon[seriesID], nil
}

type fakeSeries struct {
	rows map[domain.SeriesID]series.Canon
	err  error
}

func (f *fakeSeries) Get(_ context.Context, id domain.SeriesID) (series.Canon, error) {
	if f.err != nil {
		return series.Canon{}, f.err
	}
	c, ok := f.rows[id]
	if !ok {
		return series.Canon{}, ports.ErrNotFound
	}
	return c, nil
}

func (f *fakeSeries) GetByTMDBID(_ context.Context, tmdbID domain.TMDBID) (series.Canon, error) {
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

func (f *fakeSeriesTexts) GetWithFallback(_ context.Context, sid domain.SeriesID, lang string) (series.SeriesText, error) {
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

func seriesTextKey(id domain.SeriesID, lang string) string {
	return lang + "|" + intToStr(int(id))
}

type fakeSeasons struct {
	rows []series.CanonSeason
	err  error
}

func (f *fakeSeasons) ListBySeries(_ context.Context, _ domain.SeriesID) ([]series.CanonSeason, error) {
	return f.rows, f.err
}

type fakeEpisodes struct {
	rows []series.CanonEpisode
	err  error
}

func (f *fakeEpisodes) ListBySeries(_ context.Context, _ domain.SeriesID) ([]series.CanonEpisode, error) {
	return f.rows, f.err
}

type fakeEpisodeStates struct {
	rows []series.EpisodeState
	err  error
}

func (f *fakeEpisodeStates) ListBySeries(_ context.Context, _ domain.InstanceName, _ domain.SeriesID) ([]series.EpisodeState, error) {
	return f.rows, f.err
}

type fakeEpisodeTexts struct{}

func (fakeEpisodeTexts) GetWithFallback(_ context.Context, _ domain.EpisodeID, _ string) (series.EpisodeText, error) {
	return series.EpisodeText{}, ports.ErrNotFound
}

type fakeSeasonStatsPort struct {
	rows []series.SeasonStat
	err  error
}

func (f *fakeSeasonStatsPort) ListBySeries(
	_ context.Context, _ domain.InstanceName, _ domain.SonarrSeriesID,
) ([]series.SeasonStat, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.rows, nil
}

type fakeSeriesPeople struct {
	rows []people.SeriesCredit
	err  error
}

func (f *fakeSeriesPeople) ListBySeries(_ context.Context, _ domain.SeriesID, _ people.SeriesCreditKind) ([]people.SeriesCredit, error) {
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

func (f *fakeGenres) ListBySeries(_ context.Context, _ domain.SeriesID) ([]int64, error) {
	return f.ids, nil
}
func (f *fakeGenres) Get(_ context.Context, id int64, lang string) (taxonomy.Genre, error) {
	return taxonomy.Genre{ID: id, Name: "Drama", Language: lang}, nil
}

type fakeKeywords struct{ ids []int64 }

func (f *fakeKeywords) ListBySeries(_ context.Context, _ domain.SeriesID) ([]int64, error) {
	return f.ids, nil
}
func (f *fakeKeywords) Get(_ context.Context, id int64, lang string) (taxonomy.Keyword, error) {
	return taxonomy.Keyword{ID: id, Name: "kw", Language: lang}, nil
}

type fakeNetworks struct{ ids []int64 }

func (f *fakeNetworks) ListBySeries(_ context.Context, _ domain.SeriesID) ([]int64, error) {
	return f.ids, nil
}
func (f *fakeNetworks) ListByIDs(_ context.Context, ids []int64) ([]taxonomy.Network, error) {
	out := make([]taxonomy.Network, 0, len(ids))
	for _, id := range ids {
		out = append(out, taxonomy.Network{ID: id, Name: "AMC"})
	}
	return out, nil
}

type fakeCompanies struct{}

func (fakeCompanies) ListBySeries(_ context.Context, _ domain.SeriesID) ([]int64, error) {
	return nil, nil
}
func (fakeCompanies) ListByIDs(_ context.Context, _ []int64) ([]taxonomy.ProductionCompany, error) {
	return nil, nil
}

type fakeVideos struct {
	rows []database.VideoModel
}

func (f *fakeVideos) ListBySeriesAndType(_ context.Context, _ domain.SeriesID, _ string) ([]database.VideoModel, error) {
	return f.rows, nil
}

type fakeContentRatings struct {
	rows []database.ContentRatingModel
}

func (f *fakeContentRatings) ListBySeries(_ context.Context, _ domain.SeriesID) ([]database.ContentRatingModel, error) {
	return f.rows, nil
}

type fakeExternalIDs struct {
	rows []database.ExternalIDModel
}

func (f *fakeExternalIDs) ListByEntity(_ context.Context, _ enrichment.EntityType, _ int64) ([]database.ExternalIDModel, error) {
	return f.rows, nil
}

type fakeRecommendations struct {
	ids []domain.SeriesID
}

func (f *fakeRecommendations) ListBySeries(_ context.Context, _ domain.SeriesID) ([]domain.SeriesID, error) {
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

func (f fakeSonarrQueueLister) Queue(_ context.Context, _ domain.SonarrSeriesID) (sonarr.QueuePayload, error) {
	if f.err != nil {
		return sonarr.QueuePayload{}, f.err
	}
	return f.payload, nil
}

// --- helpers ---

func newSilentLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelError}))
}

func seriesIDPtr(v int64) *domain.SeriesID {
	id := domain.SeriesID(v)
	return &id
}

func baseDeps(t *testing.T) (Deps, *fakeSeriesCache, *fakeSeries) {
	t.Helper()
	now := time.Now().UTC()
	cache := &fakeSeriesCache{
		entries: map[string]series.CacheEntry{
			cacheKey("alpha", 1): {
				InstanceName:   "alpha",
				SonarrSeriesID: 1,
				SeriesID:       seriesIDPtr(42),
				Title:          "Breaking Bad",
				Monitored:      true,
				MissingCount:   3,
			},
		},
		byCanon: map[domain.SeriesID][]series.CacheEntry{
			99: {{InstanceName: "alpha", SonarrSeriesID: 5, SeriesID: seriesIDPtr(99)}},
		},
	}
	canon := &fakeSeries{
		rows: map[domain.SeriesID]series.Canon{
			42: {ID: 42, Title: "Breaking Bad", Year: new(2008), Status: new("Ended")},
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
		SonarrFor: func(_ domain.InstanceName) (SonarrQueueLister, bool) {
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
	require.Equal(t, domain.SeriesID(42), d.SeriesID)
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
	deps.SonarrFor = func(_ domain.InstanceName) (SonarrQueueLister, bool) { return nil, false }
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
	require.Equal(t, domain.SeriesID(42), d.SeriesID)
	// Degraded includes tmdb_series due to the OR-in rule.
	require.Contains(t, d.Degraded, enrichment.SourceTMDBSeries)
}

type failingGenres struct{}

func (failingGenres) ListBySeries(_ context.Context, _ domain.SeriesID) ([]int64, error) {
	return nil, errors.New("genres boom")
}
func (failingGenres) Get(_ context.Context, _ int64, _ string) (taxonomy.Genre, error) {
	return taxonomy.Genre{}, errors.New("genres boom")
}

func TestComposer_Get_RecommendationsInLibrary(t *testing.T) {
	deps, _, _ := baseDeps(t)
	deps.Recommendations = &fakeRecommendations{ids: []domain.SeriesID{99}}
	c := NewComposer(deps)
	d, err := c.Get(context.Background(), "alpha", 1, "en-US")
	require.NoError(t, err)
	require.Len(t, d.Recommendations, 1)
	require.True(t, d.Recommendations[0].InLibrary)
	require.Equal(t, domain.InstanceName("alpha"), d.Recommendations[0].InstanceName)
	require.Equal(t, domain.SonarrSeriesID(5), d.Recommendations[0].SonarrSeriesID)
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
	require.Equal(t, enrichment.KindSeriesEnded, classifyKind(series.Canon{Status: new("Ended")}))
	require.Equal(t, enrichment.KindSeriesContinuing, classifyKind(series.Canon{Status: new("Continuing")}))
	require.Equal(t, enrichment.KindSeriesContinuing, classifyKind(series.Canon{InProduction: true}))
	require.Equal(t, enrichment.KindSeriesContinuing, classifyKind(series.Canon{}))
}

// --- story 312: media resolver integration ---

type fakeMediaLookupIntegration struct {
	byURL       map[string]string
	ensureCalls []ensurePendingCall
}

func (f *fakeMediaLookupIntegration) HashForSourceURL(_ context.Context, url string) (string, error) {
	if h, ok := f.byURL[url]; ok {
		return h, nil
	}
	return "", ports.ErrNotFound
}

func (f *fakeMediaLookupIntegration) EnsurePending(_ context.Context, hash, sourceURL, kind string) error {
	f.ensureCalls = append(f.ensureCalls, ensurePendingCall{hash, sourceURL, kind})
	return nil
}

// networksWithLogo is a per-test fake that exposes a logo path on a single
// network. Used by TestComposer_Get_ResolvesAllAssetFields to drive the
// network logo resolution branch.
type networksWithLogo struct {
	logo string
	id   int64
}

func (n networksWithLogo) ListBySeries(_ context.Context, _ domain.SeriesID) ([]int64, error) {
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
		ID: 42, Title: "Breaking Bad", PosterAsset: new(rawPath),
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

func TestComposer_Get_PosterMissReturnsEagerHash(t *testing.T) {
	// Story 320: hero poster lookup-miss returns the deterministic
	// sha256-hex of the source URL (eager hash) + writes a pending
	// media_assets row, so the handler's pending-row sync fetch can
	// recover when the user GETs /api/v1/media/:hash. Replaces the
	// pre-320 expectation of nil-on-miss for hero.
	deps, _, canon := baseDeps(t)
	rawPath := "/abc.jpg"
	canon.rows[42] = series.Canon{ID: 42, Title: "Breaking Bad", PosterAsset: new(rawPath)}
	lookup := &fakeMediaLookupIntegration{byURL: map[string]string{}}
	deps.MediaResolver = NewMediaResolver(lookup, nil, nil, newSilentLogger())
	c := NewComposer(deps)
	d, err := c.Get(context.Background(), "alpha", 1, "en-US")
	require.NoError(t, err)
	require.NotNil(t, d.Canon.PosterAsset, "hero poster must get the eager hash")
	url := appmedia.BuildTMDBImageURL("w342", rawPath)
	require.Equal(t, appmedia.HashFromURL(url), *d.Canon.PosterAsset)
	require.Len(t, lookup.ensureCalls, 1, "EnsurePending must fire once on hero miss")
	require.Equal(t, "poster_w342", lookup.ensureCalls[0].kind)
}

func TestComposer_Get_NopResolver_KeepsNil(t *testing.T) {
	deps, _, canon := baseDeps(t)
	rawPath := "/abc.jpg"
	canon.rows[42] = series.Canon{ID: 42, Title: "Breaking Bad", PosterAsset: new(rawPath)}
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
		PosterAsset:   new("/poster.jpg"),
		BackdropAsset: new("/back.jpg"),
	}
	// Network with a logo.
	deps.Networks = networksWithLogo{logo: "/logo.png", id: 7}
	deps.Seasons = &fakeSeasons{rows: []series.CanonSeason{
		{ID: 1, SeriesID: 42, SeasonNumber: 1, PosterAsset: new("/s1.jpg")},
	}}
	deps.SeriesPeople = &fakeSeriesPeople{rows: []people.SeriesCredit{
		{PersonID: 100, Kind: people.SeriesCreditCast, CreditOrder: new(1)},
	}}
	deps.People = &fakePeople{rows: []people.Person{
		{ID: 100, Name: "Bryan Cranston", ProfileAsset: new("/bryan.jpg")},
	}}
	deps.Recommendations = &fakeRecommendations{ids: []domain.SeriesID{99}}
	canon.rows[99] = series.Canon{ID: 99, Title: "Recommended Show", PosterAsset: new("/rec.jpg")}

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

// TestComposer_ResolveAssets_HeroEagerHashOnMiss exercises the story 320
// invariant directly on c.resolveAssets — hero poster + backdrop emit
// eager hashes via EnsurePending on lookup miss, while below-the-fold
// fields (network logo, cast profile, season poster, recommendation
// poster) stay nil-on-miss and DO NOT call EnsurePending.
func TestComposer_ResolveAssets_HeroEagerHashOnMiss(t *testing.T) {
	t.Parallel()
	d := &Detail{
		Canon: series.Canon{
			ID:            42,
			Title:         "Breaking Bad",
			PosterAsset:   new("/hero-poster.jpg"),
			BackdropAsset: new("/hero-backdrop.jpg"),
		},
		Networks: []taxonomy.Network{{ID: 1, Name: "AMC", LogoAsset: new("/net.png")}},
		Cast: []CastDetail{
			{Person: people.Person{ID: 1, Name: "Bryan Cranston", ProfileAsset: new("/p1.jpg")}},
		},
		Recommendations: []RecommendationDetail{
			{Series: series.Canon{ID: 99, Title: "Rec", PosterAsset: new("/rec1.jpg")}},
		},
		Seasons: []SeasonDetail{
			{Canon: series.CanonSeason{ID: 1, SeriesID: 42, SeasonNumber: 1, PosterAsset: new("/s1.jpg")}},
		},
	}
	lookup := &fakeMediaLookupIntegration{byURL: map[string]string{}}
	resolver := NewMediaResolver(lookup, nil, nil, newSilentLogger())
	c := NewComposer(Deps{MediaResolver: resolver})
	c.resolveAssets(context.Background(), d)

	// Hero — eager hash returned.
	require.NotNil(t, d.Canon.PosterAsset)
	require.NotNil(t, d.Canon.BackdropAsset)
	require.Equal(t,
		appmedia.HashFromURL(appmedia.BuildTMDBImageURL("w342", "/hero-poster.jpg")),
		*d.Canon.PosterAsset)
	require.Equal(t,
		appmedia.HashFromURL(appmedia.BuildTMDBImageURL("w1280", "/hero-backdrop.jpg")),
		*d.Canon.BackdropAsset)

	// Below the fold — nil on miss (legacy behavior).
	require.Nil(t, d.Networks[0].LogoAsset)
	require.Nil(t, d.Cast[0].Person.ProfileAsset)
	require.Nil(t, d.Recommendations[0].Series.PosterAsset)
	require.Nil(t, d.Seasons[0].Canon.PosterAsset)

	// EnsurePending fired exactly twice — once per hero field. Order is
	// poster first (composer line ordering in resolveAssets).
	require.Len(t, lookup.ensureCalls, 2)
	require.Equal(t, "poster_w342", lookup.ensureCalls[0].kind)
	require.Equal(t, "backdrop_w1280", lookup.ensureCalls[1].kind)
}

// TestComposer_ResolveAssets_EpisodeStills exercises the story 322 wiring:
// every episode still in every season gets r.Resolve at w300, so the wire
// field carries either a sha256 hex (lookup hit) OR nil (frontend renders
// monogram). NEVER the raw TMDB path.
func TestComposer_ResolveAssets_EpisodeStills(t *testing.T) {
	t.Parallel()
	d := &Detail{
		Canon: series.Canon{ID: 42, Title: "Breaking Bad"},
		Seasons: []SeasonDetail{
			{
				Canon: series.CanonSeason{ID: 1, SeriesID: 42, SeasonNumber: 1, PosterAsset: new("/s1.jpg")},
				Episodes: []EpisodeDetail{
					{Canon: series.CanonEpisode{ID: 10, SeasonNumber: 1, EpisodeNumber: 1, StillAsset: new("/ep1.jpg")}},
					{Canon: series.CanonEpisode{ID: 11, SeasonNumber: 1, EpisodeNumber: 2, StillAsset: new("/ep2.jpg")}},
				},
			},
			{
				Canon: series.CanonSeason{ID: 2, SeriesID: 42, SeasonNumber: 2, PosterAsset: new("/s2.jpg")},
				Episodes: []EpisodeDetail{
					{Canon: series.CanonEpisode{ID: 20, SeasonNumber: 2, EpisodeNumber: 1, StillAsset: new("/ep3.jpg")}},
				},
			},
		},
	}
	const (
		hashEp1 = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
		hashEp3 = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	)
	lookup := &fakeMediaLookupIntegration{byURL: map[string]string{
		"https://image.tmdb.org/t/p/w300/ep1.jpg": hashEp1,
		"https://image.tmdb.org/t/p/w300/ep3.jpg": hashEp3,
		// ep2.jpg intentionally missing — must come back nil (not raw path).
	}}
	resolver := NewMediaResolver(lookup, nil, nil, newSilentLogger())
	c := NewComposer(Deps{MediaResolver: resolver})
	c.resolveAssets(context.Background(), d)

	// Season 1.
	require.NotNil(t, d.Seasons[0].Episodes[0].Canon.StillAsset)
	require.Equal(t, hashEp1, *d.Seasons[0].Episodes[0].Canon.StillAsset)
	require.Nil(t, d.Seasons[0].Episodes[1].Canon.StillAsset, "miss must return nil, NOT raw path")
	// Season 2.
	require.NotNil(t, d.Seasons[1].Episodes[0].Canon.StillAsset)
	require.Equal(t, hashEp3, *d.Seasons[1].Episodes[0].Canon.StillAsset)
}

// TestComposer_ResolveAssets_SeasonPosterRegression is a defensive guard that
// season poster resolution stays wired — story 322 didn't change it, but the
// operator's report named seasons too, so we lock the contract: raw path
// NEVER leaks; hash on hit, nil on miss.
func TestComposer_ResolveAssets_SeasonPosterRegression(t *testing.T) {
	t.Parallel()
	d := &Detail{
		Canon: series.Canon{ID: 42, Title: "Breaking Bad"},
		Seasons: []SeasonDetail{
			{Canon: series.CanonSeason{ID: 1, SeriesID: 42, SeasonNumber: 1, PosterAsset: new("/seasonA.jpg")}},
			{Canon: series.CanonSeason{ID: 2, SeriesID: 42, SeasonNumber: 2, PosterAsset: new("/seasonB.jpg")}},
		},
	}
	const hashA = "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"
	lookup := &fakeMediaLookupIntegration{byURL: map[string]string{
		"https://image.tmdb.org/t/p/w154/seasonA.jpg": hashA,
	}}
	resolver := NewMediaResolver(lookup, nil, nil, newSilentLogger())
	c := NewComposer(Deps{MediaResolver: resolver})
	c.resolveAssets(context.Background(), d)
	require.NotNil(t, d.Seasons[0].Canon.PosterAsset)
	require.Equal(t, hashA, *d.Seasons[0].Canon.PosterAsset)
	require.Nil(t, d.Seasons[1].Canon.PosterAsset, "miss must return nil, NOT raw path")
}

// --- story 373: pickNextEpisode table test ---

func TestPickNextEpisode(t *testing.T) {
	now := time.Date(2026, 6, 16, 12, 0, 0, 0, time.UTC)
	yesterday := now.Add(-24 * time.Hour)
	tomorrow := now.Add(24 * time.Hour)
	in2days := now.Add(48 * time.Hour)
	in3days := now.Add(72 * time.Hour)
	titleA := "Jer Bud"
	titleB := "Specials Title"

	makeEp := func(season, ep int, air *time.Time, monitored bool, title *string) EpisodeDetail {
		e := EpisodeDetail{
			Canon: series.CanonEpisode{
				SeasonNumber:  season,
				EpisodeNumber: ep,
				AirDate:       air,
			},
		}
		if monitored {
			e.State = &series.EpisodeState{Monitored: true}
		}
		if title != nil {
			e.Text = &series.EpisodeText{Title: title}
		}
		return e
	}

	tests := []struct {
		name        string
		seasons     []SeasonDetail
		wantNil     bool
		wantSeason  int
		wantEpisode int
		wantAir     *time.Time
		wantTitle   *string
	}{
		{
			name: "future monitored wins over future unmonitored",
			seasons: []SeasonDetail{
				{
					Canon: series.CanonSeason{SeasonNumber: 9},
					Episodes: []EpisodeDetail{
						makeEp(9, 1, &tomorrow, false, nil),
						makeEp(9, 5, &in2days, true, &titleA),
					},
				},
			},
			wantSeason:  9,
			wantEpisode: 5,
			wantAir:     &in2days,
			wantTitle:   &titleA,
		},
		{
			name: "future unmonitored wins when no monitored future episode",
			seasons: []SeasonDetail{
				{
					Canon: series.CanonSeason{SeasonNumber: 9},
					Episodes: []EpisodeDetail{
						makeEp(9, 1, &tomorrow, false, nil),
						makeEp(9, 2, &in2days, false, nil),
					},
				},
			},
			wantSeason:  9,
			wantEpisode: 1,
			wantAir:     &tomorrow,
		},
		{
			name: "specials (season 0) excluded even when earliest future",
			seasons: []SeasonDetail{
				{
					Canon: series.CanonSeason{SeasonNumber: 0},
					Episodes: []EpisodeDetail{
						makeEp(0, 1, &tomorrow, true, &titleB),
					},
				},
				{
					Canon: series.CanonSeason{SeasonNumber: 9},
					Episodes: []EpisodeDetail{
						makeEp(9, 1, &in3days, true, &titleA),
					},
				},
			},
			wantSeason:  9,
			wantEpisode: 1,
			wantAir:     &in3days,
			wantTitle:   &titleA,
		},
		{
			name: "past episodes excluded",
			seasons: []SeasonDetail{
				{
					Canon: series.CanonSeason{SeasonNumber: 1},
					Episodes: []EpisodeDetail{
						makeEp(1, 1, &yesterday, true, nil),
						makeEp(1, 2, &in3days, true, nil),
					},
				},
			},
			wantSeason:  1,
			wantEpisode: 2,
			wantAir:     &in3days,
		},
		{
			name: "ties broken by air_date then season then episode",
			seasons: []SeasonDetail{
				{
					Canon: series.CanonSeason{SeasonNumber: 2},
					Episodes: []EpisodeDetail{
						makeEp(2, 3, &tomorrow, true, nil),
					},
				},
				{
					Canon: series.CanonSeason{SeasonNumber: 1},
					Episodes: []EpisodeDetail{
						makeEp(1, 9, &tomorrow, true, nil),
					},
				},
			},
			wantSeason:  1,
			wantEpisode: 9,
			wantAir:     &tomorrow,
		},
		{
			name: "no future episode anywhere returns nil",
			seasons: []SeasonDetail{
				{
					Canon: series.CanonSeason{SeasonNumber: 1},
					Episodes: []EpisodeDetail{
						makeEp(1, 1, &yesterday, true, nil),
						makeEp(1, 2, nil, true, nil),
					},
				},
			},
			wantNil: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			d := &Detail{Seasons: tc.seasons}
			got := pickNextEpisode(d, now)
			if tc.wantNil {
				require.Nil(t, got)
				return
			}
			require.NotNil(t, got)
			require.Equal(t, tc.wantSeason, got.SeasonNumber)
			require.Equal(t, tc.wantEpisode, got.EpisodeNumber)
			require.NotNil(t, got.AirDate)
			require.Equal(t, *tc.wantAir, *got.AirDate)
			if tc.wantTitle != nil {
				require.NotNil(t, got.Title)
				require.Equal(t, *tc.wantTitle, *got.Title)
			} else {
				require.Nil(t, got.Title)
			}
		})
	}
}

func TestPickNextEpisode_NilSafe(t *testing.T) {
	now := time.Date(2026, 6, 16, 12, 0, 0, 0, time.UTC)
	require.Nil(t, pickNextEpisode(nil, now))
	require.Nil(t, pickNextEpisode(&Detail{}, now))
}

// --- Story 377: SeasonStats wiring through composer ---

// TestComposer_LoadSeasonsAndEpisodes_AttachesSeasonStats — story 377.
// When the SeasonStats port returns rows for the (instance, sonarr_series_id),
// each SeasonDetail.Stats must be populated by season_number.
func TestComposer_LoadSeasonsAndEpisodes_AttachesSeasonStats(t *testing.T) {
	t.Parallel()
	deps, _, _ := baseDeps(t)
	deps.Seasons = &fakeSeasons{rows: []series.CanonSeason{
		{ID: 1, SeriesID: 42, SeasonNumber: 1},
		{ID: 2, SeriesID: 42, SeasonNumber: 2},
	}}
	deps.SeasonStats = &fakeSeasonStatsPort{rows: []series.SeasonStat{
		{
			InstanceName: "alpha", SonarrSeriesID: 1, SeasonNumber: 1,
			EpisodeFileCount: 10, AiredEpisodeCount: 10, TotalEpisodeCount: 10,
			Monitored: true,
		},
	}}
	c := NewComposer(deps)
	d, err := c.Get(context.Background(), "alpha", 1, "en-US")
	require.NoError(t, err)
	require.Len(t, d.Seasons, 2)
	require.NotNil(t, d.Seasons[0].Stats, "season 1 must have stats attached")
	require.Equal(t, 10, d.Seasons[0].Stats.EpisodeFileCount)
	require.Equal(t, 10, d.Seasons[0].Stats.AiredEpisodeCount)
	require.True(t, d.Seasons[0].Stats.Monitored)
	require.Nil(t, d.Seasons[1].Stats, "season 2 must have no stats row")
}

// TestComposer_LoadSeasonsAndEpisodes_SeasonStatsNilPort — nil port
// must NOT break the composer; SeasonDetail.Stats stays nil and the
// handler falls back to the episode_states walk.
func TestComposer_LoadSeasonsAndEpisodes_SeasonStatsNilPort(t *testing.T) {
	t.Parallel()
	deps, _, _ := baseDeps(t)
	deps.Seasons = &fakeSeasons{rows: []series.CanonSeason{
		{ID: 1, SeriesID: 42, SeasonNumber: 1},
	}}
	deps.SeasonStats = nil
	c := NewComposer(deps)
	d, err := c.Get(context.Background(), "alpha", 1, "en-US")
	require.NoError(t, err)
	require.Len(t, d.Seasons, 1)
	require.Nil(t, d.Seasons[0].Stats, "nil SeasonStats port must not surface a Stats row")
}

// TestComposer_LoadSeasonsAndEpisodes_SeasonStatsError_Degrades — port
// error is warn-logged and degraded; Stats stays nil but the composer
// still returns the seasons slice.
func TestComposer_LoadSeasonsAndEpisodes_SeasonStatsError_Degrades(t *testing.T) {
	t.Parallel()
	deps, _, _ := baseDeps(t)
	deps.Seasons = &fakeSeasons{rows: []series.CanonSeason{
		{ID: 1, SeriesID: 42, SeasonNumber: 1},
	}}
	deps.SeasonStats = &fakeSeasonStatsPort{err: errors.New("db down")}
	c := NewComposer(deps)
	d, err := c.Get(context.Background(), "alpha", 1, "en-US")
	require.NoError(t, err)
	require.Len(t, d.Seasons, 1)
	require.Nil(t, d.Seasons[0].Stats, "season_stats error must degrade silently with Stats nil")
}

// --- Story 379: pickInProgress table test ---

func TestPickInProgress(t *testing.T) {
	mk := func(season, ep int, status string, size, left int64, title string) QueueRecordDetail {
		return QueueRecordDetail{
			SeasonNumber:  season,
			EpisodeNumber: ep,
			Status:        status,
			Size:          size,
			SizeLeft:      left,
			Title:         title,
		}
	}
	tests := []struct {
		name        string
		records     []QueueRecordDetail
		wantNil     bool
		wantSeason  int
		wantEpisode int
		wantPercent int
	}{
		{"downloading wins over queued", []QueueRecordDetail{mk(5, 1, "queued", 1000, 200, ""), mk(5, 3, "downloading", 1000, 700, "S05E03")}, false, 5, 3, 30},
		{"highest percent wins", []QueueRecordDetail{mk(1, 1, "downloading", 1000, 550, ""), mk(1, 2, "downloading", 1000, 200, "")}, false, 1, 2, 80},
		{"tie broken by season ASC", []QueueRecordDetail{mk(5, 1, "downloading", 1000, 500, ""), mk(2, 1, "downloading", 1000, 500, "")}, false, 2, 1, 50},
		{"tie broken by episode ASC within season", []QueueRecordDetail{mk(5, 7, "downloading", 1000, 500, ""), mk(5, 3, "downloading", 1000, 500, "")}, false, 5, 3, 50},
		{"zero size yields zero percent", []QueueRecordDetail{mk(1, 1, "downloading", 0, 0, "")}, false, 1, 1, 0},
		{"all queued returns nil", []QueueRecordDetail{mk(1, 1, "queued", 1000, 800, ""), mk(1, 2, "warning", 1000, 800, "")}, true, 0, 0, 0},
		{"empty queue returns nil", nil, true, 0, 0, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := &Detail{QueueRecords: tt.records}
			got := pickInProgress(d)
			if tt.wantNil {
				require.Nil(t, got)
				return
			}
			require.NotNil(t, got)
			require.Equal(t, tt.wantSeason, got.SeasonNumber)
			require.Equal(t, tt.wantEpisode, got.EpisodeNumber)
			require.Equal(t, tt.wantPercent, got.Percent)
		})
	}
}

func TestPickInProgress_NilSafe(t *testing.T) {
	require.Nil(t, pickInProgress(nil))
	require.Nil(t, pickInProgress(&Detail{}))
}
