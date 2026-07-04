package seriesdetail

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/internal/catalog/domain/series"
	"github.com/alexmorbo/seasonfill/internal/enrichment/domain/taxonomy"
	enrichpersistence "github.com/alexmorbo/seasonfill/internal/enrichment/persistence"
	appmedia "github.com/alexmorbo/seasonfill/internal/mediaproxy/app"
	"github.com/alexmorbo/seasonfill/internal/seriesdetail/app/freshener"
	dataports "github.com/alexmorbo/seasonfill/internal/shared/dataports"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
	"github.com/alexmorbo/seasonfill/internal/shared/domain/values"
	"github.com/alexmorbo/seasonfill/internal/shared/media"
)

// --- skeleton-local fakes ---

type fakeSkSeries struct {
	canon series.Canon
	err   error
}

func (f *fakeSkSeries) Get(_ context.Context, _ domain.SeriesID) (series.Canon, error) {
	return f.canon, f.err
}
func (f *fakeSkSeries) GetByTMDBID(context.Context, domain.TMDBID) (series.Canon, error) {
	return series.Canon{}, errors.New("unused")
}
func (f *fakeSkSeries) ListByIDs(context.Context, []domain.SeriesID) ([]series.Canon, error) {
	return nil, nil
}
func (f *fakeSkSeries) ListByTMDBIDs(context.Context, []domain.TMDBID) ([]series.Canon, error) {
	return nil, nil
}

type fakeSkSeriesTexts struct {
	row series.SeriesText
	err error
}

func (f *fakeSkSeriesTexts) GetWithFallback(_ context.Context, _ domain.SeriesID, _ string) (series.SeriesText, error) {
	return f.row, f.err
}
func (f *fakeSkSeriesTexts) ListByIDsWithFallback(context.Context, []domain.SeriesID, string) (map[domain.SeriesID]series.SeriesText, error) {
	return nil, nil
}

type fakeSkGenres struct {
	ids  []int64
	rows []taxonomy.Genre
}

func (f *fakeSkGenres) ListBySeries(context.Context, domain.SeriesID) ([]int64, error) {
	return f.ids, nil
}
func (f *fakeSkGenres) Get(context.Context, int64, string) (taxonomy.Genre, error) {
	return taxonomy.Genre{}, errors.New("unused")
}
func (f *fakeSkGenres) ListByIDsWithFallback(context.Context, []int64, string) ([]taxonomy.Genre, error) {
	return f.rows, nil
}

type fakeSkKeywords struct {
	ids  []int64
	rows []taxonomy.Keyword
}

func (f *fakeSkKeywords) ListBySeries(context.Context, domain.SeriesID) ([]int64, error) {
	return f.ids, nil
}
func (f *fakeSkKeywords) Get(context.Context, int64, string) (taxonomy.Keyword, error) {
	return taxonomy.Keyword{}, errors.New("unused")
}
func (f *fakeSkKeywords) ListByIDsWithFallback(context.Context, []int64, string) ([]taxonomy.Keyword, error) {
	return f.rows, nil
}

type fakeSkNetworks struct {
	ids  []int64
	rows []taxonomy.Network
}

func (f *fakeSkNetworks) ListBySeries(context.Context, domain.SeriesID) ([]int64, error) {
	return f.ids, nil
}
func (f *fakeSkNetworks) ListByIDs(context.Context, []int64) ([]taxonomy.Network, error) {
	return f.rows, nil
}

type fakeSkCompanies struct {
	ids  []int64
	rows []taxonomy.ProductionCompany
}

func (f *fakeSkCompanies) ListBySeries(context.Context, domain.SeriesID) ([]int64, error) {
	return f.ids, nil
}
func (f *fakeSkCompanies) ListByIDs(context.Context, []int64) ([]taxonomy.ProductionCompany, error) {
	return f.rows, nil
}

type fakeSkContentRatings struct {
	rows []enrichpersistence.ContentRating
}

func (f *fakeSkContentRatings) ListBySeries(context.Context, domain.SeriesID) ([]enrichpersistence.ContentRating, error) {
	return f.rows, nil
}

type fakeSkVideos struct {
	rows []enrichpersistence.Video
}

func (f *fakeSkVideos) ListBySeriesAndType(context.Context, domain.SeriesID, string) ([]enrichpersistence.Video, error) {
	return f.rows, nil
}

type fakeSkSeasons struct {
	rows []series.CanonSeason
}

func (f *fakeSkSeasons) ListBySeries(context.Context, domain.SeriesID) ([]series.CanonSeason, error) {
	return f.rows, nil
}

type fakeSkCacheLookup struct {
	rows []series.CacheEntry
	err  error
}

func (f *fakeSkCacheLookup) ListBySeriesID(context.Context, domain.SeriesID) ([]series.CacheEntry, error) {
	return f.rows, f.err
}
func (f *fakeSkCacheLookup) ListBySeriesIDs(context.Context, []domain.SeriesID) (map[domain.SeriesID][]series.CacheEntry, error) {
	return nil, nil
}

type fakeSkNextEpisode struct {
	ref NextEpisodeRef
	ok  bool
	err error
}

func (f *fakeSkNextEpisode) NextAired(context.Context, domain.SeriesID, string) (NextEpisodeRef, bool, error) {
	return f.ref, f.ok, f.err
}

// fakeSkMediaTexts is the Story 584b per-language poster/backdrop port
// fake. GetWithFallback returns the configured row/err; the batch method
// is unused by the skeleton path.
type fakeSkMediaTexts struct {
	row series.SeriesMediaText
	err error
}

func (f *fakeSkMediaTexts) GetWithFallback(context.Context, domain.SeriesID, string) (series.SeriesMediaText, error) {
	return f.row, f.err
}
func (f *fakeSkMediaTexts) ListByIDsWithFallback(context.Context, []domain.SeriesID, string) (map[domain.SeriesID]series.SeriesMediaText, error) {
	return nil, nil
}

// fakeSkMediaLookup is an always-miss HashLookupPort: HashForSourceURL
// returns ErrNotFound so ResolveSync takes the deterministic eager-hash
// path (sha256 of the built CDN URL). This lets a test assert WHICH raw
// path (per-lang vs canon) reached the resolver by comparing hashes.
type fakeSkMediaLookup struct{}

func (fakeSkMediaLookup) HashForSourceURL(context.Context, string) (string, error) {
	return "", dataports.ErrNotFound
}
func (fakeSkMediaLookup) EnsurePending(context.Context, string, string, string) error { return nil }

func skEagerHash(path, size string) string {
	return appmedia.HashFromURL(appmedia.BuildTMDBImageURL(size, path))
}

func skEagerResolver() *media.Resolver {
	return media.NewResolver(fakeSkMediaLookup{}, nil, nil, nil)
}

// spyFreshener records the EnsureFreshScope arguments so tests assert the
// skeleton section contract.
type spyFreshener struct {
	gotSections []freshener.Section
	gotLang     string
	gotMode     EnsureFreshMode
	result      FreshenResult
}

func (s *spyFreshener) EnsureFreshScope(_ context.Context, _ domain.SeriesID, lang string, sections []freshener.Section, _ []int, _ bool, mode EnsureFreshMode) (FreshenResult, error) {
	s.gotSections = sections
	s.gotLang = lang
	s.gotMode = mode
	return s.result, nil
}

func (s *spyFreshener) EnsureFresh(context.Context, domain.SeriesID, string) FreshenResult {
	return s.result
}

// --- helpers ---

func skBaseCanon() series.Canon {
	return series.Canon{
		ID:               42,
		Hydration:        series.HydrationFull,
		OriginalTitle:    new("Star City"),
		Status:           new("Returning Series"),
		Year:             new(2026),
		RuntimeMinutes:   new(60),
		OriginalLanguage: new("en"),
		OriginCountries:  []string{"US"},
		TMDBRating:       new(8.4),
		TMDBVotes:        new(1200),
		IMDBRating:       new(7.9),
		IMDBVotes:        new(4500),
		UpdatedAt:        time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC),
	}
}

func skBaseDeps(canon series.Canon) (SkeletonDeps, *spyFreshener, *fakeSkCacheLookup) {
	sf := &spyFreshener{}
	lookup := &fakeSkCacheLookup{}
	deps := SkeletonDeps{
		Series:            &fakeSkSeries{canon: canon},
		SeriesTexts:       &fakeSkSeriesTexts{err: errors.New("no text")},
		Genres:            &fakeSkGenres{},
		Keywords:          &fakeSkKeywords{},
		Networks:          &fakeSkNetworks{},
		Companies:         &fakeSkCompanies{},
		ContentRatings:    &fakeSkContentRatings{},
		Videos:            &fakeSkVideos{},
		Seasons:           &fakeSkSeasons{},
		SeriesCacheLookup: lookup,
		Freshener:         sf,
		Now:               func() time.Time { return time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC) },
	}
	return deps, sf, lookup
}

func mustLangTag(t *testing.T, s string) values.LanguageTag {
	t.Helper()
	lt, err := values.NewLanguageTag(s)
	require.NoError(t, err)
	return lt
}

// --- tests ---

func TestSkeletonComposer_HappyPath(t *testing.T) {
	t.Parallel()
	canon := skBaseCanon()
	deps, sf, lookup := skBaseDeps(canon)

	// Localized title present.
	deps.SeriesTexts = &fakeSkSeriesTexts{row: series.SeriesText{
		SeriesID: 42, Language: "ru-RU", Title: new("Звёздный городок"), Tagline: new("В погоне за славой"),
	}}
	deps.Genres = &fakeSkGenres{ids: []int64{1}, rows: []taxonomy.Genre{{ID: 1, TMDBID: tmdbIDPtr(18), Name: "Драма"}}}
	deps.Keywords = &fakeSkKeywords{ids: []int64{7}, rows: []taxonomy.Keyword{{ID: 7, TMDBID: tmdbIDPtr(9840), Name: "космос"}}}
	deps.Networks = &fakeSkNetworks{ids: []int64{2}, rows: []taxonomy.Network{{ID: 2, TMDBID: tmdbIDPtr(213), Name: "Netflix"}}}
	deps.Seasons = &fakeSkSeasons{rows: make([]series.CanonSeason, 3)}
	lookup.rows = []series.CacheEntry{{InstanceName: "homelab"}}

	sc := NewSkeletonComposer(deps)
	dto, err := sc.Compose(context.Background(), 42, mustLangTag(t, "ru-RU"))
	require.NoError(t, err)

	require.Equal(t, domain.SeriesID(42), dto.SeriesID)
	require.Equal(t, "ru-RU", dto.Lang.Value())
	require.Equal(t, "Звёздный городок", dto.Hero.Title.Value())
	require.Equal(t, "ru-RU", dto.Hero.Title.Lang().Value())
	require.Equal(t, "Star City", dto.Hero.OriginalTitle.Value())
	require.Equal(t, "В погоне за славой", dto.Hero.Tagline.Value())
	require.Equal(t, 2026, dto.Hero.YearStart.Value())
	require.Equal(t, 60, dto.Hero.RuntimeMinutes.Value())
	require.NotNil(t, dto.Hero.TmdbRating)
	require.InDelta(t, 8.4, dto.Hero.TmdbRating.Score().Value(), 0.001)
	require.NotNil(t, dto.Hero.ImdbRating)
	require.Nil(t, dto.Hero.OmdbRating)
	require.Len(t, dto.Hero.Genres, 1)
	require.Equal(t, GenreRef{TmdbID: 18, Name: "Драма"}, dto.Hero.Genres[0])
	require.Equal(t, "Returning Series", dto.Sidebar.Status.Value())
	require.Len(t, dto.Sidebar.Networks, 1)
	require.Equal(t, "Netflix", dto.Sidebar.Networks[0].Name)
	require.Len(t, dto.Sidebar.Keywords, 1)
	require.Equal(t, "en", dto.Sidebar.OriginalLanguage.Value())
	require.Len(t, dto.Sidebar.OriginCountries, 1)
	require.Equal(t, "US", dto.Sidebar.OriginCountries[0].Value())
	require.Equal(t, 3, dto.SeasonCount)
	require.Equal(t, []string{"homelab"}, dto.InLibraryInstances)
	require.Empty(t, dto.Degraded)

	// SectionSkeleton contract.
	require.Equal(t, []freshener.Section{freshener.SectionSkeleton}, sf.gotSections)
	require.Equal(t, "ru-RU", sf.gotLang)
	require.Equal(t, ModeSync, sf.gotMode)
}

// W15-2 — series_texts miss/error, but canon.OriginalTitle set → hero
// title falls back to original_title (the terminal never-empty tier).
// This replaces the old S-E2 "blank not canon" behaviour: original_title
// was deliberately retained in canon (Variant A) precisely to serve here.
func TestSkeletonComposer_MissingSeriesTexts_FallsBackToOriginalTitle(t *testing.T) {
	t.Parallel()
	deps, _, _ := skBaseDeps(skBaseCanon()) // SeriesTexts errors by default; OriginalTitle = "Star City"
	sc := NewSkeletonComposer(deps)
	dto, err := sc.Compose(context.Background(), 42, mustLangTag(t, "ru-RU"))
	require.NoError(t, err)
	require.Equal(t, "Star City", dto.Hero.Title.Value(), "series_texts miss → original_title terminal tier")
	require.True(t, dto.Hero.Tagline.IsZero()) // no tagline row → null
}

// W15-2 — the genuine "we know nothing" case: series_texts miss AND
// canon.OriginalTitle nil → zero VO title (FE placeholder), no panic.
func TestSkeletonComposer_MissingSeriesTexts_NilOriginalTitle_ZeroTitle(t *testing.T) {
	t.Parallel()
	canon := skBaseCanon()
	canon.OriginalTitle = nil
	deps, _, _ := skBaseDeps(canon) // SeriesTexts errors by default
	sc := NewSkeletonComposer(deps)
	dto, err := sc.Compose(context.Background(), 42, mustLangTag(t, "ru-RU"))
	require.NoError(t, err)
	require.True(t, dto.Hero.Title.IsZero(), "no text row AND no original_title → zero title, no panic")
	require.True(t, dto.Hero.Tagline.IsZero())
}

// S-E2 — hero title resolves ONLY from series_texts, never canon, even
// when canon.Title differs from every text row.
func TestSkeletonComposer_Hero_TitleFromTextsNotCanon(t *testing.T) {
	t.Parallel()
	canon := skBaseCanon()
	deps, _, _ := skBaseDeps(canon)
	// GetWithFallback emulates requested→en-US: the fake returns the en-US
	// row regardless of the requested ru-RU (production pickLanguageFallback).
	deps.SeriesTexts = &fakeSkSeriesTexts{row: series.SeriesText{
		SeriesID: 42, Language: "en-US", Title: new("English Title"),
	}}
	sc := NewSkeletonComposer(deps)
	dto, err := sc.Compose(context.Background(), 42, mustLangTag(t, "ru-RU"))
	require.NoError(t, err)
	require.Equal(t, "English Title", dto.Hero.Title.Value(), "hero uses en-US series_texts fallback, NEVER canon")
	require.NotEqual(t, "CANON-DIFFERENT", dto.Hero.Title.Value())
}

// S-E2 — a series with ONLY an en-US row renders that under a ru-RU
// request via the repo's en-US fallback.
func TestSkeletonComposer_Hero_EnUSFallbackUnderRu(t *testing.T) {
	t.Parallel()
	canon := skBaseCanon()
	deps, _, _ := skBaseDeps(canon)
	deps.SeriesTexts = &fakeSkSeriesTexts{row: series.SeriesText{
		SeriesID: 42, Language: "en-US", Title: new("English Title"),
	}}
	sc := NewSkeletonComposer(deps)
	dto, err := sc.Compose(context.Background(), 42, mustLangTag(t, "ru-RU"))
	require.NoError(t, err)
	require.Equal(t, "English Title", dto.Hero.Title.Value())
}

func TestSkeletonComposer_CanonLoadError(t *testing.T) {
	t.Parallel()
	deps, _, _ := skBaseDeps(skBaseCanon())
	deps.Series = &fakeSkSeries{err: errors.New("db down")}
	sc := NewSkeletonComposer(deps)
	_, err := sc.Compose(context.Background(), 42, mustLangTag(t, "ru-RU"))
	require.Error(t, err)
}

func TestSkeletonComposer_ColdWatch_EmptyInstances(t *testing.T) {
	t.Parallel()
	deps, _, lookup := skBaseDeps(skBaseCanon())
	lookup.rows = nil // TMDB-only series, not in any library
	sc := NewSkeletonComposer(deps)
	dto, err := sc.Compose(context.Background(), 42, mustLangTag(t, "en-US"))
	require.NoError(t, err)
	require.NotNil(t, dto.InLibraryInstances) // non-nil → JSON [] not null
	require.Empty(t, dto.InLibraryInstances)
	require.NotEmpty(t, dto.Sidebar.Status.Value()) // sidebar still populated from canon
}

func TestSkeletonComposer_MultiInstance_SortedDeduped(t *testing.T) {
	t.Parallel()
	deps, _, lookup := skBaseDeps(skBaseCanon())
	lookup.rows = []series.CacheEntry{
		{InstanceName: "beta"}, {InstanceName: "alpha"}, {InstanceName: "beta"},
	}
	sc := NewSkeletonComposer(deps)
	dto, err := sc.Compose(context.Background(), 42, mustLangTag(t, "en-US"))
	require.NoError(t, err)
	require.Equal(t, []string{"alpha", "beta"}, dto.InLibraryInstances)
}

func TestSkeletonComposer_NextEpisode(t *testing.T) {
	t.Parallel()

	t.Run("nil port omits next_episode", func(t *testing.T) {
		deps, _, _ := skBaseDeps(skBaseCanon())
		deps.NextEpisode = nil
		sc := NewSkeletonComposer(deps)
		dto, err := sc.Compose(context.Background(), 42, mustLangTag(t, "en-US"))
		require.NoError(t, err)
		require.Nil(t, dto.Hero.NextEpisodeCanon)
	})

	t.Run("port populates next_episode", func(t *testing.T) {
		deps, _, _ := skBaseDeps(skBaseCanon())
		air := time.Date(2026, 7, 6, 0, 0, 0, 0, time.UTC) // 5 days out from Now()
		deps.NextEpisode = &fakeSkNextEpisode{ok: true, ref: NextEpisodeRef{
			SeasonNumber: 2, EpisodeNumber: 1, Title: "Возвращение", AirDate: air,
		}}
		sc := NewSkeletonComposer(deps)
		dto, err := sc.Compose(context.Background(), 42, mustLangTag(t, "ru-RU"))
		require.NoError(t, err)
		require.NotNil(t, dto.Hero.NextEpisodeCanon)
		require.Equal(t, 2, dto.Hero.NextEpisodeCanon.SeasonNumber())
		require.Equal(t, 1, dto.Hero.NextEpisodeCanon.EpisodeNumber())
		require.Equal(t, 5, dto.Hero.NextEpisodeCanon.DaysUntil())
	})
}

func TestSkeletonComposer_DegradedOnStub(t *testing.T) {
	t.Parallel()
	canon := skBaseCanon()
	canon.Hydration = series.HydrationStub
	deps, sf, _ := skBaseDeps(canon)
	sf.result = FreshenResult{Degraded: true}
	sc := NewSkeletonComposer(deps)
	dto, err := sc.Compose(context.Background(), 42, mustLangTag(t, "en-US"))
	require.NoError(t, err)
	require.Contains(t, dto.Degraded, "tmdb_series")
	require.Contains(t, dto.Degraded, "freshener")
}

func TestSkeletonComposer_ContentRatingGuard(t *testing.T) {
	t.Parallel()
	deps, _, _ := skBaseDeps(skBaseCanon())
	// "18" (RU rating) is NOT in the ContentRating enum → guarded to zero.
	deps.ContentRatings = &fakeSkContentRatings{rows: []enrichpersistence.ContentRating{
		{CountryCode: "RU", Rating: "18"},
	}}
	sc := NewSkeletonComposer(deps)
	dto, err := sc.Compose(context.Background(), 42, mustLangTag(t, "ru-RU"))
	require.NoError(t, err)
	require.True(t, dto.Hero.ContentRating.IsZero())
}

// --- Story 584b — per-language poster/backdrop read path ---

func TestSkeletonComposer_PerLangPoster_PrefersMediaText(t *testing.T) {
	t.Parallel()
	deps, _, _ := skBaseDeps(skBaseCanon())
	deps.MediaResolver = skEagerResolver()
	deps.SeriesMediaTexts = &fakeSkMediaTexts{row: series.SeriesMediaText{
		SeriesID:      42,
		Language:      "ru-RU",
		PosterAsset:   new("/ru.jpg"),
		BackdropAsset: new("/rubg.jpg"),
	}}
	sc := NewSkeletonComposer(deps)
	dto, err := sc.Compose(context.Background(), 42, mustLangTag(t, "ru-RU"))
	require.NoError(t, err)
	// S-E3a — series_media_texts is the ONLY hero art source; the per-lang raw
	// path reaches the resolver.
	require.Equal(t, skEagerHash("/ru.jpg", "w342"), dto.Hero.PosterAsset.Value())
	require.Equal(t, skEagerHash("/rubg.jpg", "w1280"), dto.Hero.BackdropAsset.Value())
}

// S-E3a — series_media_texts miss (ErrNotFound) → nil hero art. The canon
// poster fallback was removed; the FE renders a monogram/placeholder.
func TestSkeletonComposer_PerLangPoster_NotFound_NilArt(t *testing.T) {
	t.Parallel()
	deps, _, _ := skBaseDeps(skBaseCanon())
	deps.MediaResolver = skEagerResolver()
	deps.SeriesMediaTexts = &fakeSkMediaTexts{err: dataports.ErrNotFound}
	sc := NewSkeletonComposer(deps)
	dto, err := sc.Compose(context.Background(), 42, mustLangTag(t, "ru-RU"))
	require.NoError(t, err)
	require.True(t, dto.Hero.PosterAsset.IsZero(), "no media text row -> nil poster (no canon fallback)")
	require.True(t, dto.Hero.BackdropAsset.IsZero())
}

// S-E3a — a per-lang row with a nil PosterAsset yields a nil poster (no canon
// fallback), while a present BackdropAsset still resolves.
func TestSkeletonComposer_PerLangPoster_NilPosterField_NilPoster(t *testing.T) {
	t.Parallel()
	deps, _, _ := skBaseDeps(skBaseCanon())
	deps.MediaResolver = skEagerResolver()
	deps.SeriesMediaTexts = &fakeSkMediaTexts{row: series.SeriesMediaText{
		SeriesID:      42,
		Language:      "ru-RU",
		PosterAsset:   nil,
		BackdropAsset: new("/rubg.jpg"),
	}}
	sc := NewSkeletonComposer(deps)
	dto, err := sc.Compose(context.Background(), 42, mustLangTag(t, "ru-RU"))
	require.NoError(t, err)
	require.True(t, dto.Hero.PosterAsset.IsZero(), "nil per-lang poster -> nil (no canon fallback)")
	require.Equal(t, skEagerHash("/rubg.jpg", "w1280"), dto.Hero.BackdropAsset.Value())
}

// S-E3a — SeriesMediaTexts unwired → nil hero art (no canon fallback), no panic.
func TestSkeletonComposer_PerLangPoster_NilDep_NilArt(t *testing.T) {
	t.Parallel()
	deps, _, _ := skBaseDeps(skBaseCanon())
	deps.MediaResolver = skEagerResolver()
	deps.SeriesMediaTexts = nil // unwired — back-compat, no panic
	sc := NewSkeletonComposer(deps)
	dto, err := sc.Compose(context.Background(), 42, mustLangTag(t, "ru-RU"))
	require.NoError(t, err)
	require.True(t, dto.Hero.PosterAsset.IsZero())
	require.True(t, dto.Hero.BackdropAsset.IsZero())
}

// S-E3a NULL/error pair — a non-ErrNotFound repo error must not propagate and
// yields nil hero art (canon fallback removed).
func TestSkeletonComposer_PerLangPoster_RepoError_NilArt(t *testing.T) {
	t.Parallel()
	deps, _, _ := skBaseDeps(skBaseCanon())
	deps.MediaResolver = skEagerResolver()
	deps.SeriesMediaTexts = &fakeSkMediaTexts{err: errors.New("db down")}
	sc := NewSkeletonComposer(deps)
	dto, err := sc.Compose(context.Background(), 42, mustLangTag(t, "ru-RU"))
	require.NoError(t, err)
	require.True(t, dto.Hero.PosterAsset.IsZero())
	require.True(t, dto.Hero.BackdropAsset.IsZero())
}

func TestSkeletonComposer_TrailerKey(t *testing.T) {
	t.Parallel()
	deps, _, _ := skBaseDeps(skBaseCanon())
	deps.Videos = &fakeSkVideos{rows: []enrichpersistence.Video{
		{Site: new("YouTube"), Key: new("dQw4w9WgXcQ"), Type: new("Trailer"), Official: true},
	}}
	sc := NewSkeletonComposer(deps)
	dto, err := sc.Compose(context.Background(), 42, mustLangTag(t, "en-US"))
	require.NoError(t, err)
	require.NotNil(t, dto.Hero.TrailerKey)
	require.Equal(t, "dQw4w9WgXcQ", dto.Hero.TrailerKey.Value())
}

// S-F trailer i18n — direct table-driven coverage of the lang-aware pick.
func trailerVid(lang, key string, official bool, published *time.Time) enrichpersistence.Video {
	v := enrichpersistence.Video{
		Site:        new("YouTube"),
		Key:         new(key),
		Type:        new("Trailer"),
		Official:    official,
		PublishedAt: published,
	}
	if lang != "" {
		v.Language = new(lang)
	}
	return v
}

func TestPickTrailerForLang(t *testing.T) {
	t.Parallel()

	ru := trailerVid("ru", "ruKEY123456", true, nil)
	en := trailerVid("en", "enKEY123456", true, nil)
	orig := trailerVid("ja", "jaKEY123456", true, nil) // original_language = ja
	foreign := trailerVid("de", "deKEY123456", true, nil)

	tests := []struct {
		name    string
		videos  []enrichpersistence.Video
		lang    string
		origLng string
		wantKey string // "" means expect nil
	}{
		{
			name:    "ru present -> ru key",
			videos:  []enrichpersistence.Video{en, ru, orig},
			lang:    "ru-RU",
			origLng: "ja",
			wantKey: "ruKEY123456",
		},
		{
			name:    "ru absent, original_language present -> original key",
			videos:  []enrichpersistence.Video{en, orig},
			lang:    "ru-RU",
			origLng: "ja",
			wantKey: "jaKEY123456",
		},
		{
			name:    "only en videos -> en key",
			videos:  []enrichpersistence.Video{en},
			lang:    "ru-RU",
			origLng: "ja",
			wantKey: "enKEY123456",
		},
		{
			name:    "empty list -> nil",
			videos:  nil,
			lang:    "ru-RU",
			origLng: "ja",
			wantKey: "",
		},
		{
			name:    "catch-all: only a foreign language -> still returned",
			videos:  []enrichpersistence.Video{foreign},
			lang:    "ru-RU",
			origLng: "ja",
			wantKey: "deKEY123456",
		},
		{
			name: "NULL Language falls to catch-all, no panic",
			videos: []enrichpersistence.Video{
				trailerVid("", "nilLANG1234", true, nil),
			},
			lang:    "ru-RU",
			origLng: "ja",
			wantKey: "nilLANG1234",
		},
		{
			name: "NULL Site/Key skipped",
			videos: []enrichpersistence.Video{
				{Language: new("ru"), Site: nil, Key: new("badSITE1234"), Type: new("Trailer"), Official: true},
				{Language: new("ru"), Site: new("YouTube"), Key: nil, Type: new("Trailer"), Official: true},
				ru,
			},
			lang:    "ru-RU",
			origLng: "ja",
			wantKey: "ruKEY123456",
		},
		{
			name:    "empty original_language tier skipped",
			videos:  []enrichpersistence.Video{en},
			lang:    "ru-RU",
			origLng: "",
			wantKey: "enKEY123456",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := pickTrailerForLang(tc.videos, tc.lang, tc.origLng)
			if tc.wantKey == "" {
				require.Nil(t, got)
				return
			}
			require.NotNil(t, got)
			require.Equal(t, tc.wantKey, got.Value())
		})
	}
}

// Within a language group an official YouTube Trailer is preferred over a
// non-official / non-YouTube sibling in the same language.
func TestPickTrailerForLang_PreferOfficialYouTube(t *testing.T) {
	t.Parallel()

	nonOfficial := trailerVid("ru", "nonoffic123", false, nil)
	nonYouTube := enrichpersistence.Video{
		Language: new("ru"), Site: new("Vimeo"), Key: new("vimeoKEY123"),
		Type: new("Trailer"), Official: true,
	}
	official := trailerVid("ru", "officialK12", true, nil)

	got := pickTrailerForLang(
		[]enrichpersistence.Video{nonOfficial, nonYouTube, official},
		"ru-RU", "ja",
	)
	require.NotNil(t, got)
	require.Equal(t, "officialK12", got.Value())
}

// Among equally-preferred same-language videos the newest PublishedAt wins;
// a nil PublishedAt sorts last.
func TestPickTrailerForLang_TieBreakPublishedAt(t *testing.T) {
	t.Parallel()

	older := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	newer := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)

	got := pickTrailerForLang(
		[]enrichpersistence.Video{
			trailerVid("ru", "nilPubli123", true, nil),
			trailerVid("ru", "olderPub123", true, &older),
			trailerVid("ru", "newerPub123", true, &newer),
		},
		"ru-RU", "ja",
	)
	require.NotNil(t, got)
	require.Equal(t, "newerPub123", got.Value())
}

// --- C3c-1 external_links footer restore ---

func TestSkeletonComposer_ExternalLinks_Present(t *testing.T) {
	t.Parallel()
	canon := skBaseCanon()
	imdb := domain.IMDBID("tt0903747")
	tmdb := domain.TMDBID(1396)
	tvdb := domain.TVDBID(81189)
	home := "https://www.example.com/show"
	canon.IMDBID = &imdb
	canon.TMDBID = &tmdb
	canon.TVDBID = &tvdb
	canon.Homepage = &home

	deps, _, _ := skBaseDeps(canon)
	sc := NewSkeletonComposer(deps)
	dto, err := sc.Compose(context.Background(), 42, mustLangTag(t, "en-US"))
	require.NoError(t, err)

	require.NotNil(t, dto.ExternalLinks.IMDBID)
	require.Equal(t, domain.IMDBID("tt0903747"), *dto.ExternalLinks.IMDBID)
	require.NotNil(t, dto.ExternalLinks.TMDBID)
	require.Equal(t, domain.TMDBID(1396), *dto.ExternalLinks.TMDBID)
	require.NotNil(t, dto.ExternalLinks.TVDBID)
	require.Equal(t, domain.TVDBID(81189), *dto.ExternalLinks.TVDBID)
	require.NotNil(t, dto.ExternalLinks.Homepage)
	require.Equal(t, "https://www.example.com/show", *dto.ExternalLinks.Homepage)
}

func TestSkeletonComposer_ExternalLinks_AllNil(t *testing.T) {
	t.Parallel()
	canon := skBaseCanon() // skBaseCanon leaves IMDBID/TMDBID/TVDBID/Homepage nil
	deps, _, _ := skBaseDeps(canon)
	sc := NewSkeletonComposer(deps)
	dto, err := sc.Compose(context.Background(), 42, mustLangTag(t, "en-US"))
	require.NoError(t, err)

	require.Nil(t, dto.ExternalLinks.IMDBID)
	require.Nil(t, dto.ExternalLinks.TMDBID)
	require.Nil(t, dto.ExternalLinks.TVDBID)
	require.Nil(t, dto.ExternalLinks.Homepage)
}

func TestSkeletonComposer_ExternalLinks_EmptyHomepageNilled(t *testing.T) {
	t.Parallel()
	canon := skBaseCanon()
	empty := ""
	canon.Homepage = &empty
	deps, _, _ := skBaseDeps(canon)
	sc := NewSkeletonComposer(deps)
	dto, err := sc.Compose(context.Background(), 42, mustLangTag(t, "en-US"))
	require.NoError(t, err)
	require.Nil(t, dto.ExternalLinks.Homepage) // "" → nil, no bare footer link
}
