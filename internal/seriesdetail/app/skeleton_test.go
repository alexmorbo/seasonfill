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
// fake. GetWithFallback/Get return the configured row/err; the batch method
// is unused by the skeleton path. Story 1081a: Get mirrors GetWithFallback's
// row/err (same underlying table row in production) so pre-1081a tests that
// only set `row`/`err` keep exercising the same poster resolution; posterAny
// backs the confirmed-absent → GetPosterAnyLang recovery tier.
type fakeSkMediaTexts struct {
	row         series.SeriesMediaText
	err         error
	backdropAny *string // W18-15 — GetBackdropAnyLang result
	posterAny   *string // Story 1081a — GetPosterAnyLang result
}

func (f *fakeSkMediaTexts) Get(context.Context, domain.SeriesID, string) (series.SeriesMediaText, error) {
	return f.row, f.err
}
func (f *fakeSkMediaTexts) GetWithFallback(context.Context, domain.SeriesID, string) (series.SeriesMediaText, error) {
	return f.row, f.err
}
func (f *fakeSkMediaTexts) ListByIDsWithFallback(context.Context, []domain.SeriesID, string) (map[domain.SeriesID]series.SeriesMediaText, error) {
	return nil, nil
}
func (f *fakeSkMediaTexts) GetBackdropAnyLang(context.Context, domain.SeriesID, string) (*string, error) {
	return f.backdropAny, nil
}
func (f *fakeSkMediaTexts) GetPosterAnyLang(context.Context, domain.SeriesID, string) (*string, error) {
	return f.posterAny, nil
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

// freshenCall captures one EnsureFreshScope invocation so tests assert the
// full per-open freshen contract (W17-2 makes Compose fire two calls: a
// ModeSync SectionSkeleton probe + a ModeAsync Overview/Cast/Media widen).
type freshenCall struct {
	sections []freshener.Section
	lang     string
	force    bool
	mode     EnsureFreshMode
}

// spyFreshener records every EnsureFreshScope invocation so tests assert the
// skeleton section contract. The got* accessors mirror the FIRST call (the
// ModeSync skeleton probe) so pre-W17-2 assertions keep passing unchanged.
type spyFreshener struct {
	calls  []freshenCall
	result FreshenResult

	gotSections []freshener.Section
	gotLang     string
	gotMode     EnsureFreshMode
}

func (s *spyFreshener) EnsureFreshScope(_ context.Context, _ domain.SeriesID, lang string, sections []freshener.Section, _ []int, force bool, mode EnsureFreshMode) (FreshenResult, error) {
	s.calls = append(s.calls, freshenCall{sections: sections, lang: lang, force: force, mode: mode})
	if len(s.calls) == 1 {
		s.gotSections = sections
		s.gotLang = lang
		s.gotMode = mode
	}
	return s.result, nil
}

func (s *spyFreshener) EnsureFresh(context.Context, domain.SeriesID, string) FreshenResult {
	return s.result
}

// seedingFreshener simulates the production freshener's HandleForcedLang seed:
// on a force=true ModeSync SectionSkeleton dispatch it invokes onSeed (the test
// wires this to mutate the media-texts fake, mirroring the series_media_texts
// {lang} upsert with poster_checked_at). All calls are recorded so tests can
// assert whether/how the seed fired.
type seedingFreshener struct {
	calls  []freshenCall
	result FreshenResult
	onSeed func()
}

func (s *seedingFreshener) EnsureFreshScope(_ context.Context, _ domain.SeriesID, lang string, sections []freshener.Section, _ []int, force bool, mode EnsureFreshMode) (FreshenResult, error) {
	s.calls = append(s.calls, freshenCall{sections: sections, lang: lang, force: force, mode: mode})
	if s.onSeed != nil && force && mode == ModeSync &&
		len(sections) == 1 && sections[0] == freshener.SectionSkeleton {
		s.onSeed()
	}
	return s.result, nil
}

func (s *seedingFreshener) EnsureFresh(context.Context, domain.SeriesID, string) FreshenResult {
	return s.result
}

// forcedSkeletonSeeds counts the W110-2 seed dispatches (force=true, ModeSync,
// exactly [SectionSkeleton]).
func (s *seedingFreshener) forcedSkeletonSeeds() int {
	n := 0
	for _, c := range s.calls {
		if c.force && c.mode == ModeSync && len(c.sections) == 1 && c.sections[0] == freshener.SectionSkeleton {
			n++
		}
	}
	return n
}

// seedCallIndex returns the index of the (single) forced SectionSkeleton seed
// call; fails the test if there is not exactly one.
func (s *seedingFreshener) seedCallIndex(t *testing.T) int {
	t.Helper()
	idx := -1
	for i, c := range s.calls {
		if c.force && c.mode == ModeSync && len(c.sections) == 1 && c.sections[0] == freshener.SectionSkeleton {
			require.Equal(t, -1, idx, "more than one forced skeleton seed")
			idx = i
		}
	}
	require.NotEqual(t, -1, idx, "no forced skeleton seed call recorded")
	return idx
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

// #1038 — TMDB-only rows whose year column was never derived: YearStart
// falls back to first_air_date's year (pure display derive). Both nil → 0.
func TestSkeletonComposer_YearStart_DerivedFromFirstAirDate(t *testing.T) {
	t.Parallel()
	t.Run("year nil, first_air_date set → derived", func(t *testing.T) {
		t.Parallel()
		canon := skBaseCanon()
		canon.Year = nil
		fad := time.Date(2022, 8, 21, 0, 0, 0, 0, time.UTC)
		canon.FirstAirDate = &fad
		deps, _, lookup := skBaseDeps(canon)
		lookup.rows = []series.CacheEntry{{InstanceName: "homelab"}}
		sc := NewSkeletonComposer(deps)
		dto, err := sc.Compose(context.Background(), 42, mustLangTag(t, "ru-RU"))
		require.NoError(t, err)
		require.Equal(t, 2022, dto.Hero.YearStart.Value())
	})
	t.Run("year nil, first_air_date nil → zero", func(t *testing.T) {
		t.Parallel()
		canon := skBaseCanon()
		canon.Year = nil
		canon.FirstAirDate = nil
		deps, _, lookup := skBaseDeps(canon)
		lookup.rows = []series.CacheEntry{{InstanceName: "homelab"}}
		sc := NewSkeletonComposer(deps)
		dto, err := sc.Compose(context.Background(), 42, mustLangTag(t, "ru-RU"))
		require.NoError(t, err)
		require.True(t, dto.Hero.YearStart.IsZero())
	})
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

// W15-9 — served-language contract on the skeleton hero title.
func TestSkeletonComposer_ServedLanguage(t *testing.T) {
	t.Parallel()

	t.Run("served row lang set; served==requested → no marker", func(t *testing.T) {
		t.Parallel()
		deps, _, _ := skBaseDeps(skBaseCanon())
		deps.SeriesTexts = &fakeSkSeriesTexts{row: series.SeriesText{
			SeriesID: 42, Language: "ru-RU", Title: new("Звёздный городок"),
		}}
		sc := NewSkeletonComposer(deps)
		dto, err := sc.Compose(context.Background(), 42, mustLangTag(t, "ru-RU"))
		require.NoError(t, err)
		require.Equal(t, "ru-RU", dto.ServedLanguage)
		require.NotContains(t, dto.Degraded, "missing_lang")
	})

	t.Run("served!=requested → missing_lang marker", func(t *testing.T) {
		t.Parallel()
		deps, _, _ := skBaseDeps(skBaseCanon())
		deps.SeriesTexts = &fakeSkSeriesTexts{row: series.SeriesText{
			SeriesID: 42, Language: "en-US", Title: new("English Title"),
		}}
		sc := NewSkeletonComposer(deps)
		dto, err := sc.Compose(context.Background(), 42, mustLangTag(t, "ru-RU"))
		require.NoError(t, err)
		require.Equal(t, "en-US", dto.ServedLanguage)
		require.Contains(t, dto.Degraded, "missing_lang")
	})

	t.Run("no text row (original_title path) → served empty, no marker", func(t *testing.T) {
		t.Parallel()
		deps, _, _ := skBaseDeps(skBaseCanon()) // SeriesTexts errors by default → original_title
		sc := NewSkeletonComposer(deps)
		dto, err := sc.Compose(context.Background(), 42, mustLangTag(t, "ru-RU"))
		require.NoError(t, err)
		require.Equal(t, "Star City", dto.Hero.Title.Value())
		require.Empty(t, dto.ServedLanguage)
		require.NotContains(t, dto.Degraded, "missing_lang")
	})

	t.Run("text row present but nil Title (original_title used) → served empty, no marker", func(t *testing.T) {
		t.Parallel()
		deps, _, _ := skBaseDeps(skBaseCanon())
		deps.SeriesTexts = &fakeSkSeriesTexts{row: series.SeriesText{
			SeriesID: 42, Language: "en-US", Title: nil, // no title → hero falls to original_title
		}}
		sc := NewSkeletonComposer(deps)
		dto, err := sc.Compose(context.Background(), 42, mustLangTag(t, "ru-RU"))
		require.NoError(t, err)
		require.Equal(t, "Star City", dto.Hero.Title.Value())
		require.Empty(t, dto.ServedLanguage)
		require.NotContains(t, dto.Degraded, "missing_lang")
	})

	t.Run("fallback row with empty-string Title (original_title used) → served empty, no marker", func(t *testing.T) {
		t.Parallel()
		deps, _, _ := skBaseDeps(skBaseCanon())
		deps.SeriesTexts = &fakeSkSeriesTexts{row: series.SeriesText{
			SeriesID: 42, Language: "en-US", Title: new(""), // empty title → hero falls to original_title
		}}
		sc := NewSkeletonComposer(deps)
		dto, err := sc.Compose(context.Background(), 42, mustLangTag(t, "ru-RU"))
		require.NoError(t, err)
		require.Equal(t, "Star City", dto.Hero.Title.Value(), "empty text title falls through to original_title")
		require.Empty(t, dto.ServedLanguage, "empty-title fallback row must NOT set served_language")
		require.NotContains(t, dto.Degraded, "missing_lang", "no spurious marker for unused empty-title row")
	})
}

// W17-2 — a library detail open must widen the freshen scope to parity with
// the tmdb_fallback route: SectionSkeleton stays the ONLY ModeSync (response-
// blocking) section, while Overview/Cast/Media are dispatched ModeAsync so the
// heavy TMDB fetches never block the response. This is what heals a stuck
// library series (enrichment_media/text/cast_synced_at NULL) on first view.
func TestSkeletonComposer_LibraryFreshenScope_WidensHeavySectionsAsync(t *testing.T) {
	t.Parallel()
	deps, sf, _ := skBaseDeps(skBaseCanon())
	sc := NewSkeletonComposer(deps)
	_, err := sc.Compose(context.Background(), 42, mustLangTag(t, "ru-RU"))
	require.NoError(t, err)

	require.Len(t, sf.calls, 2, "one ModeSync skeleton probe + one ModeAsync heavy-section widen")

	// Call 0 — SectionSkeleton, ModeSync: the sole response-blocking section.
	require.Equal(t, []freshener.Section{freshener.SectionSkeleton}, sf.calls[0].sections)
	require.Equal(t, ModeSync, sf.calls[0].mode, "skeleton must stay ModeSync (budget-blocking)")
	require.False(t, sf.calls[0].force)
	require.Equal(t, "ru-RU", sf.calls[0].lang)

	// Call 1 — Overview/Cast/Media, ModeAsync: heavy sections, non-blocking.
	require.Equal(t, []freshener.Section{
		freshener.SectionOverview,
		freshener.SectionCast,
		freshener.SectionMedia,
	}, sf.calls[1].sections)
	require.Equal(t, ModeAsync, sf.calls[1].mode, "heavy sections must be async — response must not wait on Media/Cast")
	require.Equal(t, "ru-RU", sf.calls[1].lang, "async widen must carry the requested lang for per-lang art/text")
}

// W17-2 — the response budget stays flat: exactly ONE ModeSync freshen call
// (SectionSkeleton). Media/Cast/Overview are never dispatched ModeSync, so the
// detail endpoint never synchronously waits on those TMDB fetches.
func TestSkeletonComposer_ResponseBudgetFlat_OnlySkeletonSync(t *testing.T) {
	t.Parallel()
	deps, sf, _ := skBaseDeps(skBaseCanon())
	sc := NewSkeletonComposer(deps)
	_, err := sc.Compose(context.Background(), 42, mustLangTag(t, "en-US"))
	require.NoError(t, err)

	syncCalls := 0
	for _, c := range sf.calls {
		if c.mode == ModeSync {
			syncCalls++
			require.Equal(t, []freshener.Section{freshener.SectionSkeleton}, c.sections,
				"the only ModeSync section may be SectionSkeleton")
		}
	}
	require.Equal(t, 1, syncCalls, "exactly one blocking (ModeSync) freshen call")
}

// W17-2 — re-run gating is delegated to the Probe: the composer passes
// force=false on BOTH calls, so a fresh section (TTL not elapsed / singleflight
// in-flight) dispatches no TMDB work on a subsequent open. The composer never
// forces, so it can never re-run heavy work on a warm page.
func TestSkeletonComposer_FreshenNeverForces_ProbeGatesReRuns(t *testing.T) {
	t.Parallel()
	deps, sf, _ := skBaseDeps(skBaseCanon())
	sc := NewSkeletonComposer(deps)
	_, err := sc.Compose(context.Background(), 42, mustLangTag(t, "en-US"))
	require.NoError(t, err)

	require.NotEmpty(t, sf.calls)
	for i, c := range sf.calls {
		require.Falsef(t, c.force, "freshen call %d must not force (Probe gates re-runs)", i)
	}
}

// W17-2 — a nil Freshener (unwired) must not panic and must fire no calls.
func TestSkeletonComposer_NilFreshener_NoWiden_NoPanic(t *testing.T) {
	t.Parallel()
	deps, _, _ := skBaseDeps(skBaseCanon())
	deps.Freshener = nil
	sc := NewSkeletonComposer(deps)
	dto, err := sc.Compose(context.Background(), 42, mustLangTag(t, "en-US"))
	require.NoError(t, err)
	require.Equal(t, domain.SeriesID(42), dto.SeriesID)
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

// --- Story 1081a — marker-aware poster resolution (T3/T4) ---

// T3 — confirmed-absent (PosterAsset nil, PosterCheckedAt SET) serves the
// STABLE original/canonical poster via GetPosterAnyLang instead of a
// monogram: we KNOW the localized poster will not arrive until a re-check,
// so the original poster never gets swapped out on a later poll.
func TestSkeletonComposer_PosterMarker_ConfirmedAbsent_ServesOriginal(t *testing.T) {
	t.Parallel()
	deps, _, _ := skBaseDeps(skBaseCanon())
	deps.MediaResolver = skEagerResolver()
	checkedAt := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	deps.SeriesMediaTexts = &fakeSkMediaTexts{
		row: series.SeriesMediaText{
			SeriesID:        42,
			Language:        "ru-RU",
			PosterAsset:     nil, // confirmed-absent — checked, nothing localized
			PosterCheckedAt: &checkedAt,
		},
		posterAny: new("/en.jpg"), // the stable original/canonical poster
	}
	sc := NewSkeletonComposer(deps)
	dto, err := sc.Compose(context.Background(), 42, mustLangTag(t, "ru-RU"))
	require.NoError(t, err)
	require.Equal(t, skEagerHash("/en.jpg", "w342"), dto.Hero.PosterAsset.Value(),
		"confirmed-absent must serve the stable original poster, not a monogram")
}

// T4 — never-checked (no row at all, OR row present but PosterCheckedAt nil)
// leaves the poster nil (monogram) — the cold ModeSync skeleton refresh
// resolves presence before first paint. This is the anti-swap invariant: an
// unchecked ru poster must NOT borrow en.
func TestSkeletonComposer_PosterMarker_NeverChecked_NilPoster(t *testing.T) {
	t.Parallel()
	deps, _, _ := skBaseDeps(skBaseCanon())
	deps.MediaResolver = skEagerResolver()
	deps.SeriesMediaTexts = &fakeSkMediaTexts{
		err:       dataports.ErrNotFound, // no ru-RU row at all
		posterAny: new("/en.jpg"),        // present elsewhere, but must NOT be served
	}
	sc := NewSkeletonComposer(deps)
	dto, err := sc.Compose(context.Background(), 42, mustLangTag(t, "ru-RU"))
	require.NoError(t, err)
	require.True(t, dto.Hero.PosterAsset.IsZero(),
		"never-checked (no row) must render nil/monogram, NOT borrow en (that IS the swap)")
}

// T4b — row EXISTS but is genuinely never-checked (PosterAsset nil AND
// PosterCheckedAt nil) — must still render nil, not fall through to
// GetPosterAnyLang (that tier is reserved for confirmed-absent only).
func TestSkeletonComposer_PosterMarker_RowExistsNeverChecked_NilPoster(t *testing.T) {
	t.Parallel()
	deps, _, _ := skBaseDeps(skBaseCanon())
	deps.MediaResolver = skEagerResolver()
	deps.SeriesMediaTexts = &fakeSkMediaTexts{
		row: series.SeriesMediaText{
			SeriesID:        42,
			Language:        "ru-RU",
			PosterAsset:     nil,
			PosterCheckedAt: nil, // never checked
		},
		posterAny: new("/en.jpg"),
	}
	sc := NewSkeletonComposer(deps)
	dto, err := sc.Compose(context.Background(), 42, mustLangTag(t, "ru-RU"))
	require.NoError(t, err)
	require.True(t, dto.Hero.PosterAsset.IsZero(),
		"never-checked row (both nil) must render nil — the any-lang recovery tier is confirmed-absent-only")
}

// --- W110-2 — cold poster-presence seed ---

// coldSeedDeps builds skeleton deps wired for the W110-2 seed: a mutable
// media-texts fake + a seedingFreshener + ColdMediaSeed enabled + an eager
// unified resolver (nil path → sentinel; non-nil → eager hash).
func coldSeedDeps(t *testing.T, mt *fakeSkMediaTexts, sf *seedingFreshener, enabled bool) SkeletonDeps {
	t.Helper()
	deps, _, lookup := skBaseDeps(skBaseCanon())
	lookup.rows = []series.CacheEntry{{InstanceName: "homelab"}}
	res := skEagerResolver()
	res.SetUnifiedResolve(true)
	deps.MediaResolver = res
	deps.SeriesMediaTexts = mt
	deps.Freshener = sf
	deps.ColdMediaSeed = enabled
	return deps
}

// Cold-unknown series (no requested-lang row) → the forced seed writes the
// localized poster, so the FIRST compose serves the REAL ru poster, never the
// sentinel. Asserts the seed dispatch shape too.
func TestSkeletonComposer_ColdUnknownPoster_SeedWritesLocalized_NoSentinel(t *testing.T) {
	t.Parallel()
	mt := &fakeSkMediaTexts{err: dataports.ErrNotFound} // never-checked at first
	sf := &seedingFreshener{}
	sf.onSeed = func() {
		// HandleForcedLang wrote the ru row with a present poster.
		mt.err = nil
		mt.row = series.SeriesMediaText{
			SeriesID: 42, Language: "ru-RU",
			PosterAsset:     new("/ru.jpg"),
			PosterCheckedAt: new(time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)),
		}
	}
	sc := NewSkeletonComposer(coldSeedDeps(t, mt, sf, true))
	dto, err := sc.Compose(context.Background(), 42, mustLangTag(t, "ru-RU"))
	require.NoError(t, err)

	require.Equal(t, 1, sf.forcedSkeletonSeeds(), "exactly one forced SectionSkeleton seed on cold-unknown")
	require.Equal(t, skEagerHash("/ru.jpg", "w342"), dto.Hero.PosterAsset.Value(),
		"first paint must carry the real localized poster")
	require.NotEqual(t, appmedia.SentinelMissingHash, dto.Hero.PosterAsset.Value(),
		"cold-unknown must NOT serve the sentinel on first paint")
	// Seed dispatch shape.
	require.Equal(t, "ru-RU", sf.calls[sf.seedCallIndex(t)].lang)
}

// Cold-unknown series with NO localized poster → the seed writes a confirmed-
// absent row; buildHero then serves the stable original via GetPosterAnyLang —
// still non-nil, still not the sentinel (no en→ru swap since it's now
// confirmed-absent).
func TestSkeletonComposer_ColdUnknownPoster_SeedWritesAbsent_ServesOriginal(t *testing.T) {
	t.Parallel()
	mt := &fakeSkMediaTexts{err: dataports.ErrNotFound, posterAny: new("/en.jpg")}
	sf := &seedingFreshener{}
	sf.onSeed = func() {
		mt.err = nil
		mt.row = series.SeriesMediaText{
			SeriesID: 42, Language: "ru-RU",
			PosterAsset:     nil, // strict miss → absence row
			PosterCheckedAt: new(time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)),
		}
	}
	sc := NewSkeletonComposer(coldSeedDeps(t, mt, sf, true))
	dto, err := sc.Compose(context.Background(), 42, mustLangTag(t, "ru-RU"))
	require.NoError(t, err)

	require.Equal(t, 1, sf.forcedSkeletonSeeds())
	require.Equal(t, skEagerHash("/en.jpg", "w342"), dto.Hero.PosterAsset.Value(),
		"confirmed-absent after seed → stable original via GetPosterAnyLang")
	require.NotEqual(t, appmedia.SentinelMissingHash, dto.Hero.PosterAsset.Value())
}

// Warm/present path: a requested-lang row already carries a poster → NO seed
// dispatched, poster unchanged.
func TestSkeletonComposer_WarmPresentPoster_NoSeed(t *testing.T) {
	t.Parallel()
	mt := &fakeSkMediaTexts{row: series.SeriesMediaText{
		SeriesID: 42, Language: "ru-RU", PosterAsset: new("/ru.jpg"),
	}}
	sf := &seedingFreshener{}
	sf.onSeed = func() { t.Fatal("seed must not fire when poster is present") }
	sc := NewSkeletonComposer(coldSeedDeps(t, mt, sf, true))
	dto, err := sc.Compose(context.Background(), 42, mustLangTag(t, "ru-RU"))
	require.NoError(t, err)

	require.Equal(t, 0, sf.forcedSkeletonSeeds(), "present poster → no forced seed")
	require.Equal(t, skEagerHash("/ru.jpg", "w342"), dto.Hero.PosterAsset.Value())
}

// Confirmed-absent path: PosterCheckedAt stamped, PosterAsset nil → presence is
// KNOWN (absent) → NO seed; GetPosterAnyLang serves the original.
func TestSkeletonComposer_ConfirmedAbsentPoster_NoSeed(t *testing.T) {
	t.Parallel()
	checkedAt := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	mt := &fakeSkMediaTexts{
		row: series.SeriesMediaText{
			SeriesID: 42, Language: "ru-RU",
			PosterAsset:     nil,
			PosterCheckedAt: &checkedAt,
		},
		posterAny: new("/en.jpg"),
	}
	sf := &seedingFreshener{}
	sf.onSeed = func() { t.Fatal("seed must not fire when presence is confirmed-absent") }
	sc := NewSkeletonComposer(coldSeedDeps(t, mt, sf, true))
	dto, err := sc.Compose(context.Background(), 42, mustLangTag(t, "ru-RU"))
	require.NoError(t, err)

	require.Equal(t, 0, sf.forcedSkeletonSeeds())
	require.Equal(t, skEagerHash("/en.jpg", "w342"), dto.Hero.PosterAsset.Value())
}

// Kill-switch OFF: cold-unknown series → NO seed dispatched, poster falls back
// to the pre-W110-2 sentinel (self-heals on refresh via the async widen).
func TestSkeletonComposer_KillSwitchOff_NoSeed_Sentinel(t *testing.T) {
	t.Parallel()
	mt := &fakeSkMediaTexts{err: dataports.ErrNotFound}
	sf := &seedingFreshener{}
	sf.onSeed = func() { t.Fatal("seed must not fire when ColdMediaSeed is disabled") }
	sc := NewSkeletonComposer(coldSeedDeps(t, mt, sf, false)) // kill-switch OFF
	dto, err := sc.Compose(context.Background(), 42, mustLangTag(t, "ru-RU"))
	require.NoError(t, err)

	require.Equal(t, 0, sf.forcedSkeletonSeeds(), "kill-switch off → no forced seed")
	require.Equal(t, appmedia.SentinelMissingHash, dto.Hero.PosterAsset.Value(),
		"disabled → legacy sentinel-on-cold behavior preserved")
}

// Genuinely art-less series: the seed writes a confirmed-absent row AND no poster
// exists in any language (GetPosterAnyLang → nil) → the hero stays the sentinel/
// monogram. The seed must NOT fabricate a false poster.
func TestSkeletonComposer_ArtlessSeries_SeedWritesAbsence_StillSentinel(t *testing.T) {
	t.Parallel()
	mt := &fakeSkMediaTexts{err: dataports.ErrNotFound, posterAny: nil}
	sf := &seedingFreshener{}
	sf.onSeed = func() {
		mt.err = nil
		mt.row = series.SeriesMediaText{
			SeriesID: 42, Language: "ru-RU",
			PosterAsset:     nil,
			PosterCheckedAt: new(time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)),
		}
		// posterAny stays nil — no art anywhere.
	}
	sc := NewSkeletonComposer(coldSeedDeps(t, mt, sf, true))
	dto, err := sc.Compose(context.Background(), 42, mustLangTag(t, "ru-RU"))
	require.NoError(t, err)

	require.Equal(t, 1, sf.forcedSkeletonSeeds())
	require.Equal(t, appmedia.SentinelMissingHash, dto.Hero.PosterAsset.Value(),
		"no art anywhere → sentinel/monogram, never a fabricated poster")
}

// W16-3 — network & production-company logos must be resolved through the
// MediaResolver (content-hash), not passed through as raw TMDB paths. The
// skeleton path uses plain Resolve (not ResolveSync), whose eager-hash branch
// only fires under the unified-resolve contract, so the test enables it.
func TestSkeletonComposer_NetworkAndCompanyLogos_Resolved(t *testing.T) {
	t.Parallel()
	deps, _, _ := skBaseDeps(skBaseCanon())
	deps.MediaResolver = skEagerResolver()
	deps.MediaResolver.SetUnifiedResolve(true)
	deps.Networks = &fakeSkNetworks{
		ids:  []int64{7},
		rows: []taxonomy.Network{{ID: 7, TMDBID: tmdbIDPtr(213), Name: "Netflix", LogoAsset: new("/net.png")}},
	}
	deps.Companies = &fakeSkCompanies{
		ids:  []int64{9},
		rows: []taxonomy.ProductionCompany{{ID: 9, Name: "AMC Studios", LogoAsset: new("/co.png")}},
	}
	sc := NewSkeletonComposer(deps)
	dto, err := sc.Compose(context.Background(), 42, mustLangTag(t, "ru-RU"))
	require.NoError(t, err)

	require.Len(t, dto.Sidebar.Networks, 1)
	require.Equal(t, skEagerHash("/net.png", "w185"), dto.Sidebar.Networks[0].LogoAsset,
		"network logo must be the resolved content hash, not the raw /net.png path")
	require.Len(t, dto.Sidebar.ProductionCompanies, 1)
	require.Equal(t, skEagerHash("/co.png", "w185"), dto.Sidebar.ProductionCompanies[0].LogoAsset,
		"company logo must be the resolved content hash, not the raw /co.png path")
}

// W16-3 negative — a nil LogoAsset must yield an empty string (no panic, no
// bogus hash), for both networks and companies. With unified-resolve OFF the
// resolver returns nil for a nil path, and strOrEmpty maps that to "".
func TestSkeletonComposer_NetworkAndCompanyLogos_NilPath_Empty(t *testing.T) {
	t.Parallel()
	deps, _, _ := skBaseDeps(skBaseCanon())
	deps.MediaResolver = skEagerResolver()
	deps.Networks = &fakeSkNetworks{
		ids:  []int64{7},
		rows: []taxonomy.Network{{ID: 7, TMDBID: tmdbIDPtr(213), Name: "Netflix", LogoAsset: nil}},
	}
	deps.Companies = &fakeSkCompanies{
		ids:  []int64{9},
		rows: []taxonomy.ProductionCompany{{ID: 9, Name: "AMC Studios", LogoAsset: nil}},
	}
	sc := NewSkeletonComposer(deps)
	dto, err := sc.Compose(context.Background(), 42, mustLangTag(t, "ru-RU"))
	require.NoError(t, err)

	require.Len(t, dto.Sidebar.Networks, 1)
	require.Empty(t, dto.Sidebar.Networks[0].LogoAsset)
	require.Len(t, dto.Sidebar.ProductionCompanies, 1)
	require.Empty(t, dto.Sidebar.ProductionCompanies[0].LogoAsset)
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

// W18-15 — the best-language row is POSTER-ONLY (backdrop NULL); the hero must
// recover a backdrop from the per-column any-language fallback, not a placeholder.
func TestSkeletonComposer_PerLangBackdrop_AnyLangFallback(t *testing.T) {
	t.Parallel()
	deps, _, _ := skBaseDeps(skBaseCanon())
	deps.MediaResolver = skEagerResolver()
	deps.SeriesMediaTexts = &fakeSkMediaTexts{
		row: series.SeriesMediaText{
			SeriesID:      42,
			Language:      "ru-RU",
			PosterAsset:   new("/ru.jpg"),
			BackdropAsset: nil, // poster-only ru row — the bug
		},
		backdropAny: new("/en_bg.jpg"), // an en-US / other-lang row HAS a backdrop
	}
	sc := NewSkeletonComposer(deps)
	dto, err := sc.Compose(context.Background(), 42, mustLangTag(t, "ru-RU"))
	require.NoError(t, err)
	require.Equal(t, skEagerHash("/ru.jpg", "w342"), dto.Hero.PosterAsset.Value(), "poster stays per-lang ru")
	require.Equal(t, skEagerHash("/en_bg.jpg", "w1280"), dto.Hero.BackdropAsset.Value(),
		"W18-15 — backdrop recovered from any-lang fallback, not a placeholder")
}

// W18-15 negative — poster-only row AND no backdrop in any language → the hero
// backdrop stays zero (placeholder); the fallback must not fabricate one.
func TestSkeletonComposer_PerLangBackdrop_AnyLangMiss_NilBackdrop(t *testing.T) {
	t.Parallel()
	deps, _, _ := skBaseDeps(skBaseCanon())
	deps.MediaResolver = skEagerResolver()
	deps.SeriesMediaTexts = &fakeSkMediaTexts{
		row:         series.SeriesMediaText{SeriesID: 42, Language: "ru-RU", PosterAsset: new("/ru.jpg")},
		backdropAny: nil, // no backdrop in any language
	}
	sc := NewSkeletonComposer(deps)
	dto, err := sc.Compose(context.Background(), 42, mustLangTag(t, "ru-RU"))
	require.NoError(t, err)
	require.Equal(t, skEagerHash("/ru.jpg", "w342"), dto.Hero.PosterAsset.Value())
	require.True(t, dto.Hero.BackdropAsset.IsZero(), "no backdrop anywhere → zero (placeholder)")
}

// Story 1111 F-04 — posterPresenceUnknown returns true ONLY for a genuine no-row
// (ErrNotFound). A transient error must NOT be treated as unknown (would force a
// blocking seed every view). Present/confirmed-absent are KNOWN.
func TestPosterPresenceUnknown_OnlyNotFoundIsUnknown(t *testing.T) {
	t.Parallel()
	mk := func(mt *fakeSkMediaTexts) *SkeletonComposer {
		return NewSkeletonComposer(SkeletonDeps{SeriesMediaTexts: mt})
	}
	checkedAt := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	require.True(t, mk(&fakeSkMediaTexts{err: dataports.ErrNotFound}).
		posterPresenceUnknown(context.Background(), 42, "ru-RU"), "ErrNotFound → unknown")
	require.False(t, mk(&fakeSkMediaTexts{err: errors.New("db blip")}).
		posterPresenceUnknown(context.Background(), 42, "ru-RU"), "transient → NOT unknown (skip seed)")
	require.False(t, mk(&fakeSkMediaTexts{row: series.SeriesMediaText{PosterAsset: new("/ru.jpg")}}).
		posterPresenceUnknown(context.Background(), 42, "ru-RU"), "present → known")
	require.False(t, mk(&fakeSkMediaTexts{row: series.SeriesMediaText{PosterCheckedAt: &checkedAt}}).
		posterPresenceUnknown(context.Background(), 42, "ru-RU"), "confirmed-absent → known")
	require.True(t, mk(&fakeSkMediaTexts{row: series.SeriesMediaText{}}).
		posterPresenceUnknown(context.Background(), 42, "ru-RU"), "never-checked row (both nil) → unknown")
}

// Story 1111 F-04 — end-to-end: a TRANSIENT media-texts error with ColdMediaSeed
// ON must NOT dispatch a forced SectionSkeleton seed (pre-fix it did, turning a
// DB blip into a blocking GetTV per view). Contrast the ErrNotFound cold-unknown
// tests which DO seed.
func TestSkeletonComposer_TransientMediaTextsError_NoSeed(t *testing.T) {
	t.Parallel()
	mt := &fakeSkMediaTexts{err: errors.New("db connection reset")} // transient, NOT ErrNotFound
	sf := &seedingFreshener{}
	sf.onSeed = func() { t.Fatal("transient media-texts error must NOT force a cold seed (F-04)") }
	sc := NewSkeletonComposer(coldSeedDeps(t, mt, sf, true)) // ColdMediaSeed ON
	_, err := sc.Compose(context.Background(), 42, mustLangTag(t, "ru-RU"))
	require.NoError(t, err)
	require.Equal(t, 0, sf.forcedSkeletonSeeds(), "transient error → no forced SectionSkeleton seed")
}
