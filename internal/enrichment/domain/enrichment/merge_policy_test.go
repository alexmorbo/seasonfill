package enrichment

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// TestMergeSeries covers every PRD §5.4 series row.
// One sub-test per row — the table column "rule" documents
// the priority rule under test.
func TestMergeSeries(t *testing.T) {
	t.Parallel()
	day1 := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	day2 := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	cases := []struct {
		name   string
		rule   string
		canon  SeriesCanon
		patch  SeriesPatch
		source Source
		assert func(t *testing.T, got SeriesCanon)
	}{
		// --- Title: Sonarr > TMDB ---
		{
			name:   "Title Sonarr overwrites TMDB",
			rule:   "Title: Sonarr > TMDB",
			canon:  SeriesCanon{Title: "TMDB Title"},
			patch:  SeriesPatch{Title: new("Sonarr Title")},
			source: SourceSonarr,
			assert: func(t *testing.T, g SeriesCanon) {
				assert.Equal(t, "Sonarr Title", g.Title)
			},
		},
		{
			name:   "Title TMDB fills empty canon",
			rule:   "Title: TMDB fallback",
			canon:  SeriesCanon{Title: ""},
			patch:  SeriesPatch{Title: new("TMDB Title")},
			source: SourceTMDBSeries,
			assert: func(t *testing.T, g SeriesCanon) {
				assert.Equal(t, "TMDB Title", g.Title)
			},
		},
		{
			name:   "Title TMDB does NOT overwrite Sonarr canon",
			rule:   "Title: TMDB lower priority",
			canon:  SeriesCanon{Title: "Sonarr Existing"},
			patch:  SeriesPatch{Title: new("TMDB New")},
			source: SourceTMDBSeries,
			assert: func(t *testing.T, g SeriesCanon) {
				assert.Equal(t, "Sonarr Existing", g.Title)
			},
		},
		// --- OriginalTitle: TMDB only ---
		{
			name:   "OriginalTitle TMDB writes",
			rule:   "OriginalTitle: TMDB only",
			canon:  SeriesCanon{},
			patch:  SeriesPatch{OriginalTitle: new("オリジナル")},
			source: SourceTMDBSeries,
			assert: func(t *testing.T, g SeriesCanon) {
				assert.Equal(t, "オリジナル", *g.OriginalTitle)
			},
		},
		{
			name:   "OriginalTitle Sonarr no-op",
			rule:   "OriginalTitle: Sonarr no authority",
			canon:  SeriesCanon{},
			patch:  SeriesPatch{OriginalTitle: new("ignored")},
			source: SourceSonarr,
			assert: func(t *testing.T, g SeriesCanon) {
				assert.Nil(t, g.OriginalTitle)
			},
		},
		// --- Status: TMDB > Sonarr ---
		{
			name:   "Status TMDB overwrites Sonarr",
			rule:   "Status: TMDB > Sonarr",
			canon:  SeriesCanon{Status: new("continuing")},
			patch:  SeriesPatch{Status: new("Returning Series")},
			source: SourceTMDBSeries,
			assert: func(t *testing.T, g SeriesCanon) {
				assert.Equal(t, "Returning Series", *g.Status)
			},
		},
		{
			name:   "Status Sonarr fills empty canon",
			rule:   "Status: Sonarr fallback",
			canon:  SeriesCanon{},
			patch:  SeriesPatch{Status: new("continuing")},
			source: SourceSonarr,
			assert: func(t *testing.T, g SeriesCanon) {
				assert.Equal(t, "continuing", *g.Status)
			},
		},
		{
			name:   "Status Sonarr does NOT overwrite TMDB canon",
			rule:   "Status: Sonarr lower priority",
			canon:  SeriesCanon{Status: new("Returning Series")},
			patch:  SeriesPatch{Status: new("continuing")},
			source: SourceSonarr,
			assert: func(t *testing.T, g SeriesCanon) {
				assert.Equal(t, "Returning Series", *g.Status)
			},
		},
		// --- FirstAirDate, LastAirDate: TMDB > Sonarr ---
		{
			name:   "FirstAirDate TMDB overwrites",
			rule:   "FirstAirDate: TMDB > Sonarr",
			canon:  SeriesCanon{FirstAirDate: new(day1)},
			patch:  SeriesPatch{FirstAirDate: new(day2)},
			source: SourceTMDBSeries,
			assert: func(t *testing.T, g SeriesCanon) {
				assert.Equal(t, day2, *g.FirstAirDate)
			},
		},
		{
			name:   "LastAirDate TMDB overwrites",
			rule:   "LastAirDate: TMDB > Sonarr",
			canon:  SeriesCanon{LastAirDate: new(day1)},
			patch:  SeriesPatch{LastAirDate: new(day2)},
			source: SourceTMDBSeries,
			assert: func(t *testing.T, g SeriesCanon) {
				assert.Equal(t, day2, *g.LastAirDate)
			},
		},
		{
			name:   "FirstAirDate Sonarr fills empty",
			rule:   "FirstAirDate: Sonarr fallback",
			canon:  SeriesCanon{},
			patch:  SeriesPatch{FirstAirDate: new(day1)},
			source: SourceSonarr,
			assert: func(t *testing.T, g SeriesCanon) {
				assert.Equal(t, day1, *g.FirstAirDate)
			},
		},
		{
			name:   "LastAirDate Sonarr does NOT overwrite existing canon",
			rule:   "LastAirDate: Sonarr fallback-only",
			canon:  SeriesCanon{LastAirDate: new(day1)},
			patch:  SeriesPatch{LastAirDate: new(day2)},
			source: SourceSonarr,
			assert: func(t *testing.T, g SeriesCanon) {
				assert.Equal(t, day1, *g.LastAirDate)
			},
		},
		{
			name:   "LastAirDate Sonarr fills empty",
			rule:   "LastAirDate: Sonarr fallback",
			canon:  SeriesCanon{},
			patch:  SeriesPatch{LastAirDate: new(day1)},
			source: SourceSonarr,
			assert: func(t *testing.T, g SeriesCanon) {
				assert.Equal(t, day1, *g.LastAirDate)
			},
		},
		// --- NextAirDate: TMDB > Sonarr (Sonarr fallback-only) ---
		{
			name:   "NextAirDate Sonarr does NOT overwrite existing canon",
			rule:   "NextAirDate: Sonarr fallback-only",
			canon:  SeriesCanon{NextAirDate: new(day1)},
			patch:  SeriesPatch{NextAirDate: new(day2)},
			source: SourceSonarr,
			assert: func(t *testing.T, g SeriesCanon) {
				assert.Equal(t, day1, *g.NextAirDate)
			},
		},
		{
			name:   "NextAirDate Sonarr fills empty",
			rule:   "NextAirDate: Sonarr fallback",
			canon:  SeriesCanon{},
			patch:  SeriesPatch{NextAirDate: new(day1)},
			source: SourceSonarr,
			assert: func(t *testing.T, g SeriesCanon) {
				assert.Equal(t, day1, *g.NextAirDate)
			},
		},
		{
			name:   "NextAirDate TMDB overwrites existing canon",
			rule:   "NextAirDate: TMDB > Sonarr",
			canon:  SeriesCanon{NextAirDate: new(day1)},
			patch:  SeriesPatch{NextAirDate: new(day2)},
			source: SourceTMDBSeries,
			assert: func(t *testing.T, g SeriesCanon) {
				assert.Equal(t, day2, *g.NextAirDate)
			},
		},
		// --- Year: TMDB > Sonarr (Sonarr fallback-only) ---
		{
			name:   "Year Sonarr does NOT overwrite existing canon",
			rule:   "Year: Sonarr fallback-only",
			canon:  SeriesCanon{Year: new(2020)},
			patch:  SeriesPatch{Year: new(2021)},
			source: SourceSonarr,
			assert: func(t *testing.T, g SeriesCanon) {
				assert.Equal(t, 2020, *g.Year)
			},
		},
		{
			name:   "Year Sonarr fills empty",
			rule:   "Year: Sonarr fallback",
			canon:  SeriesCanon{},
			patch:  SeriesPatch{Year: new(2020)},
			source: SourceSonarr,
			assert: func(t *testing.T, g SeriesCanon) {
				assert.Equal(t, 2020, *g.Year)
			},
		},
		{
			name:   "Year TMDB overwrites existing canon",
			rule:   "Year: TMDB > Sonarr",
			canon:  SeriesCanon{Year: new(2020)},
			patch:  SeriesPatch{Year: new(2021)},
			source: SourceTMDBSeries,
			assert: func(t *testing.T, g SeriesCanon) {
				assert.Equal(t, 2021, *g.Year)
			},
		},
		{
			name:   "Year TMDB derived from first_air_date when patch year nil",
			rule:   "Year: derived from first_air_date",
			canon:  SeriesCanon{},
			patch:  SeriesPatch{FirstAirDate: new(day2)},
			source: SourceTMDBSeries,
			assert: func(t *testing.T, g SeriesCanon) {
				assert.NotNil(t, g.Year)
				assert.Equal(t, day2.Year(), *g.Year)
			},
		},
		{
			name:   "Year TMDB stays nil when both year and first_air_date absent",
			rule:   "Year: no derive without a date",
			canon:  SeriesCanon{},
			patch:  SeriesPatch{},
			source: SourceTMDBSeries,
			assert: func(t *testing.T, g SeriesCanon) {
				assert.Nil(t, g.Year)
			},
		},
		{
			name:   "Year TMDB explicit patch year not clobbered by derive",
			rule:   "Year: explicit wins over derive",
			canon:  SeriesCanon{},
			patch:  SeriesPatch{Year: new(1999), FirstAirDate: new(day2)},
			source: SourceTMDBSeries,
			assert: func(t *testing.T, g SeriesCanon) {
				assert.Equal(t, 1999, *g.Year)
			},
		},
		{
			name:   "Year Sonarr derived from first_air_date fill-empty",
			rule:   "Year: Sonarr derive from date",
			canon:  SeriesCanon{},
			patch:  SeriesPatch{FirstAirDate: new(day1)},
			source: SourceSonarr,
			assert: func(t *testing.T, g SeriesCanon) {
				assert.NotNil(t, g.Year)
				assert.Equal(t, day1.Year(), *g.Year)
			},
		},
		// --- RuntimeMinutes: TMDB > Sonarr (Sonarr fallback-only) ---
		{
			name:   "RuntimeMinutes Sonarr does NOT overwrite existing canon",
			rule:   "RuntimeMinutes: Sonarr fallback-only",
			canon:  SeriesCanon{RuntimeMinutes: new(45)},
			patch:  SeriesPatch{RuntimeMinutes: new(60)},
			source: SourceSonarr,
			assert: func(t *testing.T, g SeriesCanon) {
				assert.Equal(t, 45, *g.RuntimeMinutes)
			},
		},
		{
			name:   "RuntimeMinutes Sonarr fills empty",
			rule:   "RuntimeMinutes: Sonarr fallback",
			canon:  SeriesCanon{},
			patch:  SeriesPatch{RuntimeMinutes: new(60)},
			source: SourceSonarr,
			assert: func(t *testing.T, g SeriesCanon) {
				assert.Equal(t, 60, *g.RuntimeMinutes)
			},
		},
		{
			name:   "RuntimeMinutes TMDB overwrites existing canon",
			rule:   "RuntimeMinutes: TMDB > Sonarr",
			canon:  SeriesCanon{RuntimeMinutes: new(45)},
			patch:  SeriesPatch{RuntimeMinutes: new(60)},
			source: SourceTMDBSeries,
			assert: func(t *testing.T, g SeriesCanon) {
				assert.Equal(t, 60, *g.RuntimeMinutes)
			},
		},
		// --- Homepage: TMDB only ---
		{
			name:   "Homepage TMDB writes",
			rule:   "Homepage: TMDB only",
			canon:  SeriesCanon{},
			patch:  SeriesPatch{Homepage: new("https://x.com")},
			source: SourceTMDBSeries,
			assert: func(t *testing.T, g SeriesCanon) {
				assert.Equal(t, "https://x.com", *g.Homepage)
			},
		},
		{
			name:   "Homepage Sonarr no-op",
			rule:   "Homepage: Sonarr no authority",
			canon:  SeriesCanon{},
			patch:  SeriesPatch{Homepage: new("ignored")},
			source: SourceSonarr,
			assert: func(t *testing.T, g SeriesCanon) {
				assert.Nil(t, g.Homepage)
			},
		},
		// --- Popularity, InProduction: TMDB only ---
		{
			name:   "Popularity TMDB writes",
			rule:   "Popularity: TMDB only",
			canon:  SeriesCanon{},
			patch:  SeriesPatch{Popularity: new(42.5)},
			source: SourceTMDBSeries,
			assert: func(t *testing.T, g SeriesCanon) {
				assert.Equal(t, 42.5, *g.Popularity)
			},
		},
		{
			name:   "InProduction TMDB writes",
			rule:   "InProduction: TMDB only",
			canon:  SeriesCanon{},
			patch:  SeriesPatch{InProduction: new(true)},
			source: SourceTMDBSeries,
			assert: func(t *testing.T, g SeriesCanon) {
				assert.True(t, g.InProduction)
			},
		},
		// --- TMDBRating, TMDBVotes: TMDB only ---
		{
			name:   "TMDBRating TMDB writes",
			rule:   "TMDBRating: TMDB only",
			canon:  SeriesCanon{},
			patch:  SeriesPatch{TMDBRating: new(8.4)},
			source: SourceTMDBSeries,
			assert: func(t *testing.T, g SeriesCanon) {
				assert.Equal(t, 8.4, *g.TMDBRating)
			},
		},
		{
			name:   "TMDBVotes TMDB writes",
			rule:   "TMDBVotes: TMDB only",
			canon:  SeriesCanon{},
			patch:  SeriesPatch{TMDBVotes: new(1024)},
			source: SourceTMDBSeries,
			assert: func(t *testing.T, g SeriesCanon) {
				assert.Equal(t, 1024, *g.TMDBVotes)
			},
		},
		// --- IMDBRating, IMDBVotes, OMDBRated, OMDBAwards: OMDb only ---
		{
			name:   "IMDBRating OMDb writes",
			rule:   "IMDBRating: OMDb only",
			canon:  SeriesCanon{},
			patch:  SeriesPatch{IMDBRating: new(7.2)},
			source: SourceOMDb,
			assert: func(t *testing.T, g SeriesCanon) {
				assert.Equal(t, 7.2, *g.IMDBRating)
			},
		},
		{
			name:   "IMDBVotes OMDb writes",
			rule:   "IMDBVotes: OMDb only",
			canon:  SeriesCanon{},
			patch:  SeriesPatch{IMDBVotes: new(5000)},
			source: SourceOMDb,
			assert: func(t *testing.T, g SeriesCanon) {
				assert.Equal(t, 5000, *g.IMDBVotes)
			},
		},
		{
			name:   "OMDBRated OMDb writes",
			rule:   "OMDBRated: OMDb only",
			canon:  SeriesCanon{},
			patch:  SeriesPatch{OMDBRated: new("TV-MA")},
			source: SourceOMDb,
			assert: func(t *testing.T, g SeriesCanon) {
				assert.Equal(t, "TV-MA", *g.OMDBRated)
			},
		},
		{
			name:   "OMDBAwards OMDb writes",
			rule:   "OMDBAwards: OMDb only",
			canon:  SeriesCanon{},
			patch:  SeriesPatch{OMDBAwards: new("3 Emmys")},
			source: SourceOMDb,
			assert: func(t *testing.T, g SeriesCanon) {
				assert.Equal(t, "3 Emmys", *g.OMDBAwards)
			},
		},
		{
			name:   "IMDBRating TMDB no-op",
			rule:   "IMDBRating: TMDB no authority",
			canon:  SeriesCanon{},
			patch:  SeriesPatch{IMDBRating: new(9.9)},
			source: SourceTMDBSeries,
			assert: func(t *testing.T, g SeriesCanon) {
				assert.Nil(t, g.IMDBRating)
			},
		},
		// --- PosterAsset, BackdropAsset: TMDB > Sonarr ---
		{
			name:   "PosterAsset TMDB overwrites",
			rule:   "PosterAsset: TMDB > Sonarr",
			canon:  SeriesCanon{PosterAsset: new("sonarr-hash")},
			patch:  SeriesPatch{PosterAsset: new("tmdb-hash")},
			source: SourceTMDBSeries,
			assert: func(t *testing.T, g SeriesCanon) {
				assert.Equal(t, "tmdb-hash", *g.PosterAsset)
			},
		},
		{
			name:   "BackdropAsset TMDB overwrites",
			rule:   "BackdropAsset: TMDB > Sonarr",
			canon:  SeriesCanon{BackdropAsset: new("sonarr-hash")},
			patch:  SeriesPatch{BackdropAsset: new("tmdb-hash")},
			source: SourceTMDBSeries,
			assert: func(t *testing.T, g SeriesCanon) {
				assert.Equal(t, "tmdb-hash", *g.BackdropAsset)
			},
		},
		{
			name:   "PosterAsset Sonarr fills empty",
			rule:   "PosterAsset: Sonarr fallback",
			canon:  SeriesCanon{},
			patch:  SeriesPatch{PosterAsset: new("sonarr-hash")},
			source: SourceSonarr,
			assert: func(t *testing.T, g SeriesCanon) {
				assert.Equal(t, "sonarr-hash", *g.PosterAsset)
			},
		},
		// --- TMDBID: Sonarr > TMDB ---
		{
			name:   "TMDBID Sonarr overwrites",
			rule:   "TMDBID: Sonarr > TMDB (Sonarr already carries tmdbId)",
			canon:  SeriesCanon{TMDBID: new(111)},
			patch:  SeriesPatch{TMDBID: new(222)},
			source: SourceSonarr,
			assert: func(t *testing.T, g SeriesCanon) {
				assert.Equal(t, 222, *g.TMDBID)
			},
		},
		{
			name:   "TMDBID TMDB fills empty",
			rule:   "TMDBID: TMDB fallback (/find by tvdb)",
			canon:  SeriesCanon{},
			patch:  SeriesPatch{TMDBID: new(333)},
			source: SourceTMDBSeries,
			assert: func(t *testing.T, g SeriesCanon) {
				assert.Equal(t, 333, *g.TMDBID)
			},
		},
		// --- TVDBID: Sonarr only ---
		{
			name:   "TVDBID Sonarr writes",
			rule:   "TVDBID: Sonarr only",
			canon:  SeriesCanon{},
			patch:  SeriesPatch{TVDBID: new(99999)},
			source: SourceSonarr,
			assert: func(t *testing.T, g SeriesCanon) {
				assert.Equal(t, 99999, *g.TVDBID)
			},
		},
		// --- IMDBID: Sonarr > TMDB ---
		{
			name:   "IMDBID Sonarr overwrites",
			rule:   "IMDBID: Sonarr > TMDB",
			canon:  SeriesCanon{IMDBID: new("tt000")},
			patch:  SeriesPatch{IMDBID: new("tt111")},
			source: SourceSonarr,
			assert: func(t *testing.T, g SeriesCanon) {
				assert.Equal(t, "tt111", *g.IMDBID)
			},
		},
		{
			name:   "IMDBID TMDB fills empty",
			rule:   "IMDBID: TMDB fallback",
			canon:  SeriesCanon{},
			patch:  SeriesPatch{IMDBID: new("tt222")},
			source: SourceTMDBSeries,
			assert: func(t *testing.T, g SeriesCanon) {
				assert.Equal(t, "tt222", *g.IMDBID)
			},
		},
		// --- Hydration lift on TMDB write ---
		{
			name:   "TMDB write lifts stub -> full",
			rule:   "Hydration: stub->full on TMDB write",
			canon:  SeriesCanon{Hydration: LevelStub, Title: "stub"},
			patch:  SeriesPatch{OriginalTitle: new("orig")},
			source: SourceTMDBSeries,
			assert: func(t *testing.T, g SeriesCanon) {
				assert.Equal(t, LevelFull, g.Hydration)
			},
		},
		{
			name:   "Sonarr write does NOT lift hydration",
			rule:   "Hydration: Sonarr no authority",
			canon:  SeriesCanon{Hydration: LevelStub},
			patch:  SeriesPatch{Title: new("stub")},
			source: SourceSonarr,
			assert: func(t *testing.T, g SeriesCanon) {
				assert.Equal(t, LevelStub, g.Hydration)
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := MergeSeries(tc.canon, tc.patch, tc.source)
			tc.assert(t, got)
		})
	}
}

func TestMergeSeason(t *testing.T) {
	t.Parallel()
	day := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	cases := []struct {
		name   string
		canon  SeasonCanon
		patch  SeasonPatch
		source Source
		assert func(t *testing.T, g SeasonCanon)
	}{
		{
			name:   "Name TMDB writes",
			canon:  SeasonCanon{},
			patch:  SeasonPatch{Name: new("Season 1")},
			source: SourceTMDBSeason,
			assert: func(t *testing.T, g SeasonCanon) {
				assert.Equal(t, "Season 1", *g.Name)
			},
		},
		{
			name:   "Overview TMDB writes",
			canon:  SeasonCanon{},
			patch:  SeasonPatch{Overview: new("Premier season")},
			source: SourceTMDBSeason,
			assert: func(t *testing.T, g SeasonCanon) {
				assert.Equal(t, "Premier season", *g.Overview)
			},
		},
		{
			name:   "AirDate TMDB writes",
			canon:  SeasonCanon{},
			patch:  SeasonPatch{AirDate: new(day)},
			source: SourceTMDBSeason,
			assert: func(t *testing.T, g SeasonCanon) {
				assert.Equal(t, day, *g.AirDate)
			},
		},
		{
			name:   "PosterAsset TMDB writes",
			canon:  SeasonCanon{},
			patch:  SeasonPatch{PosterAsset: new("hash")},
			source: SourceTMDBSeason,
			assert: func(t *testing.T, g SeasonCanon) {
				assert.Equal(t, "hash", *g.PosterAsset)
			},
		},
		{
			name:   "EpisodeCount Sonarr overwrites TMDB",
			canon:  SeasonCanon{EpisodeCount: new(10)},
			patch:  SeasonPatch{EpisodeCount: new(12)},
			source: SourceSonarr,
			assert: func(t *testing.T, g SeasonCanon) {
				assert.Equal(t, 12, *g.EpisodeCount)
			},
		},
		{
			name:   "EpisodeCount TMDB fills empty",
			canon:  SeasonCanon{},
			patch:  SeasonPatch{EpisodeCount: new(10)},
			source: SourceTMDBSeason,
			assert: func(t *testing.T, g SeasonCanon) {
				assert.Equal(t, 10, *g.EpisodeCount)
			},
		},
		{
			name:   "EpisodeCount TMDB does NOT overwrite Sonarr",
			canon:  SeasonCanon{EpisodeCount: new(12)},
			patch:  SeasonPatch{EpisodeCount: new(10)},
			source: SourceTMDBSeason,
			assert: func(t *testing.T, g SeasonCanon) {
				assert.Equal(t, 12, *g.EpisodeCount)
			},
		},
		{
			name:   "Name Sonarr no-op",
			canon:  SeasonCanon{},
			patch:  SeasonPatch{Name: new("ignored")},
			source: SourceSonarr,
			assert: func(t *testing.T, g SeasonCanon) {
				assert.Nil(t, g.Name)
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := MergeSeason(tc.canon, tc.patch, tc.source)
			tc.assert(t, got)
		})
	}
}

func TestMergeEpisode(t *testing.T) {
	t.Parallel()
	d1 := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	d2 := time.Date(2024, 2, 1, 0, 0, 0, 0, time.UTC)
	cases := []struct {
		name   string
		canon  EpisodeCanon
		patch  EpisodePatch
		source Source
		assert func(t *testing.T, g EpisodeCanon)
	}{
		{
			name:   "AirDate Sonarr overwrites TMDB",
			canon:  EpisodeCanon{AirDate: new(d1)},
			patch:  EpisodePatch{AirDate: new(d2)},
			source: SourceSonarr,
			assert: func(t *testing.T, g EpisodeCanon) {
				assert.Equal(t, d2, *g.AirDate)
			},
		},
		{
			name:   "AirDate TMDB fills empty",
			canon:  EpisodeCanon{},
			patch:  EpisodePatch{AirDate: new(d1)},
			source: SourceTMDBSeason,
			assert: func(t *testing.T, g EpisodeCanon) {
				assert.Equal(t, d1, *g.AirDate)
			},
		},
		{
			name:   "RuntimeMinutes Sonarr overwrites",
			canon:  EpisodeCanon{RuntimeMinutes: new(40)},
			patch:  EpisodePatch{RuntimeMinutes: new(45)},
			source: SourceSonarr,
			assert: func(t *testing.T, g EpisodeCanon) {
				assert.Equal(t, 45, *g.RuntimeMinutes)
			},
		},
		{
			name:   "FinaleType Sonarr overwrites",
			canon:  EpisodeCanon{FinaleType: new("season")},
			patch:  EpisodePatch{FinaleType: new("series")},
			source: SourceSonarr,
			assert: func(t *testing.T, g EpisodeCanon) {
				assert.Equal(t, "series", *g.FinaleType)
			},
		},
		{
			name:   "StillAsset TMDB writes",
			canon:  EpisodeCanon{},
			patch:  EpisodePatch{StillAsset: new("still-hash")},
			source: SourceTMDBSeason,
			assert: func(t *testing.T, g EpisodeCanon) {
				assert.Equal(t, "still-hash", *g.StillAsset)
			},
		},
		{
			name:   "TMDBRating TMDB writes",
			canon:  EpisodeCanon{},
			patch:  EpisodePatch{TMDBRating: new(8.0)},
			source: SourceTMDBSeason,
			assert: func(t *testing.T, g EpisodeCanon) {
				assert.Equal(t, 8.0, *g.TMDBRating)
			},
		},
		{
			name:   "TMDBVotes TMDB writes",
			canon:  EpisodeCanon{},
			patch:  EpisodePatch{TMDBVotes: new(500)},
			source: SourceTMDBSeason,
			assert: func(t *testing.T, g EpisodeCanon) {
				assert.Equal(t, 500, *g.TMDBVotes)
			},
		},
		{
			name:   "TMDBEpisodeID TMDB writes",
			canon:  EpisodeCanon{},
			patch:  EpisodePatch{TMDBEpisodeID: new(12345)},
			source: SourceTMDBSeason,
			assert: func(t *testing.T, g EpisodeCanon) {
				assert.Equal(t, 12345, *g.TMDBEpisodeID)
			},
		},
		{
			name:   "SonarrEpisodeID Sonarr writes",
			canon:  EpisodeCanon{},
			patch:  EpisodePatch{SonarrEpisodeID: new(67890)},
			source: SourceSonarr,
			assert: func(t *testing.T, g EpisodeCanon) {
				assert.Equal(t, 67890, *g.SonarrEpisodeID)
			},
		},
		{
			name:   "StillAsset Sonarr no-op",
			canon:  EpisodeCanon{},
			patch:  EpisodePatch{StillAsset: new("ignored")},
			source: SourceSonarr,
			assert: func(t *testing.T, g EpisodeCanon) {
				assert.Nil(t, g.StillAsset)
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := MergeEpisode(tc.canon, tc.patch, tc.source)
			tc.assert(t, got)
		})
	}
}

func TestMergePerson(t *testing.T) {
	t.Parallel()
	d := time.Date(1970, 1, 1, 0, 0, 0, 0, time.UTC)
	cases := []struct {
		name   string
		canon  PersonCanon
		patch  PersonPatch
		source Source
		assert func(t *testing.T, g PersonCanon)
	}{
		{
			name:   "Name TMDB writes",
			canon:  PersonCanon{},
			patch:  PersonPatch{Name: new("Jane Doe")},
			source: SourceTMDBPerson,
			assert: func(t *testing.T, g PersonCanon) {
				assert.Equal(t, "Jane Doe", g.Name)
			},
		},
		{
			name:   "OriginalName TMDB writes",
			canon:  PersonCanon{},
			patch:  PersonPatch{OriginalName: new("ジェーン")},
			source: SourceTMDBPerson,
			assert: func(t *testing.T, g PersonCanon) {
				assert.Equal(t, "ジェーン", *g.OriginalName)
			},
		},
		{
			name:   "Gender TMDB writes",
			canon:  PersonCanon{},
			patch:  PersonPatch{Gender: new(2)},
			source: SourceTMDBPerson,
			assert: func(t *testing.T, g PersonCanon) {
				assert.Equal(t, 2, *g.Gender)
			},
		},
		{
			name:   "Birthday TMDB writes",
			canon:  PersonCanon{},
			patch:  PersonPatch{Birthday: new(d)},
			source: SourceTMDBPerson,
			assert: func(t *testing.T, g PersonCanon) {
				assert.Equal(t, d, *g.Birthday)
			},
		},
		{
			name:   "Deathday TMDB writes",
			canon:  PersonCanon{},
			patch:  PersonPatch{Deathday: new(d)},
			source: SourceTMDBPerson,
			assert: func(t *testing.T, g PersonCanon) {
				assert.Equal(t, d, *g.Deathday)
			},
		},
		{
			name:   "PlaceOfBirth TMDB writes",
			canon:  PersonCanon{},
			patch:  PersonPatch{PlaceOfBirth: new("Tokyo, Japan")},
			source: SourceTMDBPerson,
			assert: func(t *testing.T, g PersonCanon) {
				assert.Equal(t, "Tokyo, Japan", *g.PlaceOfBirth)
			},
		},
		{
			name:   "KnownForDepartment TMDB writes",
			canon:  PersonCanon{},
			patch:  PersonPatch{KnownForDepartment: new("Acting")},
			source: SourceTMDBPerson,
			assert: func(t *testing.T, g PersonCanon) {
				assert.Equal(t, "Acting", *g.KnownForDepartment)
			},
		},
		{
			name:   "Popularity TMDB writes",
			canon:  PersonCanon{},
			patch:  PersonPatch{Popularity: new(15.5)},
			source: SourceTMDBPerson,
			assert: func(t *testing.T, g PersonCanon) {
				assert.Equal(t, 15.5, *g.Popularity)
			},
		},
		{
			name:   "ProfileAsset TMDB writes",
			canon:  PersonCanon{},
			patch:  PersonPatch{ProfileAsset: new("profile-hash")},
			source: SourceTMDBPerson,
			assert: func(t *testing.T, g PersonCanon) {
				assert.Equal(t, "profile-hash", *g.ProfileAsset)
			},
		},
		{
			name:   "TMDBID TMDB writes",
			canon:  PersonCanon{},
			patch:  PersonPatch{TMDBID: new(123)},
			source: SourceTMDBPerson,
			assert: func(t *testing.T, g PersonCanon) {
				assert.Equal(t, 123, *g.TMDBID)
			},
		},
		{
			name:   "IMDBID TMDB writes",
			canon:  PersonCanon{},
			patch:  PersonPatch{IMDBID: new("nm0000123")},
			source: SourceTMDBPerson,
			assert: func(t *testing.T, g PersonCanon) {
				assert.Equal(t, "nm0000123", *g.IMDBID)
			},
		},
		{
			name:   "TMDB write lifts stub -> full",
			canon:  PersonCanon{Hydration: LevelStub},
			patch:  PersonPatch{Name: new("Stub Person")},
			source: SourceTMDBPerson,
			assert: func(t *testing.T, g PersonCanon) {
				assert.Equal(t, LevelFull, g.Hydration)
			},
		},
		{
			name:   "Sonarr source no-op",
			canon:  PersonCanon{Hydration: LevelStub, Name: "Original"},
			patch:  PersonPatch{Name: new("Rewritten")},
			source: SourceSonarr,
			assert: func(t *testing.T, g PersonCanon) {
				assert.Equal(t, "Original", g.Name)
				assert.Equal(t, LevelStub, g.Hydration)
			},
		},
		{
			name:   "OMDb source no-op",
			canon:  PersonCanon{Name: "Original"},
			patch:  PersonPatch{Name: new("Rewritten")},
			source: SourceOMDb,
			assert: func(t *testing.T, g PersonCanon) {
				assert.Equal(t, "Original", g.Name)
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := MergePerson(tc.canon, tc.patch, tc.source)
			tc.assert(t, got)
		})
	}
}
