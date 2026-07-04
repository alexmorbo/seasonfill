package seriesdetail

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/internal/catalog/domain/series"
	"github.com/alexmorbo/seasonfill/internal/enrichment/domain/enrichment"
	"github.com/alexmorbo/seasonfill/internal/enrichment/domain/people"
	"github.com/alexmorbo/seasonfill/internal/enrichment/domain/taxonomy"
	"github.com/alexmorbo/seasonfill/internal/shared/clients/sonarr"
	ports "github.com/alexmorbo/seasonfill/internal/shared/dataports"
	database "github.com/alexmorbo/seasonfill/internal/shared/db"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
	"github.com/alexmorbo/seasonfill/internal/shared/media"
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

func (f *fakeSeriesCache) ListBySeriesIDs(_ context.Context, ids []domain.SeriesID) (map[domain.SeriesID][]series.CacheEntry, error) {
	if f.listErr != nil {
		return nil, f.listErr
	}
	out := make(map[domain.SeriesID][]series.CacheEntry, len(ids))
	for _, id := range ids {
		if rows, ok := f.byCanon[id]; ok && len(rows) > 0 {
			out[id] = rows
		}
	}
	return out, nil
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

func (f *fakeSeries) ListByIDs(_ context.Context, ids []domain.SeriesID) ([]series.Canon, error) {
	if f.err != nil {
		return nil, f.err
	}
	out := make([]series.Canon, 0, len(ids))
	for _, id := range ids {
		if c, ok := f.rows[id]; ok {
			out = append(out, c)
		}
	}
	return out, nil
}

func (f *fakeSeries) ListByTMDBIDs(_ context.Context, tmdbIDs []domain.TMDBID) ([]series.Canon, error) {
	if f.err != nil {
		return nil, f.err
	}
	out := make([]series.Canon, 0, len(tmdbIDs))
	for _, id := range tmdbIDs {
		for _, c := range f.rows {
			if c.TMDBID != nil && *c.TMDBID == id {
				out = append(out, c)
				break
			}
		}
	}
	return out, nil
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

// ListByIDsWithFallback — Story 565 (B-recs-lang). Applies the same
// two-tier lookup semantics as GetWithFallback across every requested id.
func (f *fakeSeriesTexts) ListByIDsWithFallback(_ context.Context, ids []domain.SeriesID, lang string) (map[domain.SeriesID]series.SeriesText, error) {
	if f.err != nil {
		return nil, f.err
	}
	out := make(map[domain.SeriesID]series.SeriesText, len(ids))
	for _, id := range ids {
		if t, ok := f.rows[seriesTextKey(id, lang)]; ok {
			out[id] = t
			continue
		}
		if t, ok := f.rows[seriesTextKey(id, "en-US")]; ok {
			out[id] = t
		}
	}
	return out, nil
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

type fakeEpisodeTexts struct {
	// byEpisode optionally seeds individual rows. Empty/nil → every
	// lookup returns ErrNotFound (preserves prior fake behaviour).
	byEpisode map[domain.EpisodeID]series.EpisodeText
}

func (f fakeEpisodeTexts) GetWithFallback(_ context.Context, episodeID domain.EpisodeID, _ string) (series.EpisodeText, error) {
	if t, ok := f.byEpisode[episodeID]; ok {
		return t, nil
	}
	return series.EpisodeText{}, ports.ErrNotFound
}

func (f fakeEpisodeTexts) ListByEpisodeIDsWithFallback(_ context.Context, episodeIDs []domain.EpisodeID, _ string) (map[domain.EpisodeID]series.EpisodeText, error) {
	if len(episodeIDs) == 0 || len(f.byEpisode) == 0 {
		return map[domain.EpisodeID]series.EpisodeText{}, nil
	}
	out := make(map[domain.EpisodeID]series.EpisodeText, len(episodeIDs))
	for _, id := range episodeIDs {
		if t, ok := f.byEpisode[id]; ok {
			out[id] = t
		}
	}
	return out, nil
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

func (f *fakeSeriesPeople) ListBySeries(_ context.Context, _ domain.SeriesID, _ people.SeriesCreditKind, _ string) ([]people.SeriesCredit, error) {
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
func (f *fakeGenres) ListByIDsWithFallback(_ context.Context, ids []int64, lang string) ([]taxonomy.Genre, error) {
	out := make([]taxonomy.Genre, 0, len(ids))
	for _, id := range ids {
		out = append(out, taxonomy.Genre{ID: id, Name: "Drama", Language: lang})
	}
	return out, nil
}

type fakeKeywords struct{ ids []int64 }

func (f *fakeKeywords) ListBySeries(_ context.Context, _ domain.SeriesID) ([]int64, error) {
	return f.ids, nil
}
func (f *fakeKeywords) Get(_ context.Context, id int64, lang string) (taxonomy.Keyword, error) {
	return taxonomy.Keyword{ID: id, Name: "kw", Language: lang}, nil
}
func (f *fakeKeywords) ListByIDsWithFallback(_ context.Context, ids []int64, lang string) ([]taxonomy.Keyword, error) {
	out := make([]taxonomy.Keyword, 0, len(ids))
	for _, id := range ids {
		out = append(out, taxonomy.Keyword{ID: id, Name: "kw", Language: lang})
	}
	return out, nil
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

// fakeFreshness implements EnrichmentFreshnessPort for composer tests.
// syncedBySource maps Source → last-success timestamp (the production
// adapter reads canon.enrichment_*_synced_at for tmdb_series + omdb;
// for tmdb_season / tmdb_person the canon row doesn't carry a column
// so the adapter returns nil — but the test fake lets a test pretend
// either case to exercise rule 1 / rule 3 branches).
//
// errors lets a test return live error rows keyed by source.
type fakeFreshness struct {
	syncedBySource map[enrichment.Source]*time.Time
	errors         []enrichment.EnrichmentError
}

func (f *fakeFreshness) SyncedAtFor(_ context.Context, _ domain.SeriesID, src enrichment.Source) (*time.Time, error) {
	if t, ok := f.syncedBySource[src]; ok {
		return t, nil
	}
	return nil, nil
}

func (f *fakeFreshness) ErrorsFor(_ context.Context, _ domain.SeriesID) ([]enrichment.EnrichmentError, error) {
	return f.errors, nil
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
			42: {ID: 42, OriginalTitle: new("Breaking Bad"), Year: new(2008), Status: new("Ended")},
			99: {ID: 99, OriginalTitle: new("Recommended Show")},
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
		Freshness:         &fakeFreshness{syncedBySource: map[enrichment.Source]*time.Time{}},
		SonarrFor: func(_ domain.InstanceName) (SonarrQueueLister, bool) {
			return fakeSonarrQueueLister{payload: sonarr.QueuePayload{}}, true
		},
		Logger: newSilentLogger(),
		Now:    func() time.Time { return now },
	}
	return deps, cache, canon
}

// --- tests ---

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

// C-season-fix — the single-season path must honour season_texts for the
// season name/overview, not return the canon (RU-written) row verbatim.
func TestComposer_GetSeason_LocalizedRow_OverridesCanon(t *testing.T) {
	deps, _, _ := baseDeps(t)
	deps.Seasons = &fakeSeasons{rows: []series.CanonSeason{
		{ID: 1, SeriesID: 42, SeasonNumber: 1},
	}}
	deps.SeasonTexts = &seasonsFakeTexts{rows: map[int]series.SeasonText{
		1: {SeriesID: 42, SeasonNumber: 1, Language: "ru-RU", Name: new("Сезон один"), Overview: new("Локализованное описание")},
	}}
	c := NewComposer(deps)
	d, err := c.GetSeason(context.Background(), "alpha", 1, 1, "ru-RU")
	require.NoError(t, err)
	require.Len(t, d.Seasons, 1)
	require.NotNil(t, d.Seasons[0].Name)
	require.Equal(t, "Сезон один", *d.Seasons[0].Name)
	require.NotNil(t, d.Seasons[0].Overview)
	require.Equal(t, "Локализованное описание", *d.Seasons[0].Overview)
}

// THE OPERATOR BUG: EN request must NOT return the RU text. In prod the CANON
// season row was authored in RU (Sonarr / older enrichment wrote RU into
// seasons.name/overview). The B3b worker populates season_texts with the en-US
// row; under ?lang=en-US the repo two-tier resolves that en-US row, so the
// composer MUST override canon-RU with it. Pre-change GetSeason returned canon
// verbatim → RU leaked to EN clients (the bug). This test FAILS against
// pre-change code (canon-RU) and PASSES post-change (en-US override).
func TestComposer_GetSeason_EN_DoesNotGetRU(t *testing.T) {
	deps, _, _ := baseDeps(t)
	// Canon is RU — the exact prod condition that caused the operator report.
	deps.Seasons = &fakeSeasons{rows: []series.CanonSeason{
		{ID: 1, SeriesID: 42, SeasonNumber: 1},
	}}
	// season_texts en-US row (repo already applied the en-US two-tier resolve).
	deps.SeasonTexts = &seasonsFakeTexts{rows: map[int]series.SeasonText{
		1: {SeriesID: 42, SeasonNumber: 1, Language: "en-US", Name: new("Season 1"), Overview: new("English overview")},
	}}
	c := NewComposer(deps)
	d, err := c.GetSeason(context.Background(), "alpha", 1, 1, "en-US")
	require.NoError(t, err)
	require.Len(t, d.Seasons, 1)
	require.NotNil(t, d.Seasons[0].Name)
	require.Equal(t, "Season 1", *d.Seasons[0].Name)
	require.NotEqual(t, "Сезон 1", *d.Seasons[0].Name)
	require.NotNil(t, d.Seasons[0].Overview)
	require.Equal(t, "English overview", *d.Seasons[0].Overview)
	require.NotEqual(t, "Русское описание", *d.Seasons[0].Overview)
}

// S-E2 — localized row entirely absent (empty map) with the SeasonTexts
// dep wired → canon name/overview are cleared to nil (canon is no longer
// a tier-3 fallback; FE renders the numbered-label placeholder).
func TestComposer_GetSeason_NoTextsRow_BlankNotCanon(t *testing.T) {
	deps, _, _ := baseDeps(t)
	deps.Seasons = &fakeSeasons{rows: []series.CanonSeason{
		{ID: 1, SeriesID: 42, SeasonNumber: 1},
	}}
	deps.SeasonTexts = &seasonsFakeTexts{rows: map[int]series.SeasonText{}}
	c := NewComposer(deps)
	d, err := c.GetSeason(context.Background(), "alpha", 1, 1, "ru-RU")
	require.NoError(t, err)
	require.Len(t, d.Seasons, 1)
	require.Nil(t, d.Seasons[0].Name, "no texts row → nil, NEVER canon")
	require.Nil(t, d.Seasons[0].Overview)
}

// S-E2 NULL/error pair. The nil-dep case is back-compat: when the
// SeasonTexts port is unwired the composer never stages, so the read-model
// name/overview stay nil. A wired dep that ERRORS also degrades to nil texts.
// S-E3a removed the canon name/overview tier, so both cases yield nil (no canon
// fallback). Both return a nil GetSeason error and never panic.
func TestComposer_GetSeason_SeasonTexts_NilAndError(t *testing.T) {
	canonRows := []series.CanonSeason{
		{ID: 1, SeriesID: 42, SeasonNumber: 1},
	}

	t.Run("nil dep yields nil (no canon fallback)", func(t *testing.T) {
		deps, _, _ := baseDeps(t)
		deps.Seasons = &fakeSeasons{rows: canonRows}
		deps.SeasonTexts = nil
		c := NewComposer(deps)
		d, err := c.GetSeason(context.Background(), "alpha", 1, 1, "ru-RU")
		require.NoError(t, err)
		require.Len(t, d.Seasons, 1)
		// S-E3a — canon season carries no name/overview; an unwired SeasonTexts
		// port leaves the staged fields nil (no canon fallback tier).
		require.Nil(t, d.Seasons[0].Name, "unwired dep → nil, no canon fallback")
		require.Nil(t, d.Seasons[0].Overview)
	})

	t.Run("repo error clears canon", func(t *testing.T) {
		deps, _, _ := baseDeps(t)
		deps.Seasons = &fakeSeasons{rows: canonRows}
		deps.SeasonTexts = &seasonsFakeTexts{err: errors.New("season_texts db down")}
		c := NewComposer(deps)
		d, err := c.GetSeason(context.Background(), "alpha", 1, 1, "ru-RU")
		require.NoError(t, err)
		require.Len(t, d.Seasons, 1)
		require.Nil(t, d.Seasons[0].Name, "wired dep + repo error → nil, NEVER canon")
		require.Nil(t, d.Seasons[0].Overview)
	})
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

type ensurePendingCall struct {
	hash, sourceURL, kind string
}

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

// TestComposer_ResolveAssets_BelowFold_NilOnMiss — S-E3a: resolveAssets no
// longer touches the hero (series poster/backdrop live in series_media_texts,
// resolved by SkeletonComposer). This path resolves only below-the-fold art:
// season posters (staged on SeasonDetail), recommendation posters (staged on
// RecommendationDetail), network logos and cast profiles — each hash-on-hit,
// nil-on-miss, NEVER the raw TMDB path. All-miss lookup → every field nil and
// no eager EnsurePending side effects (hero was the only eager path).
func TestComposer_ResolveAssets_BelowFold_NilOnMiss(t *testing.T) {
	t.Parallel()
	d := &Detail{
		Canon:    series.Canon{ID: 42, OriginalTitle: new("Breaking Bad")},
		Networks: []taxonomy.Network{{ID: 1, Name: "AMC", LogoAsset: new("/net.png")}},
		Cast: []CastDetail{
			{Person: people.Person{ID: 1, Name: "Bryan Cranston", ProfileAsset: new("/p1.jpg")}},
		},
		Recommendations: []RecommendationDetail{
			{Series: series.Canon{ID: 99}, PosterAsset: new("/rec1.jpg")},
		},
		Seasons: []SeasonDetail{
			{Canon: series.CanonSeason{ID: 1, SeriesID: 42, SeasonNumber: 1}, PosterAsset: new("/s1.jpg")},
		},
	}
	lookup := &fakeMediaLookupIntegration{byURL: map[string]string{}}
	resolver := media.NewResolver(lookup, nil, nil, newSilentLogger())
	c := NewComposer(Deps{MediaResolver: resolver})
	c.resolveAssets(context.Background(), d)

	require.Nil(t, d.Networks[0].LogoAsset)
	require.Nil(t, d.Cast[0].Person.ProfileAsset)
	require.Nil(t, d.Recommendations[0].PosterAsset)
	require.Nil(t, d.Seasons[0].PosterAsset)

	require.Empty(t, lookup.ensureCalls, "hero was the only eager path; below-fold miss adds no EnsurePending")
}

// TestComposer_ResolveAssets_EpisodeStills exercises the story 322 wiring:
// every episode still in every season gets r.Resolve at w300, so the wire
// field carries either a sha256 hex (lookup hit) OR nil (frontend renders
// monogram). NEVER the raw TMDB path.
func TestComposer_ResolveAssets_EpisodeStills(t *testing.T) {
	t.Parallel()
	d := &Detail{
		Canon: series.Canon{ID: 42, OriginalTitle: new("Breaking Bad")},
		Seasons: []SeasonDetail{
			{
				Canon: series.CanonSeason{ID: 1, SeriesID: 42, SeasonNumber: 1},
				Episodes: []EpisodeDetail{
					{Canon: series.CanonEpisode{ID: 10, SeasonNumber: 1, EpisodeNumber: 1, StillAsset: new("/ep1.jpg")}},
					{Canon: series.CanonEpisode{ID: 11, SeasonNumber: 1, EpisodeNumber: 2, StillAsset: new("/ep2.jpg")}},
				},
			},
			{
				Canon: series.CanonSeason{ID: 2, SeriesID: 42, SeasonNumber: 2},
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
	resolver := media.NewResolver(lookup, nil, nil, newSilentLogger())
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
		Canon: series.Canon{ID: 42, OriginalTitle: new("Breaking Bad")},
		Seasons: []SeasonDetail{
			// S-E3a — season poster raw path is staged on SeasonDetail.PosterAsset.
			{Canon: series.CanonSeason{ID: 1, SeriesID: 42, SeasonNumber: 1}, PosterAsset: new("/seasonA.jpg")},
			{Canon: series.CanonSeason{ID: 2, SeriesID: 42, SeasonNumber: 2}, PosterAsset: new("/seasonB.jpg")},
		},
	}
	const hashA = "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"
	lookup := &fakeMediaLookupIntegration{byURL: map[string]string{
		"https://image.tmdb.org/t/p/w154/seasonA.jpg": hashA,
	}}
	resolver := media.NewResolver(lookup, nil, nil, newSilentLogger())
	c := NewComposer(Deps{MediaResolver: resolver})
	c.resolveAssets(context.Background(), d)
	require.NotNil(t, d.Seasons[0].PosterAsset)
	require.Equal(t, hashA, *d.Seasons[0].PosterAsset)
	require.Nil(t, d.Seasons[1].PosterAsset, "miss must return nil, NOT raw path")
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
	// B1b-2 — driven directly against the preserved loadSeasonsAndEpisodes
	// (the fat Composer.Get was deleted at the B1b cutover). Same-package
	// white-box call preserves the season-stats-attach coverage.
	d := &Detail{Instance: "alpha", SonarrSeriesID: 1, SeriesID: 42}
	err := c.loadSeasonsAndEpisodes(context.Background(), d, "en-US")
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
	d := &Detail{Instance: "alpha", SonarrSeriesID: 1, SeriesID: 42}
	err := c.loadSeasonsAndEpisodes(context.Background(), d, "en-US")
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
	d := &Detail{Instance: "alpha", SonarrSeriesID: 1, SeriesID: 42}
	err := c.loadSeasonsAndEpisodes(context.Background(), d, "en-US")
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

// --- Story 533a: GetCanonicalSeasons / GetCanonicalCast ---

// baseCanonicalDeps returns a Deps tailored for the canon-only methods.
// Per-instance ports (EpisodeStates, SeasonStats, SeriesCache, etc.) are
// nil because GetCanonicalSeasons/GetCanonicalCast do not touch them.
func baseCanonicalDeps() Deps {
	return Deps{
		Seasons:      &fakeSeasons{},
		Episodes:     &fakeEpisodes{},
		EpisodeTexts: fakeEpisodeTexts{},
		SeriesPeople: &fakeSeriesPeople{},
		People:       &fakePeople{},
		Logger:       newSilentLogger(),
		Now:          func() time.Time { return time.Now().UTC() },
	}
}

func TestComposer_GetCanonicalSeasons_HappyPath(t *testing.T) {
	deps := baseCanonicalDeps()
	deps.Seasons = &fakeSeasons{rows: []series.CanonSeason{
		{SeasonNumber: 1},
		{SeasonNumber: 2},
	}}
	deps.Episodes = &fakeEpisodes{rows: []series.CanonEpisode{
		{ID: 11, SeasonNumber: 1, EpisodeNumber: 1},
		{ID: 12, SeasonNumber: 1, EpisodeNumber: 2},
		{ID: 21, SeasonNumber: 2, EpisodeNumber: 1},
	}}
	c := NewComposer(deps)
	got, err := c.GetCanonicalSeasons(context.Background(), 100, "en-US")
	require.NoError(t, err)
	require.Len(t, got, 2)
	require.Len(t, got[0].Episodes, 2)
	require.Len(t, got[1].Episodes, 1)
	require.Nil(t, got[0].Stats, "fallback path must not load SeasonStats")
	require.Nil(t, got[0].Episodes[0].State, "fallback path must not load EpisodeStates")
}

func TestComposer_GetCanonicalSeasons_NoSeasonsReturnsEmpty(t *testing.T) {
	deps := baseCanonicalDeps()
	c := NewComposer(deps)
	got, err := c.GetCanonicalSeasons(context.Background(), 100, "en-US")
	require.NoError(t, err)
	require.NotNil(t, got, "empty must be non-nil slice")
	require.Len(t, got, 0)
}

func TestComposer_GetCanonicalSeasons_SeasonsError_Propagates(t *testing.T) {
	deps := baseCanonicalDeps()
	deps.Seasons = &fakeSeasons{err: errors.New("seasons boom")}
	c := NewComposer(deps)
	_, err := c.GetCanonicalSeasons(context.Background(), 100, "en-US")
	require.Error(t, err)
}

func TestComposer_GetCanonicalSeasons_EpisodesError_Propagates(t *testing.T) {
	deps := baseCanonicalDeps()
	deps.Seasons = &fakeSeasons{rows: []series.CanonSeason{{SeasonNumber: 1}}}
	deps.Episodes = &fakeEpisodes{err: errors.New("eps boom")}
	c := NewComposer(deps)
	_, err := c.GetCanonicalSeasons(context.Background(), 100, "en-US")
	require.Error(t, err)
}

// fakeSeasonMediaTexts stages a per-season localized poster raw path so the
// canon-seasons path has a PosterAsset to resolve.
type fakeSeasonMediaTexts struct {
	rows map[int]series.SeasonMediaText
	err  error
}

func (f fakeSeasonMediaTexts) ListBySeriesWithFallback(_ context.Context, _ domain.SeriesID, _ string) (map[int]series.SeasonMediaText, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.rows, nil
}

// TestComposer_GetCanonicalSeasons_AsyncSeasonPoster locks the W16-5 contract:
// GetCanonicalSeasons resolves season posters via the ASYNC Resolve
// (hash-on-hit, nil-on-miss) — NOT the old blocking ResolveSync unified path.
// A lookup HIT returns the sha256 hash; a MISS returns nil (frontend renders a
// monogram), never the raw TMDB path.
func TestComposer_GetCanonicalSeasons_AsyncSeasonPoster(t *testing.T) {
	deps := baseCanonicalDeps()
	deps.Seasons = &fakeSeasons{rows: []series.CanonSeason{
		{SeasonNumber: 1},
		{SeasonNumber: 2},
	}}
	deps.Episodes = &fakeEpisodes{rows: []series.CanonEpisode{
		{ID: 11, SeasonNumber: 1, EpisodeNumber: 1},
		{ID: 21, SeasonNumber: 2, EpisodeNumber: 1},
	}}
	deps.SeasonMediaTexts = fakeSeasonMediaTexts{rows: map[int]series.SeasonMediaText{
		1: {SeasonNumber: 1, PosterAsset: new("/seasonHit.jpg")},
		2: {SeasonNumber: 2, PosterAsset: new("/seasonMiss.jpg")},
	}}
	const hashHit = "dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd"
	lookup := &fakeMediaLookupIntegration{byURL: map[string]string{
		"https://image.tmdb.org/t/p/w154/seasonHit.jpg": hashHit,
		// seasonMiss.jpg intentionally absent — async Resolve returns nil.
	}}
	deps.MediaResolver = media.NewResolver(lookup, nil, nil, newSilentLogger())

	c := NewComposer(deps)
	got, err := c.GetCanonicalSeasons(context.Background(), 100, "en-US")
	require.NoError(t, err)
	require.Len(t, got, 2)
	require.NotNil(t, got[0].PosterAsset, "hit season poster must resolve to a hash")
	require.Equal(t, hashHit, *got[0].PosterAsset)
	require.Nil(t, got[1].PosterAsset, "miss must return nil, NOT the raw TMDB path")
}

func TestComposer_GetCanonicalSeason_ReturnsRequestedSeasonWithEpisodeTexts(t *testing.T) {
	deps := baseCanonicalDeps()
	deps.Seasons = &fakeSeasons{rows: []series.CanonSeason{
		{SeasonNumber: 1},
		{SeasonNumber: 2},
	}}
	deps.Episodes = &fakeEpisodes{rows: []series.CanonEpisode{
		{ID: 11, SeasonNumber: 1, EpisodeNumber: 1},
		{ID: 21, SeasonNumber: 2, EpisodeNumber: 1},
		{ID: 22, SeasonNumber: 2, EpisodeNumber: 2},
	}}
	ruTitle := "Эпизод"
	deps.EpisodeTexts = fakeEpisodeTexts{byEpisode: map[domain.EpisodeID]series.EpisodeText{
		21: {EpisodeID: 21, Language: "ru-RU", Title: &ruTitle},
	}}
	c := NewComposer(deps)

	got, ok, err := c.GetCanonicalSeason(context.Background(), 100, 2, "ru-RU")
	require.NoError(t, err)
	require.True(t, ok, "requested season must be found")
	require.Equal(t, 2, got.Canon.SeasonNumber)
	require.Len(t, got.Episodes, 2)
	require.Nil(t, got.Episodes[0].State, "canon-only path must not load EpisodeStates")
	require.NotNil(t, got.Episodes[0].Text, "episode_texts row must be staged")
	require.Equal(t, "Эпизод", *got.Episodes[0].Text.Title)
	require.Nil(t, got.Episodes[1].Text, "episode without a texts row stays blank (graceful)")
}

func TestComposer_GetCanonicalSeason_UnknownSeasonReturnsNotOK(t *testing.T) {
	deps := baseCanonicalDeps()
	deps.Seasons = &fakeSeasons{rows: []series.CanonSeason{{SeasonNumber: 1}}}
	deps.Episodes = &fakeEpisodes{rows: []series.CanonEpisode{{ID: 11, SeasonNumber: 1, EpisodeNumber: 1}}}
	c := NewComposer(deps)

	_, ok, err := c.GetCanonicalSeason(context.Background(), 100, 9, "en-US")
	require.NoError(t, err)
	require.False(t, ok, "a missing season is a graceful (nil-error) not-found")
}

func TestComposer_GetCanonicalSeason_SeasonsError_Propagates(t *testing.T) {
	deps := baseCanonicalDeps()
	deps.Seasons = &fakeSeasons{err: errors.New("seasons boom")}
	c := NewComposer(deps)

	_, _, err := c.GetCanonicalSeason(context.Background(), 100, 1, "en-US")
	require.Error(t, err)
}

func TestComposer_GetCanonicalCast_TopN(t *testing.T) {
	// Build 15 credits + matching persons; expect CastDefaultLimit (10) returned.
	credits := make([]people.SeriesCredit, 0, 15)
	persons := make([]people.Person, 0, 15)
	for i := 1; i <= 15; i++ {
		order := i
		credits = append(credits, people.SeriesCredit{PersonID: int64(i), CreditOrder: &order})
		persons = append(persons, people.Person{ID: int64(i)})
	}
	deps := baseCanonicalDeps()
	deps.SeriesPeople = &fakeSeriesPeople{rows: credits}
	deps.People = &fakePeople{rows: persons}
	c := NewComposer(deps)
	got, err := c.GetCanonicalCast(context.Background(), 100, "en-US", 0)
	require.NoError(t, err)
	require.Len(t, got, CastDefaultLimit)
}

func TestComposer_GetCanonicalCast_NoCreditsReturnsEmpty(t *testing.T) {
	deps := baseCanonicalDeps()
	c := NewComposer(deps)
	got, err := c.GetCanonicalCast(context.Background(), 100, "en-US", 0)
	require.NoError(t, err)
	require.NotNil(t, got)
	require.Len(t, got, 0)
}

func TestComposer_GetCanonicalCast_DropsMissingPeopleRow(t *testing.T) {
	deps := baseCanonicalDeps()
	deps.SeriesPeople = &fakeSeriesPeople{rows: []people.SeriesCredit{
		{PersonID: 1},
		{PersonID: 2},
	}}
	// Only PersonID=1 has a corresponding people row → cast list shrinks.
	deps.People = &fakePeople{rows: []people.Person{{ID: 1}}}
	c := NewComposer(deps)
	got, err := c.GetCanonicalCast(context.Background(), 100, "en-US", 0)
	require.NoError(t, err)
	require.Len(t, got, 1)
	require.Equal(t, int64(1), got[0].Person.ID)
}

func TestComposer_GetCanonicalCast_ExplicitLimit(t *testing.T) {
	credits := make([]people.SeriesCredit, 0, 5)
	persons := make([]people.Person, 0, 5)
	for i := 1; i <= 5; i++ {
		credits = append(credits, people.SeriesCredit{PersonID: int64(i)})
		persons = append(persons, people.Person{ID: int64(i)})
	}
	deps := baseCanonicalDeps()
	deps.SeriesPeople = &fakeSeriesPeople{rows: credits}
	deps.People = &fakePeople{rows: persons}
	c := NewComposer(deps)
	got, err := c.GetCanonicalCast(context.Background(), 100, "en-US", 3)
	require.NoError(t, err)
	require.Len(t, got, 3, "explicit positive limit must cap output")
}

func TestComposer_GetCanonicalCast_SeriesPeopleError_Propagates(t *testing.T) {
	deps := baseCanonicalDeps()
	deps.SeriesPeople = &fakeSeriesPeople{err: errors.New("people boom")}
	c := NewComposer(deps)
	_, err := c.GetCanonicalCast(context.Background(), 100, "en-US", 0)
	require.Error(t, err)
}

// W15-9 — served-language contract on the season detail (the requested
// season's name/overview row language).
func TestComposer_GetSeason_ServedLanguage(t *testing.T) {
	t.Run("fallback name lang surfaced", func(t *testing.T) {
		deps, _, _ := baseDeps(t)
		deps.Seasons = &fakeSeasons{rows: []series.CanonSeason{{ID: 1, SeriesID: 42, SeasonNumber: 1}}}
		deps.SeasonTexts = &seasonsFakeTexts{rows: map[int]series.SeasonText{
			1: {SeriesID: 42, SeasonNumber: 1, Language: "en-US", Name: new("Season 1")},
		}}
		d, err := NewComposer(deps).GetSeason(context.Background(), "alpha", 1, 1, "ru-RU")
		require.NoError(t, err)
		require.Equal(t, "en-US", d.ServedLanguage)
	})

	t.Run("requested lang → served=requested", func(t *testing.T) {
		deps, _, _ := baseDeps(t)
		deps.Seasons = &fakeSeasons{rows: []series.CanonSeason{{ID: 1, SeriesID: 42, SeasonNumber: 1}}}
		deps.SeasonTexts = &seasonsFakeTexts{rows: map[int]series.SeasonText{
			1: {SeriesID: 42, SeasonNumber: 1, Language: "ru-RU", Name: new("Сезон 1")},
		}}
		d, err := NewComposer(deps).GetSeason(context.Background(), "alpha", 1, 1, "ru-RU")
		require.NoError(t, err)
		require.Equal(t, "ru-RU", d.ServedLanguage)
	})

	t.Run("no texts row → served empty", func(t *testing.T) {
		deps, _, _ := baseDeps(t)
		deps.Seasons = &fakeSeasons{rows: []series.CanonSeason{{ID: 1, SeriesID: 42, SeasonNumber: 1}}}
		deps.SeasonTexts = &seasonsFakeTexts{rows: map[int]series.SeasonText{}}
		d, err := NewComposer(deps).GetSeason(context.Background(), "alpha", 1, 1, "ru-RU")
		require.NoError(t, err)
		require.Empty(t, d.ServedLanguage)
	})
}
