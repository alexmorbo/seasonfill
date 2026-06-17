package seriesdetail

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/application/ports"
	"github.com/alexmorbo/seasonfill/domain/people"
	"github.com/alexmorbo/seasonfill/domain/series"
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

func (f *fakeCastSeriesPeople) ListBySeries(_ context.Context, _ int64, kind people.SeriesCreditKind) ([]people.SeriesCredit, error) {
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
	counts map[int64]int
	err    error
}

func (f *fakeEpisodesCount) CountBySeries(_ context.Context, seriesID int64) (int, error) {
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
				SeriesID:       i64ptr(42),
				Title:          "The Last of Us",
				Monitored:      true,
			},
		},
		byCanon: map[int64][]series.CacheEntry{},
	}
	canon := &fakeSeries{
		rows: map[int64]series.Canon{
			42: {ID: 42, Title: "The Last of Us", TMDBID: intPtr(100)},
		},
	}
	sp := &fakeCastSeriesPeople{}
	persons := &fakeCastPeople{rows: map[int64]people.Person{}}
	credits := &fakePersonCredits{rows: map[int64][]PersonCreditRef{}}
	counts := &fakeEpisodesCount{counts: map[int64]int{42: 9}}
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

func seedPerson(persons *fakeCastPeople, id int64, name string, tmdbID *int) {
	persons.rows[id] = people.Person{ID: id, Name: name, TMDBID: tmdbID}
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
	seedPerson(persons, 1, "Pedro Pascal", intPtr(1001))
	seedPerson(persons, 2, "Bella Ramsey", intPtr(1002))
	seedPerson(persons, 3, "Anna Torv", intPtr(1003))
	sp.cast = []people.SeriesCredit{
		castCredit(1, intPtr(0), "Joel Miller", intPtr(9)),
		castCredit(2, intPtr(1), "Ellie", intPtr(9)),
		castCredit(3, intPtr(2), "Tess", intPtr(3)),
	}
	// 2 crew: Craig Mazin (Writing/Writer), Neil Druckmann (Production/EP).
	seedPerson(persons, 10, "Craig Mazin", intPtr(2001))
	seedPerson(persons, 11, "Neil Druckmann", intPtr(2002))
	sp.crew = []people.SeriesCredit{
		crewCredit(10, "Writing", "Writer", intPtr(9)),
		crewCredit(11, "Production", "Executive Producer", intPtr(9)),
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
	canon.rows[200] = series.Canon{ID: 200, Title: "Game of Thrones", TMDBID: intPtr(200)}
	canon.rows[300] = series.Canon{ID: 300, Title: "Mindhunter", TMDBID: intPtr(300)}
	cache.byCanon[200] = []series.CacheEntry{{InstanceName: "alpha", SonarrSeriesID: 5, SeriesID: i64ptr(200)}}
	cache.byCanon[300] = []series.CacheEntry{{InstanceName: "alpha", SonarrSeriesID: 7, SeriesID: i64ptr(300)}}

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
		castCredit(2, intPtr(0), "ch", nil),
		castCredit(3, intPtr(3), "ch", nil),
		castCredit(1, intPtr(5), "ch", nil),
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
		crewCredit(1, "Production", "Executive Producer", intPtr(9)),
		crewCredit(1, "Directing", "Director", intPtr(2)),
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
		InstanceName: "alpha", SonarrSeriesID: 3, SeriesID: i64ptr(999),
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
	seedPerson(persons, 1, "Solo Actor", intPtr(5001))
	sp.cast = []people.SeriesCredit{castCredit(1, intPtr(0), "Hero", intPtr(9))}
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
		castCredit(1, intPtr(0), "ch", nil),
		castCredit(9, intPtr(1), "ch", nil),
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
		ID:           42,
		Title:        "The Last of Us",
		TMDBID:       intPtr(100),
		PosterAsset:  &posterPath,
		Status:       &status,
		Year:         &year,
		LastAirDate:  &lastAir,
		InProduction: false,
	}
	// Story 312: composer wraps the raw TMDB path through MediaResolver;
	// inject a fake lookup so the wire field carries the sha256 hash.
	const wantHash = "poster-asset-hash"
	deps.MediaResolver = NewMediaResolver(&fakeMediaLookupCast{byURL: map[string]string{
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

func TestCastComposer_SeriesSummary_StatusFallbacks(t *testing.T) {
	t.Parallel()
	// Walk the mapStatusToken switch arms to lock the contract.
	cases := []struct {
		name         string
		raw          *string
		inProduction bool
		want         string
	}{
		{"ended", strPtr("Ended"), false, "ended"},
		{"canceled", strPtr("Canceled"), false, "canceled"},
		{"upcoming", strPtr("Upcoming"), false, "upcoming"},
		{"planned", strPtr("Planned"), false, "upcoming"},
		{"continuing", strPtr("Continuing"), false, "continuing"},
		{"in_production", strPtr("In Production"), false, "in_production"},
		{"post_production_excluded", strPtr("Post Production"), false, "unknown"},
		{"inProduction_only", nil, true, "in_production"},
		{"empty", nil, false, "unknown"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			deps, _, canon, _, _, _, _ := castBaseDeps(t)
			canon.rows[42] = series.Canon{
				ID:           42,
				Title:        "X",
				Status:       tc.raw,
				InProduction: tc.inProduction,
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
		ID:    42,
		Title: "Stub series",
	}
	c := NewCastComposer(deps)
	d, err := c.Get(context.Background(), "alpha", 1, "en-US")
	require.NoError(t, err)
	require.Nil(t, d.Summary.FirstAiredYear)
	require.Nil(t, d.Summary.LastAiredYear)
	require.Nil(t, d.Summary.PosterAsset)
	require.Equal(t, "Stub series", d.Summary.Title)
	require.Equal(t, "unknown", d.Summary.Status)
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
	// Seed canon poster + one cast member with raw profile path.
	canon.rows[42] = series.Canon{
		ID: 42, Title: "Breaking Bad", PosterAsset: strPtr("/hero.jpg"),
	}
	sp.cast = []people.SeriesCredit{
		{PersonID: 100, Kind: people.SeriesCreditCast, CreditOrder: intPtr(1)},
	}
	persons.rows[100] = people.Person{
		ID: 100, Name: "Bryan Cranston", ProfileAsset: strPtr("/bryan.jpg"),
	}

	const hashPoster = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	const hashCast = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	deps.MediaResolver = NewMediaResolver(&fakeMediaLookupCast{byURL: map[string]string{
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
