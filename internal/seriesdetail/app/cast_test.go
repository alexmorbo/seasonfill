package seriesdetail

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/internal/catalog/domain/series"
	"github.com/alexmorbo/seasonfill/internal/enrichment/domain/people"
	ports "github.com/alexmorbo/seasonfill/internal/shared/dataports"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
	"github.com/alexmorbo/seasonfill/internal/shared/media"
)

// --- cast-composer-local fakes ---

// fakeCastSeriesPeople distinguishes cast and crew rows by the
// Kind argument the composer passes. Keeps the test fixtures
// terse without breaking the shared fakeSeriesPeople type.
type fakeCastSeriesPeople struct {
	cast []people.SeriesCredit
	crew []people.SeriesCredit
	err  error
}

func (f *fakeCastSeriesPeople) ListBySeries(_ context.Context, _ domain.SeriesID, kind people.SeriesCreditKind, _ string) ([]people.SeriesCredit, error) {
	if f.err != nil {
		return nil, f.err
	}
	if kind == people.SeriesCreditCast {
		return f.cast, nil
	}
	return f.crew, nil
}

type fakeCastPeople struct {
	rows map[int64]people.Person
}

func (f *fakeCastPeople) ListByIDs(_ context.Context, ids []int64) ([]people.Person, error) {
	out := make([]people.Person, 0, len(ids))
	for _, id := range ids {
		if p, ok := f.rows[id]; ok {
			out = append(out, p)
		}
	}
	return out, nil
}

type fakePersonCredits struct {
	rows map[int64][]PersonCreditRef
	err  error
}

func (f *fakePersonCredits) ListByPerson(_ context.Context, personID int64) ([]PersonCreditRef, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.rows[personID], nil
}

type fakeEpisodesCount struct {
	counts map[domain.SeriesID]int
	err    error
}

func (f *fakeEpisodesCount) CountBySeries(_ context.Context, seriesID domain.SeriesID) (int, error) {
	if f.err != nil {
		return 0, f.err
	}
	return f.counts[seriesID], nil
}

func castBaseDeps(t *testing.T) (CastDeps, *fakeSeriesCache, *fakeSeries, *fakeCastSeriesPeople, *fakeCastPeople, *fakePersonCredits, *fakeEpisodesCount) {
	t.Helper()
	now := time.Now().UTC()
	cache := &fakeSeriesCache{
		entries: map[string]series.CacheEntry{
			cacheKey("alpha", 1): {
				InstanceName:   "alpha",
				SonarrSeriesID: 1,
				SeriesID:       seriesIDPtr(42),
				Title:          "The Last of Us",
				Monitored:      true,
			},
		},
		byCanon: map[domain.SeriesID][]series.CacheEntry{},
	}
	canon := &fakeSeries{
		rows: map[domain.SeriesID]series.Canon{
			42: {ID: 42, OriginalTitle: new("The Last of Us"), TMDBID: tmdbIDPtr(100)},
		},
	}
	sp := &fakeCastSeriesPeople{}
	persons := &fakeCastPeople{rows: map[int64]people.Person{}}
	credits := &fakePersonCredits{rows: map[int64][]PersonCreditRef{}}
	counts := &fakeEpisodesCount{counts: map[domain.SeriesID]int{42: 9}}
	deps := CastDeps{
		SeriesCache:       cache,
		SeriesCacheLookup: cache,
		Series:            canon,
		SeriesPeople:      sp,
		People:            persons,
		PersonCredits:     credits,
		EpisodesCount:     counts,
		Logger:            newSilentLogger(),
		Now:               func() time.Time { return now },
	}
	return deps, cache, canon, sp, persons, credits, counts
}

func seedPerson(persons *fakeCastPeople, id int64, name string, tmdbID *domain.TMDBID) {
	persons.rows[id] = people.Person{ID: id, Name: name, TMDBID: tmdbID}
}

// tmdbIDPtr makes a *domain.TMDBID from an int literal — story 403.
func tmdbIDPtr(v int) *domain.TMDBID {
	id := domain.TMDBID(v)
	return &id
}

func castCredit(personID int64, order *int, character string, episodes *int) people.SeriesCredit {
	ch := character
	return people.SeriesCredit{
		PersonID:      personID,
		Kind:          people.SeriesCreditCast,
		CharacterName: &ch,
		CreditOrder:   order,
		EpisodeCount:  episodes,
	}
}

func crewCredit(personID int64, dept, job string, episodes *int) people.SeriesCredit {
	d, j := dept, job
	return people.SeriesCredit{
		PersonID:     personID,
		Kind:         people.SeriesCreditCrew,
		Department:   &d,
		Job:          &j,
		EpisodeCount: episodes,
	}
}

// --- tests ---

func TestCastComposer_HappyPath_FullCastCrew(t *testing.T) {
	t.Parallel()
	deps, cache, canon, sp, persons, credits, _ := castBaseDeps(t)
	// 3 cast: Pedro (order=0, in current+other), Bella (order=1, current only),
	// Anna (order=2, current+Mindhunter).
	seedPerson(persons, 1, "Pedro Pascal", tmdbIDPtr(1001))
	seedPerson(persons, 2, "Bella Ramsey", tmdbIDPtr(1002))
	seedPerson(persons, 3, "Anna Torv", tmdbIDPtr(1003))
	sp.cast = []people.SeriesCredit{
		castCredit(1, new(0), "Joel Miller", new(9)),
		castCredit(2, new(1), "Ellie", new(9)),
		castCredit(3, new(2), "Tess", new(3)),
	}
	// 2 crew: Craig Mazin (Writing/Writer), Neil Druckmann (Production/EP).
	seedPerson(persons, 10, "Craig Mazin", tmdbIDPtr(2001))
	seedPerson(persons, 11, "Neil Druckmann", tmdbIDPtr(2002))
	sp.crew = []people.SeriesCredit{
		crewCredit(10, "Writing", "Writer", new(9)),
		crewCredit(11, "Production", "Executive Producer", new(9)),
	}
	// person_credits: Pedro is in TMDB show 200 (Game of Thrones — in library);
	// Anna is in TMDB show 300 (Mindhunter — in library).
	// Bella, Craig, Neil only appear on current series (TMDB 100).
	credits.rows[1] = []PersonCreditRef{{MediaType: "tv", TMDBMediaID: 100}, {MediaType: "tv", TMDBMediaID: 200}}
	credits.rows[2] = []PersonCreditRef{{MediaType: "tv", TMDBMediaID: 100}}
	credits.rows[3] = []PersonCreditRef{{MediaType: "tv", TMDBMediaID: 100}, {MediaType: "tv", TMDBMediaID: 300}}
	credits.rows[10] = []PersonCreditRef{{MediaType: "tv", TMDBMediaID: 100}}
	credits.rows[11] = []PersonCreditRef{{MediaType: "tv", TMDBMediaID: 100}}
	// Canon: 200 (GoT), 300 (Mindhunter) live in library + map to TMDB.
	canon.rows[200] = series.Canon{ID: 200, OriginalTitle: new("Game of Thrones"), TMDBID: tmdbIDPtr(200)}
	canon.rows[300] = series.Canon{ID: 300, OriginalTitle: new("Mindhunter"), TMDBID: tmdbIDPtr(300)}
	cache.byCanon[200] = []series.CacheEntry{{InstanceName: "alpha", SonarrSeriesID: 5, SeriesID: seriesIDPtr(200)}}
	cache.byCanon[300] = []series.CacheEntry{{InstanceName: "alpha", SonarrSeriesID: 7, SeriesID: seriesIDPtr(300)}}

	c := NewCastComposer(deps)
	d, err := c.Get(context.Background(), "alpha", 1, "en-US")
	require.NoError(t, err)
	require.Equal(t, 3, len(d.Cast))
	require.Equal(t, 2, len(d.Crew))
	require.Equal(t, 9, d.TotalEpisodeCount)
	require.Equal(t, "Pedro Pascal", d.Cast[0].Person.Name)
	require.True(t, d.Cast[0].InLibrary, "Pedro Pascal also in Game of Thrones")
	require.False(t, d.Cast[1].InLibrary, "Bella Ramsey only in current series")
	require.True(t, d.Cast[2].InLibrary, "Anna Torv also in Mindhunter")
	// Crew sorted by (department, name): Production/Neil before Writing/Craig.
	require.Equal(t, "Production", *d.Crew[0].Credit.Department)
	require.Equal(t, "Neil Druckmann", d.Crew[0].Person.Name)
	require.Equal(t, "Writing", *d.Crew[1].Credit.Department)
	require.Equal(t, "Craig Mazin", d.Crew[1].Person.Name)
}

func TestCastComposer_CastSortedByCreditOrder(t *testing.T) {
	t.Parallel()
	deps, _, _, sp, persons, _, _ := castBaseDeps(t)
	seedPerson(persons, 1, "A", nil)
	seedPerson(persons, 2, "B", nil)
	seedPerson(persons, 3, "C", nil)
	seedPerson(persons, 4, "D", nil)
	// ListBySeries already orders by credit_order ASC NULLS LAST —
	// simulate that ordering in the fixture (composer just preserves
	// repository order).
	sp.cast = []people.SeriesCredit{
		castCredit(2, new(0), "ch", nil),
		castCredit(3, new(3), "ch", nil),
		castCredit(1, new(5), "ch", nil),
		castCredit(4, nil, "ch", nil),
	}
	c := NewCastComposer(deps)
	d, err := c.Get(context.Background(), "alpha", 1, "en-US")
	require.NoError(t, err)
	require.Equal(t, []int64{2, 3, 1, 4}, []int64{
		d.Cast[0].Person.ID, d.Cast[1].Person.ID, d.Cast[2].Person.ID, d.Cast[3].Person.ID,
	})
}

func TestCastComposer_CrewGroupedByDepartmentThenName(t *testing.T) {
	t.Parallel()
	deps, _, _, sp, persons, _, _ := castBaseDeps(t)
	seedPerson(persons, 1, "Z", nil)
	seedPerson(persons, 2, "A", nil)
	seedPerson(persons, 3, "A", nil) // same name as id=2 (different person)
	seedPerson(persons, 4, "M", nil)
	sp.crew = []people.SeriesCredit{
		crewCredit(1, "Writing", "Writer", nil),
		crewCredit(2, "Directing", "Director", nil),
		crewCredit(3, "Writing", "Story Editor", nil),
		crewCredit(4, "Directing", "Director", nil),
	}
	c := NewCastComposer(deps)
	d, err := c.Get(context.Background(), "alpha", 1, "en-US")
	require.NoError(t, err)
	require.Equal(t, 4, len(d.Crew))
	// Expected sorted: (Directing, A) id=2, (Directing, M) id=4,
	// (Writing, A) id=3, (Writing, Z) id=1.
	require.Equal(t, int64(2), d.Crew[0].Person.ID)
	require.Equal(t, int64(4), d.Crew[1].Person.ID)
	require.Equal(t, int64(3), d.Crew[2].Person.ID)
	require.Equal(t, int64(1), d.Crew[3].Person.ID)
}

func TestCastComposer_DuplicateCrewJobsPreserved(t *testing.T) {
	t.Parallel()
	deps, _, _, sp, persons, _, _ := castBaseDeps(t)
	// Vince Gilligan: EP (Production) AND Director (Directing) on
	// the same series.
	seedPerson(persons, 1, "Vince Gilligan", nil)
	sp.crew = []people.SeriesCredit{
		crewCredit(1, "Production", "Executive Producer", new(9)),
		crewCredit(1, "Directing", "Director", new(2)),
	}
	c := NewCastComposer(deps)
	d, err := c.Get(context.Background(), "alpha", 1, "en-US")
	require.NoError(t, err)
	require.Equal(t, 2, len(d.Crew), "two rows for same person preserved")
	// Sort: Directing before Production.
	require.Equal(t, "Directing", *d.Crew[0].Credit.Department)
	require.Equal(t, "Director", *d.Crew[0].Credit.Job)
	require.Equal(t, "Production", *d.Crew[1].Credit.Department)
	require.Equal(t, "Executive Producer", *d.Crew[1].Credit.Job)
	require.Equal(t, int64(1), d.Crew[0].Person.ID)
	require.Equal(t, int64(1), d.Crew[1].Person.ID)
}

func TestCastComposer_TotalEpisodeCount_HappyAndZeroFallback(t *testing.T) {
	t.Parallel()
	t.Run("happy path", func(t *testing.T) {
		t.Parallel()
		deps, _, _, _, _, _, counts := castBaseDeps(t)
		counts.counts[42] = 62
		c := NewCastComposer(deps)
		d, err := c.Get(context.Background(), "alpha", 1, "en-US")
		require.NoError(t, err)
		require.Equal(t, 62, d.TotalEpisodeCount)
	})
	t.Run("count error → zero, no failure", func(t *testing.T) {
		deps, _, _, _, _, _, counts := castBaseDeps(t)
		counts.err = errors.New("boom")
		c := NewCastComposer(deps)
		d, err := c.Get(context.Background(), "alpha", 1, "en-US")
		require.NoError(t, err)
		require.Equal(t, 0, d.TotalEpisodeCount)
	})
}

func TestCastComposer_404_MissingCache(t *testing.T) {
	t.Parallel()
	deps, _, _, _, _, _, _ := castBaseDeps(t)
	c := NewCastComposer(deps)
	_, err := c.Get(context.Background(), "alpha", 999, "en-US")
	require.ErrorIs(t, err, ports.ErrNotFound)
}

func TestCastComposer_404_NilSeriesIDInCache(t *testing.T) {
	t.Parallel()
	deps, cache, _, _, _, _, _ := castBaseDeps(t)
	cache.entries[cacheKey("alpha", 2)] = series.CacheEntry{
		InstanceName: "alpha", SonarrSeriesID: 2, SeriesID: nil,
	}
	c := NewCastComposer(deps)
	_, err := c.Get(context.Background(), "alpha", 2, "en-US")
	require.ErrorIs(t, err, ports.ErrNotFound)
}

func TestCastComposer_CanonMissingPropagates(t *testing.T) {
	t.Parallel()
	deps, cache, _, _, _, _, _ := castBaseDeps(t)
	cache.entries[cacheKey("alpha", 3)] = series.CacheEntry{
		InstanceName: "alpha", SonarrSeriesID: 3, SeriesID: seriesIDPtr(999),
	}
	c := NewCastComposer(deps)
	_, err := c.Get(context.Background(), "alpha", 3, "en-US")
	require.Error(t, err)
	// fakeSeries.Get → ports.ErrNotFound for unknown id; composer wraps
	// but the sentinel propagates via errors.Is.
	require.ErrorIs(t, err, ports.ErrNotFound)
}

func TestCastComposer_SelfLinkSuppression(t *testing.T) {
	t.Parallel()
	deps, cache, canon, sp, persons, credits, _ := castBaseDeps(t)
	seedPerson(persons, 1, "Solo Actor", tmdbIDPtr(5001))
	sp.cast = []people.SeriesCredit{castCredit(1, new(0), "Hero", new(9))}
	// The only TV credit resolves to the CURRENT series (TMDB 100 → canon 42).
	credits.rows[1] = []PersonCreditRef{{MediaType: "tv", TMDBMediaID: 100}}
	// Make sure canon resolution for tmdb=100 → current series id 42.
	// fakeSeries.GetByTMDBID matches by TMDBID field; canon[42] has TMDBID 100.
	_ = canon
	_ = cache
	c := NewCastComposer(deps)
	d, err := c.Get(context.Background(), "alpha", 1, "en-US")
	require.NoError(t, err)
	require.Equal(t, 1, len(d.Cast))
	require.False(t, d.Cast[0].InLibrary, "person only on current series → no self-link")
}

func TestCastComposer_PersonRowMissing_SkippedGracefully(t *testing.T) {
	t.Parallel()
	deps, _, _, sp, persons, _, _ := castBaseDeps(t)
	seedPerson(persons, 1, "A", nil)
	// credit references person_id=9 which has no people row.
	sp.cast = []people.SeriesCredit{
		castCredit(1, new(0), "ch", nil),
		castCredit(9, new(1), "ch", nil),
	}
	c := NewCastComposer(deps)
	d, err := c.Get(context.Background(), "alpha", 1, "en-US")
	require.NoError(t, err)
	require.Equal(t, 1, len(d.Cast), "missing person row → that entry skipped")
	require.Equal(t, int64(1), d.Cast[0].Person.ID)
}

func TestCastComposer_SeriesSummary_HappyPath(t *testing.T) {
	t.Parallel()
	deps, _, canon, _, _, _, _ := castBaseDeps(t)
	// Replace the default canon row with a richer one so we can
	// assert every summary field individually.
	posterPath := "/poster.jpg"
	status := "Returning Series"
	lastAir := time.Date(2025, 4, 13, 0, 0, 0, 0, time.UTC)
	year := 2023
	canon.rows[42] = series.Canon{
		ID:            42,
		OriginalTitle: new("The Last of Us"),
		TMDBID:        tmdbIDPtr(100),
		Status:        &status,
		Year:          &year,
		LastAirDate:   &lastAir,
		InProduction:  false,
	}
	// S-E3a — hero title falls back to canon OriginalTitle (no SeriesTexts
	// fake); hero poster raw path now comes from series_media_texts.
	deps.SeriesMediaTexts = &fakeSkMediaTexts{row: series.SeriesMediaText{
		SeriesID: 42, Language: "en-US", PosterAsset: &posterPath,
	}}
	// Story 312: composer wraps the raw TMDB path through MediaResolver;
	// inject a fake lookup so the wire field carries the sha256 hash.
	const wantHash = "poster-asset-hash"
	deps.MediaResolver = media.NewResolver(&fakeMediaLookupCast{byURL: map[string]string{
		"https://image.tmdb.org/t/p/w342/poster.jpg": wantHash,
	}}, nil, nil, newSilentLogger())
	c := NewCastComposer(deps)
	d, err := c.Get(context.Background(), "alpha", 1, "en-US")
	require.NoError(t, err)
	require.Equal(t, "The Last of Us", d.Summary.Title)
	require.NotNil(t, d.Summary.PosterAsset)
	require.Equal(t, wantHash, *d.Summary.PosterAsset)
	require.Equal(t, "continuing", d.Summary.Status, "Returning Series → continuing")
	require.NotNil(t, d.Summary.FirstAiredYear)
	require.Equal(t, 2023, *d.Summary.FirstAiredYear)
	require.NotNil(t, d.Summary.LastAiredYear)
	require.Equal(t, 2025, *d.Summary.LastAiredYear)
}

// TestCastComposer_Hero_TitleFromSeriesTexts_EnUSFallback — D-0 (S-E3a): the
// cast hero title resolves from series_texts (requested-lang → en-US), winning
// over canon OriginalTitle; a series with no texts row degrades to
// OriginalTitle. Reuses the package fakeSeriesTexts keyed by seriesTextKey.
func TestCastComposer_Hero_TitleFromSeriesTexts_EnUSFallback(t *testing.T) {
	t.Parallel()
	t.Run("en-US series_texts renders under ru-RU request", func(t *testing.T) {
		t.Parallel()
		deps, _, canon, _, _, _, _ := castBaseDeps(t)
		canon.rows[42] = series.Canon{ID: 42, OriginalTitle: new("Canon Original"), TMDBID: tmdbIDPtr(100)}
		deps.SeriesTexts = &fakeSeriesTexts{rows: map[string]series.SeriesText{
			seriesTextKey(42, "en-US"): {SeriesID: 42, Language: "en-US", Title: new("English Title")},
		}}
		c := NewCastComposer(deps)
		d, err := c.Get(context.Background(), "alpha", 1, "ru-RU")
		require.NoError(t, err)
		require.Equal(t, "English Title", d.Summary.Title,
			"series_texts en-US fallback wins over canon OriginalTitle")
	})
	t.Run("no texts row degrades to canon OriginalTitle", func(t *testing.T) {
		t.Parallel()
		deps, _, canon, _, _, _, _ := castBaseDeps(t)
		canon.rows[42] = series.Canon{ID: 42, OriginalTitle: new("Only Original"), TMDBID: tmdbIDPtr(100)}
		deps.SeriesTexts = &fakeSeriesTexts{rows: map[string]series.SeriesText{}}
		c := NewCastComposer(deps)
		d, err := c.Get(context.Background(), "alpha", 1, "ru-RU")
		require.NoError(t, err)
		require.Equal(t, "Only Original", d.Summary.Title,
			"no series_texts row → canon OriginalTitle fallback")
	})
}

func TestCastComposer_SeriesSummary_StatusFallbacks(t *testing.T) {
	t.Parallel()
	// Walk the mapStatusToken switch arms to lock the contract.
	cases := []struct {
		name         string
		raw          *string
		inProduction bool
		want         string
	}{
		{"ended", new("Ended"), false, "ended"},
		{"canceled", new("Canceled"), false, "canceled"},
		{"upcoming", new("Upcoming"), false, "upcoming"},
		{"planned", new("Planned"), false, "upcoming"},
		{"continuing", new("Continuing"), false, "continuing"},
		{"in_production", new("In Production"), false, "in_production"},
		{"post_production_excluded", new("Post Production"), false, "unknown"},
		{"inProduction_only", nil, true, "in_production"},
		{"empty", nil, false, "unknown"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			deps, _, canon, _, _, _, _ := castBaseDeps(t)
			canon.rows[42] = series.Canon{
				ID:            42,
				OriginalTitle: new("X"),
				Status:        tc.raw,
				InProduction:  tc.inProduction,
			}
			c := NewCastComposer(deps)
			d, err := c.Get(context.Background(), "alpha", 1, "en-US")
			require.NoError(t, err)
			require.Equal(t, tc.want, d.Summary.Status)
		})
	}
}

func TestCastComposer_SeriesSummary_NilYears(t *testing.T) {
	t.Parallel()
	deps, _, canon, _, _, _, _ := castBaseDeps(t)
	canon.rows[42] = series.Canon{
		ID:            42,
		OriginalTitle: new("Stub series"),
	}
	c := NewCastComposer(deps)
	d, err := c.Get(context.Background(), "alpha", 1, "en-US")
	require.NoError(t, err)
	require.Nil(t, d.Summary.FirstAiredYear)
	require.Nil(t, d.Summary.LastAiredYear)
	require.Nil(t, d.Summary.PosterAsset)
	// S-E3a — hero title falls back to canon OriginalTitle (no SeriesTexts).
	require.Equal(t, "Stub series", d.Summary.Title)
	require.Equal(t, "unknown", d.Summary.Status)
}

// #1038 — FirstAiredYear falls back to first_air_date's year when canon.Year
// is nil (heals TMDB-only rows whose year column was never derived).
func TestCastComposer_SeriesSummary_FirstAiredYearFromFirstAirDate(t *testing.T) {
	t.Parallel()
	deps, _, canon, _, _, _, _ := castBaseDeps(t)
	fad := time.Date(2022, 8, 21, 0, 0, 0, 0, time.UTC)
	canon.rows[42] = series.Canon{
		ID:            42,
		OriginalTitle: new("House of the Dragon"),
		TMDBID:        tmdbIDPtr(100),
		FirstAirDate:  &fad,
	}
	c := NewCastComposer(deps)
	d, err := c.Get(context.Background(), "alpha", 1, "en-US")
	require.NoError(t, err)
	require.NotNil(t, d.Summary.FirstAiredYear)
	require.Equal(t, 2022, *d.Summary.FirstAiredYear)
}

// --- story 312 ---

type fakeMediaLookupCast struct {
	byURL map[string]string
}

func (f *fakeMediaLookupCast) HashForSourceURL(_ context.Context, url string) (string, error) {
	if h, ok := f.byURL[url]; ok {
		return h, nil
	}
	return "", ports.ErrNotFound
}

func (f *fakeMediaLookupCast) EnsurePending(_ context.Context, _, _, _ string) error {
	return nil
}

func TestCastComposer_Get_ResolvesSummaryAndProfileAssets(t *testing.T) {
	t.Parallel()
	deps, _, canon, sp, persons, _, _ := castBaseDeps(t)
	// Seed canon + one cast member with raw profile path. S-E3a — hero poster
	// raw path comes from series_media_texts (canon dropped poster_asset).
	canon.rows[42] = series.Canon{
		ID: 42, OriginalTitle: new("Breaking Bad"),
	}
	deps.SeriesMediaTexts = &fakeSkMediaTexts{row: series.SeriesMediaText{
		SeriesID: 42, Language: "en-US", PosterAsset: new("/hero.jpg"),
	}}
	sp.cast = []people.SeriesCredit{
		{PersonID: 100, Kind: people.SeriesCreditCast, CreditOrder: new(1)},
	}
	persons.rows[100] = people.Person{
		ID: 100, Name: "Bryan Cranston", ProfileAsset: new("/bryan.jpg"),
	}

	const hashPoster = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	const hashCast = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	deps.MediaResolver = media.NewResolver(&fakeMediaLookupCast{byURL: map[string]string{
		"https://image.tmdb.org/t/p/w342/hero.jpg":  hashPoster,
		"https://image.tmdb.org/t/p/w185/bryan.jpg": hashCast,
	}}, nil, nil, newSilentLogger())

	c := NewCastComposer(deps)
	d, err := c.Get(context.Background(), "alpha", 1, "en-US")
	require.NoError(t, err)
	require.NotNil(t, d.Summary.PosterAsset)
	require.Equal(t, hashPoster, *d.Summary.PosterAsset)
	require.Len(t, d.Cast, 1)
	require.NotNil(t, d.Cast[0].Person.ProfileAsset)
	require.Equal(t, hashCast, *d.Cast[0].Person.ProfileAsset)
}

// --- Story 556 (E-1 Z7) batch in_library tests ---

// countingSeries wraps fakeSeries and increments per-method counters
// so the query-budget SLO test can assert that ListByTMDBIDs is the
// only batch path hit (not the legacy per-credit GetByTMDBID).
type countingSeries struct {
	inner              *fakeSeries
	listByTMDBIDsCalls atomic.Int32
	getByTMDBIDCalls   atomic.Int32
	getCalls           atomic.Int32
	listByIDsCalls     atomic.Int32
}

func (f *countingSeries) Get(ctx context.Context, id domain.SeriesID) (series.Canon, error) {
	f.getCalls.Add(1)
	return f.inner.Get(ctx, id)
}

func (f *countingSeries) GetByTMDBID(ctx context.Context, tmdbID domain.TMDBID) (series.Canon, error) {
	f.getByTMDBIDCalls.Add(1)
	return f.inner.GetByTMDBID(ctx, tmdbID)
}

func (f *countingSeries) ListByIDs(ctx context.Context, ids []domain.SeriesID) ([]series.Canon, error) {
	f.listByIDsCalls.Add(1)
	return f.inner.ListByIDs(ctx, ids)
}

func (f *countingSeries) ListByTMDBIDs(ctx context.Context, tmdbIDs []domain.TMDBID) ([]series.Canon, error) {
	f.listByTMDBIDsCalls.Add(1)
	return f.inner.ListByTMDBIDs(ctx, tmdbIDs)
}

// countingSeriesCache wraps fakeSeriesCache and counts the batch path.
type countingSeriesCache struct {
	inner                *fakeSeriesCache
	listBySeriesIDsCalls atomic.Int32
	listBySeriesIDCalls  atomic.Int32
}

func (f *countingSeriesCache) Get(ctx context.Context, instance domain.InstanceName, sonarrID domain.SonarrSeriesID) (series.CacheEntry, error) {
	return f.inner.Get(ctx, instance, sonarrID)
}

func (f *countingSeriesCache) ListBySeriesID(ctx context.Context, id domain.SeriesID) ([]series.CacheEntry, error) {
	f.listBySeriesIDCalls.Add(1)
	return f.inner.ListBySeriesID(ctx, id)
}

func (f *countingSeriesCache) ListBySeriesIDs(ctx context.Context, ids []domain.SeriesID) (map[domain.SeriesID][]series.CacheEntry, error) {
	f.listBySeriesIDsCalls.Add(1)
	return f.inner.ListBySeriesIDs(ctx, ids)
}

func TestCastComposer_InLibraryBatchedAcrossCast(t *testing.T) {
	t.Parallel()
	deps, cache, canon, sp, persons, credits, _ := castBaseDeps(t)
	// Person A (id=1) — 2 TV credits: TMDB 200 (in library) + TMDB 999 (no canon).
	// Person B (id=2) — 1 TV credit on TMDB 100 (current series only) → not in library.
	// Person C (id=3) — no TV credits at all.
	seedPerson(persons, 1, "PersonA", tmdbIDPtr(1001))
	seedPerson(persons, 2, "PersonB", tmdbIDPtr(1002))
	seedPerson(persons, 3, "PersonC", tmdbIDPtr(1003))
	sp.cast = []people.SeriesCredit{
		castCredit(1, new(0), "cha", new(9)),
		castCredit(2, new(1), "chb", new(9)),
		castCredit(3, new(2), "chc", new(9)),
	}
	credits.rows[1] = []PersonCreditRef{
		{MediaType: "tv", TMDBMediaID: 200},
		{MediaType: "tv", TMDBMediaID: 999},
	}
	credits.rows[2] = []PersonCreditRef{{MediaType: "tv", TMDBMediaID: 100}}
	credits.rows[3] = nil
	// Canon for tmdb=200 exists + lives in library; tmdb=999 has no canon row.
	canon.rows[200] = series.Canon{ID: 200, OriginalTitle: new("GoT"), TMDBID: tmdbIDPtr(200)}
	cache.byCanon[200] = []series.CacheEntry{{InstanceName: "alpha", SonarrSeriesID: 5, SeriesID: seriesIDPtr(200)}}

	c := NewCastComposer(deps)
	d, err := c.Get(context.Background(), "alpha", 1, "en-US")
	require.NoError(t, err)
	require.Len(t, d.Cast, 3)
	require.True(t, d.Cast[0].InLibrary, "Person A has TMDB 200 hit")
	require.False(t, d.Cast[1].InLibrary, "Person B only on current series")
	require.False(t, d.Cast[2].InLibrary, "Person C has zero credits")
}

func TestCastComposer_InLibrarySelfSuppressed_Batched(t *testing.T) {
	t.Parallel()
	deps, _, _, sp, persons, credits, _ := castBaseDeps(t)
	seedPerson(persons, 1, "Solo", tmdbIDPtr(5001))
	sp.cast = []people.SeriesCredit{castCredit(1, new(0), "Hero", new(9))}
	// TMDB 100 → current series id 42; pass 3 must drop it before cache lookup.
	credits.rows[1] = []PersonCreditRef{{MediaType: "tv", TMDBMediaID: 100}}
	c := NewCastComposer(deps)
	d, err := c.Get(context.Background(), "alpha", 1, "en-US")
	require.NoError(t, err)
	require.Len(t, d.Cast, 1)
	require.False(t, d.Cast[0].InLibrary, "self-link must be suppressed in batch path")
}

// failingListByTMDBIDsSeries wraps a base fakeSeries and only errors on
// the batch ListByTMDBIDs call — Step 2's canon Get must still succeed
// so the composer reaches the in_library batch path under test.
type failingListByTMDBIDsSeries struct {
	inner *fakeSeries
	err   error
}

func (f *failingListByTMDBIDsSeries) Get(ctx context.Context, id domain.SeriesID) (series.Canon, error) {
	return f.inner.Get(ctx, id)
}

func (f *failingListByTMDBIDsSeries) GetByTMDBID(ctx context.Context, tmdbID domain.TMDBID) (series.Canon, error) {
	return f.inner.GetByTMDBID(ctx, tmdbID)
}

func (f *failingListByTMDBIDsSeries) ListByIDs(ctx context.Context, ids []domain.SeriesID) ([]series.Canon, error) {
	return f.inner.ListByIDs(ctx, ids)
}

func (f *failingListByTMDBIDsSeries) ListByTMDBIDs(_ context.Context, _ []domain.TMDBID) ([]series.Canon, error) {
	return nil, f.err
}

func TestCastComposer_InLibrarySeriesBatchFailure(t *testing.T) {
	t.Parallel()
	deps, _, canon, sp, persons, credits, _ := castBaseDeps(t)
	seedPerson(persons, 1, "PersonA", tmdbIDPtr(1001))
	seedPerson(persons, 2, "PersonB", tmdbIDPtr(1002))
	sp.cast = []people.SeriesCredit{
		castCredit(1, new(0), "cha", new(9)),
		castCredit(2, new(1), "chb", new(9)),
	}
	credits.rows[1] = []PersonCreditRef{{MediaType: "tv", TMDBMediaID: 200}}
	credits.rows[2] = []PersonCreditRef{{MediaType: "tv", TMDBMediaID: 300}}
	deps.Series = &failingListByTMDBIDsSeries{inner: canon, err: errors.New("series batch down")}
	c := NewCastComposer(deps)
	d, err := c.Get(context.Background(), "alpha", 1, "en-US")
	require.NoError(t, err, "series batch failure must NOT propagate — response ships degraded")
	require.Len(t, d.Cast, 2)
	require.False(t, d.Cast[0].InLibrary)
	require.False(t, d.Cast[1].InLibrary)
}

func TestCastComposer_InLibraryCacheBatchFailure(t *testing.T) {
	t.Parallel()
	deps, cache, canon, sp, persons, credits, _ := castBaseDeps(t)
	seedPerson(persons, 1, "PersonA", tmdbIDPtr(1001))
	sp.cast = []people.SeriesCredit{castCredit(1, new(0), "cha", new(9))}
	credits.rows[1] = []PersonCreditRef{{MediaType: "tv", TMDBMediaID: 200}}
	canon.rows[200] = series.Canon{ID: 200, OriginalTitle: new("GoT"), TMDBID: tmdbIDPtr(200)}
	cache.listErr = errors.New("cache batch down")
	c := NewCastComposer(deps)
	d, err := c.Get(context.Background(), "alpha", 1, "en-US")
	require.NoError(t, err, "cache batch failure must NOT propagate")
	require.Len(t, d.Cast, 1)
	require.False(t, d.Cast[0].InLibrary)
}

func TestCastComposer_InLibraryZeroTMDBIDsIgnored(t *testing.T) {
	t.Parallel()
	deps, _, _, sp, persons, credits, _ := castBaseDeps(t)
	seedPerson(persons, 1, "PersonA", tmdbIDPtr(1001))
	sp.cast = []people.SeriesCredit{castCredit(1, new(0), "cha", new(9))}
	// Only credit has TMDBMediaID=0 — must NOT trigger a canon lookup.
	credits.rows[1] = []PersonCreditRef{{MediaType: "tv", TMDBMediaID: 0}}
	c := NewCastComposer(deps)
	d, err := c.Get(context.Background(), "alpha", 1, "en-US")
	require.NoError(t, err)
	require.Len(t, d.Cast, 1)
	require.False(t, d.Cast[0].InLibrary, "TMDBMediaID=0 must be ignored")
}

func TestCastComposer_QueryCountBudget(t *testing.T) {
	t.Parallel()
	deps, baseCache, baseCanon, sp, persons, credits, _ := castBaseDeps(t)
	// 5 cast members, each with 3 distinct TV credits → 15 unique tmdb ids.
	// All non-current series live in library.
	for i := 1; i <= 5; i++ {
		seedPerson(persons, int64(i), "P", tmdbIDPtr(1000+i))
		sp.cast = append(sp.cast, castCredit(int64(i), new(i-1), "c", new(9)))
		var refs []PersonCreditRef
		for j := range 3 {
			tmdb := 2000 + i*10 + j
			refs = append(refs, PersonCreditRef{MediaType: "tv", TMDBMediaID: tmdb})
			baseCanon.rows[domain.SeriesID(tmdb)] = series.Canon{
				ID: domain.SeriesID(tmdb), OriginalTitle: new("X"), TMDBID: tmdbIDPtr(tmdb),
			}
			baseCache.byCanon[domain.SeriesID(tmdb)] = []series.CacheEntry{
				{InstanceName: "alpha", SonarrSeriesID: domain.SonarrSeriesID(tmdb), SeriesID: seriesIDPtr(int64(tmdb))},
			}
		}
		credits.rows[int64(i)] = refs
	}
	cs := &countingSeries{inner: baseCanon}
	cc := &countingSeriesCache{inner: baseCache}
	deps.Series = cs
	deps.SeriesCache = cc
	deps.SeriesCacheLookup = cc

	c := NewCastComposer(deps)
	d, err := c.Get(context.Background(), "alpha", 1, "en-US")
	require.NoError(t, err)
	require.Len(t, d.Cast, 5)
	for _, ent := range d.Cast {
		require.True(t, ent.InLibrary, "every cast member has a library hit")
	}
	// SLO: per-credit GetByTMDBID + per-resolved ListBySeriesID MUST be zero
	// (the legacy path); batched ListByTMDBIDs + ListBySeriesIDs hit once each.
	require.Zero(t, cs.getByTMDBIDCalls.Load(), "legacy per-credit GetByTMDBID MUST be zero")
	require.Zero(t, cc.listBySeriesIDCalls.Load(), "legacy per-resolved ListBySeriesID MUST be zero")
	require.LessOrEqual(t, cs.listByTMDBIDsCalls.Load(), int32(1), "series batch fires at most once")
	require.LessOrEqual(t, cc.listBySeriesIDsCalls.Load(), int32(1), "cache batch fires at most once")
}

// W15-9 — served-language contract on the cast page (hero summary title
// served via SeriesTexts.GetWithFallback).
func TestCastComposer_ServedLanguage(t *testing.T) {
	t.Parallel()

	t.Run("summary-title fallback lang surfaced", func(t *testing.T) {
		t.Parallel()
		deps, _, _, _, _, _, _ := castBaseDeps(t)
		deps.SeriesTexts = &fakeSkSeriesTexts{row: series.SeriesText{
			SeriesID: 42, Language: "en-US", Title: new("The Last of Us"),
		}}
		out, err := NewCastComposer(deps).Get(context.Background(), "alpha", 1, "ru-RU")
		require.NoError(t, err)
		require.Equal(t, "en-US", out.ServedLanguage)
	})

	t.Run("summary title in requested lang → served=requested", func(t *testing.T) {
		t.Parallel()
		deps, _, _, _, _, _, _ := castBaseDeps(t)
		deps.SeriesTexts = &fakeSkSeriesTexts{row: series.SeriesText{
			SeriesID: 42, Language: "ru-RU", Title: new("Одни из нас"),
		}}
		out, err := NewCastComposer(deps).Get(context.Background(), "alpha", 1, "ru-RU")
		require.NoError(t, err)
		require.Equal(t, "ru-RU", out.ServedLanguage)
	})

	t.Run("no series_texts (original_title path) → served empty", func(t *testing.T) {
		t.Parallel()
		deps, _, _, _, _, _, _ := castBaseDeps(t)
		// SeriesTexts unwired → hero title falls to canon.OriginalTitle.
		out, err := NewCastComposer(deps).Get(context.Background(), "alpha", 1, "ru-RU")
		require.NoError(t, err)
		require.Empty(t, out.ServedLanguage)
	})
}
