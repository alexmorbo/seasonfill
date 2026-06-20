package handlers

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/application/seriesdetail"
	"github.com/alexmorbo/seasonfill/infrastructure/database"
	"github.com/alexmorbo/seasonfill/interface/http/dto"
	"github.com/alexmorbo/seasonfill/internal/catalog/domain/series"
	"github.com/alexmorbo/seasonfill/internal/enrichment/domain/taxonomy"
)

// --- mapHero — branch coverage ---

func TestMapHero_MinimalCanon(t *testing.T) {
	t.Parallel()
	d := &seriesdetail.Detail{
		Canon: series.Canon{Title: "Bare"},
	}
	h := mapHero(d)
	assert.Equal(t, "Bare", h.Title)
	assert.Nil(t, h.YearStart)
	assert.Nil(t, h.YearEnd)
	assert.Nil(t, h.TMDBRating)
	assert.Nil(t, h.IMDbRating)
	assert.Nil(t, h.ContentRating)
	assert.Nil(t, h.Trailer)
	assert.Nil(t, h.NextEpisode)
	assert.Empty(t, h.Genres)
	assert.Empty(t, h.Networks)
}

func TestMapHero_LocalisedTitleOverridesCanon(t *testing.T) {
	t.Parallel()
	titleLocal := "Локализованный"
	tagline := "тагnine"
	d := &seriesdetail.Detail{
		Canon: series.Canon{Title: "Canon Title"},
		Text:  &series.SeriesText{Title: &titleLocal, Language: "ru-RU", Tagline: &tagline},
	}
	h := mapHero(d)
	assert.Equal(t, "Локализованный", h.Title)
	assert.Equal(t, "ru-RU", h.TitleLanguage)
	require.NotNil(t, h.Tagline)
	assert.Equal(t, "тагnine", *h.Tagline)
}

func TestMapHero_EmptyLocalisedTitleFallsBack(t *testing.T) {
	t.Parallel()
	empty := ""
	d := &seriesdetail.Detail{
		Canon: series.Canon{Title: "Canon Title"},
		Text:  &series.SeriesText{Title: &empty, Language: "ru-RU"},
	}
	h := mapHero(d)
	assert.Equal(t, "Canon Title", h.Title, "empty localised title shouldn't override")
}

func TestMapHero_YearStartAndEnd(t *testing.T) {
	t.Parallel()
	year := 2008
	lastAir := time.Date(2013, 9, 29, 0, 0, 0, 0, time.UTC)
	d := &seriesdetail.Detail{
		Canon: series.Canon{
			Title:       "Breaking Bad",
			Year:        &year,
			LastAirDate: &lastAir,
		},
	}
	h := mapHero(d)
	require.NotNil(t, h.YearStart)
	assert.Equal(t, 2008, *h.YearStart)
	require.NotNil(t, h.YearEnd)
	assert.Equal(t, 2013, *h.YearEnd)
}

func TestMapHero_TMDBRatingWithVotes(t *testing.T) {
	t.Parallel()
	rating := 9.5
	votes := 1234
	d := &seriesdetail.Detail{
		Canon: series.Canon{
			Title:      "X",
			TMDBRating: &rating,
			TMDBVotes:  &votes,
		},
	}
	h := mapHero(d)
	require.NotNil(t, h.TMDBRating)
	assert.InDelta(t, 9.5, h.TMDBRating.Score, 0.001)
	assert.Equal(t, 1234, h.TMDBRating.Votes)
}

func TestMapHero_TMDBRatingWithoutVotesDefaultsZero(t *testing.T) {
	t.Parallel()
	rating := 7.0
	d := &seriesdetail.Detail{
		Canon: series.Canon{Title: "X", TMDBRating: &rating},
	}
	h := mapHero(d)
	require.NotNil(t, h.TMDBRating)
	assert.Equal(t, 0, h.TMDBRating.Votes)
}

func TestMapHero_IMDBRatingWithVotes(t *testing.T) {
	t.Parallel()
	rating := 8.5
	votes := 5555
	d := &seriesdetail.Detail{
		Canon: series.Canon{
			Title:      "X",
			IMDBRating: &rating,
			IMDBVotes:  &votes,
		},
	}
	h := mapHero(d)
	require.NotNil(t, h.IMDbRating)
	assert.InDelta(t, 8.5, h.IMDbRating.Score, 0.001)
	assert.Equal(t, 5555, h.IMDbRating.Votes)
}

func TestMapHero_PopulatesGenresAndNetworks(t *testing.T) {
	t.Parallel()
	logoURL := "https://example.com/amc.png"
	d := &seriesdetail.Detail{
		Canon: series.Canon{Title: "X"},
		Genres: []taxonomy.Genre{
			{ID: 1, Name: "Drama", Language: "en-US"},
			{ID: 2, Name: "Crime", Language: "en-US"},
		},
		Networks: []taxonomy.Network{
			{ID: 10, Name: "AMC", LogoAsset: &logoURL},
		},
	}
	h := mapHero(d)
	require.Len(t, h.Genres, 2)
	assert.Equal(t, "Drama", h.Genres[0].Name)
	require.Len(t, h.Networks, 1)
	assert.Equal(t, "AMC", h.Networks[0].Name)
	require.NotNil(t, h.Networks[0].LogoAsset)
	assert.Equal(t, logoURL, *h.Networks[0].LogoAsset)
}

func TestMapHero_ContentRatingBadge(t *testing.T) {
	t.Parallel()
	d := &seriesdetail.Detail{
		Canon: series.Canon{Title: "X"},
		ContentRating: &database.ContentRatingModel{
			CountryCode: "US",
			Rating:      "TV-MA",
		},
	}
	h := mapHero(d)
	require.NotNil(t, h.ContentRating)
	assert.Equal(t, "US", h.ContentRating.CountryCode)
	assert.Equal(t, "TV-MA", h.ContentRating.Rating)
}

func TestMapHero_TrailerWithSiteAndKey(t *testing.T) {
	t.Parallel()
	site := "YouTube"
	key := "abc123"
	pub := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	d := &seriesdetail.Detail{
		Canon: series.Canon{Title: "X"},
		Trailer: &database.VideoModel{
			Site:        &site,
			Key:         &key,
			Name:        "Official Trailer",
			PublishedAt: &pub,
		},
	}
	h := mapHero(d)
	require.NotNil(t, h.Trailer)
	assert.Equal(t, "YouTube", h.Trailer.Site)
	assert.Equal(t, "abc123", h.Trailer.Key)
	assert.Equal(t, "Official Trailer", h.Trailer.Name)
}

func TestMapHero_TrailerWithNilSiteAndKey(t *testing.T) {
	t.Parallel()
	d := &seriesdetail.Detail{
		Canon: series.Canon{Title: "X"},
		Trailer: &database.VideoModel{
			Name: "Untitled",
		},
	}
	h := mapHero(d)
	require.NotNil(t, h.Trailer)
	assert.Equal(t, "", h.Trailer.Site)
	assert.Equal(t, "", h.Trailer.Key)
}

func TestMapHero_NextAirDateSetsNextEpisode(t *testing.T) {
	t.Parallel()
	nextAir := time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC)
	d := &seriesdetail.Detail{
		Canon: series.Canon{Title: "X", NextAirDate: &nextAir},
	}
	h := mapHero(d)
	require.NotNil(t, h.NextEpisode)
	require.NotNil(t, h.NextEpisode.AirDate)
	assert.Equal(t, nextAir, *h.NextEpisode.AirDate)
}

func TestMapHero_OriginalTitleAndAssets(t *testing.T) {
	t.Parallel()
	orig := "Better Call Saul"
	poster := "/p/x.jpg"
	backdrop := "/b/x.jpg"
	runtime := 47
	d := &seriesdetail.Detail{
		Canon: series.Canon{
			Title:          "Better Call Saul",
			OriginalTitle:  &orig,
			PosterAsset:    &poster,
			BackdropAsset:  &backdrop,
			RuntimeMinutes: &runtime,
		},
	}
	h := mapHero(d)
	require.NotNil(t, h.OriginalTitle)
	assert.Equal(t, "Better Call Saul", *h.OriginalTitle)
	require.NotNil(t, h.PosterAsset)
	assert.Equal(t, "/p/x.jpg", *h.PosterAsset)
	require.NotNil(t, h.BackdropAsset)
	assert.Equal(t, "/b/x.jpg", *h.BackdropAsset)
	require.NotNil(t, h.RuntimeMinutes)
	assert.Equal(t, 47, *h.RuntimeMinutes)
}

// --- story 373: NextEpisode prefers composer pick over canon ---

func TestMapHero_ComposerNextEpisodePreferredOverCanon(t *testing.T) {
	t.Parallel()
	canonNext := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
	composerNext := time.Date(2026, 6, 29, 0, 0, 0, 0, time.UTC)
	title := "Jer Bud"
	d := &seriesdetail.Detail{
		Canon: series.Canon{Title: "Rick and Morty", NextAirDate: &canonNext},
		NextEpisode: &seriesdetail.NextEpisodeDetail{
			SeasonNumber:  9,
			EpisodeNumber: 5,
			Title:         &title,
			AirDate:       &composerNext,
		},
	}
	h := mapHero(d)
	require.NotNil(t, h.NextEpisode)
	require.Equal(t, 9, h.NextEpisode.SeasonNumber)
	require.Equal(t, 5, h.NextEpisode.EpisodeNumber)
	require.NotNil(t, h.NextEpisode.Title)
	assert.Equal(t, "Jer Bud", *h.NextEpisode.Title)
	require.NotNil(t, h.NextEpisode.AirDate)
	assert.Equal(t, composerNext, *h.NextEpisode.AirDate)
}

func TestMapHero_CanonFallbackWhenComposerNil(t *testing.T) {
	t.Parallel()
	canonNext := time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC)
	d := &seriesdetail.Detail{
		Canon: series.Canon{Title: "X", NextAirDate: &canonNext},
	}
	h := mapHero(d)
	require.NotNil(t, h.NextEpisode)
	require.Nil(t, h.NextEpisode.Title)
	require.NotNil(t, h.NextEpisode.AirDate)
	assert.Equal(t, canonNext, *h.NextEpisode.AirDate)
	assert.Equal(t, 0, h.NextEpisode.SeasonNumber)
	assert.Equal(t, 0, h.NextEpisode.EpisodeNumber)
}

func TestMapHero_NoNextEpisodeWhenBothNil(t *testing.T) {
	t.Parallel()
	d := &seriesdetail.Detail{
		Canon: series.Canon{Title: "X"},
	}
	h := mapHero(d)
	assert.Nil(t, h.NextEpisode)
}

// --- Story 379: in-progress projection + per-season downloading_count ---

// TestMapLibrary_InProgress_FromComposer — when the composer fills
// d.InProgress, mapLibrary must surface the same fields onto
// LibraryStrip.in_progress (Title, percent, season, episode).
func TestMapLibrary_InProgress_FromComposer(t *testing.T) {
	t.Parallel()
	title := "A Rickconvenient Mort"
	d := &seriesdetail.Detail{
		Canon: series.Canon{Title: "Rick and Morty"},
		InProgress: &seriesdetail.InProgressDetail{
			SeasonNumber:  5,
			EpisodeNumber: 3,
			Title:         &title,
			Percent:       45,
		},
	}
	lib := mapLibrary(d)
	require.NotNil(t, lib.InProgress)
	assert.Equal(t, 5, lib.InProgress.SeasonNumber)
	assert.Equal(t, 3, lib.InProgress.EpisodeNumber)
	require.NotNil(t, lib.InProgress.Title)
	assert.Equal(t, "A Rickconvenient Mort", *lib.InProgress.Title)
	assert.Equal(t, 45, lib.InProgress.Percent)
}

// TestMapLibrary_InProgress_NilWhenComposerEmpty — composer nil → DTO nil.
func TestMapLibrary_InProgress_NilWhenComposerEmpty(t *testing.T) {
	t.Parallel()
	d := &seriesdetail.Detail{Canon: series.Canon{Title: "X"}}
	lib := mapLibrary(d)
	assert.Nil(t, lib.InProgress)
}

// TestMapSeasons_DownloadingCount — every downloading queue record whose
// season_number matches a SeasonDetail must bump that season's
// downloading_count. Queued / completed records must NOT count.
func TestMapSeasons_DownloadingCount(t *testing.T) {
	t.Parallel()
	d := &seriesdetail.Detail{
		Seasons: []seriesdetail.SeasonDetail{
			{Canon: series.CanonSeason{SeasonNumber: 1}},
			{Canon: series.CanonSeason{SeasonNumber: 5}},
		},
		QueueRecords: []seriesdetail.QueueRecordDetail{
			{SeasonNumber: 5, EpisodeNumber: 3, Status: "downloading"},
			{SeasonNumber: 5, EpisodeNumber: 4, Status: "downloading"},
			{SeasonNumber: 5, EpisodeNumber: 5, Status: "queued"}, // not downloading
			{SeasonNumber: 1, EpisodeNumber: 1, Status: "downloading"},
		},
	}
	out := mapSeasons(d)
	require.Len(t, out, 2)
	// out[0] is season 1.
	assert.Equal(t, 1, out[0].SeasonNumber)
	assert.Equal(t, 1, out[0].DownloadingCount)
	// out[1] is season 5.
	assert.Equal(t, 5, out[1].SeasonNumber)
	assert.Equal(t, 2, out[1].DownloadingCount, "queued record must not count")
}

// TestMapHero_NullableFieldsJSONProjection_OmitemptyContract proves the
// JSON wire contract for the three nullable hero fields the live B-13
// regression list highlighted: premiere_date, original_language,
// countries. Different from the struct-equality tests — this catches
// wrong KEY-PRESENCE (someone dropping `,omitempty` from the dto tag).
// json.Marshal -> map[string]any asserts against the wire.
func TestMapHero_NullableFieldsJSONProjection_OmitemptyContract(t *testing.T) {
	t.Parallel()

	date := time.Date(2026, 5, 28, 0, 0, 0, 0, time.UTC)
	langEN := "en"
	langRU := "ru"
	langEmpty := ""

	cases := []struct {
		name              string
		firstAirDate      *time.Time
		originalLanguage  *string
		originCountries   []string
		expectPremiere    bool
		wantPremiereValue string
		expectLanguage    bool
		wantLanguageValue string
		expectCountries   bool
		wantCountriesLen  int
	}{
		{
			name:           "all_nil_all_keys_absent",
			expectPremiere: false, expectLanguage: false, expectCountries: false,
		},
		{
			name:            "empty_countries_slice_key_absent",
			originCountries: []string{},
			expectPremiere:  false, expectLanguage: false, expectCountries: false,
		},
		{
			name:            "countries_only_present",
			originCountries: []string{"US"},
			expectCountries: true, wantCountriesLen: 1,
		},
		{
			name:             "language_only_present",
			originalLanguage: &langEN,
			expectLanguage:   true, wantLanguageValue: "en",
		},
		{
			name:           "premiere_only_present",
			firstAirDate:   &date,
			expectPremiere: true, wantPremiereValue: "2026-05-28",
		},
		{
			name:             "all_three_present",
			firstAirDate:     &date,
			originalLanguage: &langEN,
			originCountries:  []string{"US", "CA"},
			expectPremiere:   true, wantPremiereValue: "2026-05-28",
			expectLanguage: true, wantLanguageValue: "en",
			expectCountries: true, wantCountriesLen: 2,
		},
		{
			name:             "empty_language_string_treated_as_absent",
			firstAirDate:     &date,
			originalLanguage: &langEmpty,
			originCountries:  []string{"US"},
			expectPremiere:   true, wantPremiereValue: "2026-05-28",
			expectLanguage:  false,
			expectCountries: true, wantCountriesLen: 1,
		},
		{
			name:             "ru_language_with_country",
			originalLanguage: &langRU,
			originCountries:  []string{"RU"},
			expectLanguage:   true, wantLanguageValue: "ru",
			expectCountries: true, wantCountriesLen: 1,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			d := &seriesdetail.Detail{
				Canon: series.Canon{
					Title:            "X",
					FirstAirDate:     tc.firstAirDate,
					OriginalLanguage: tc.originalLanguage,
					OriginCountries:  tc.originCountries,
				},
			}

			var hero dto.SeriesHero
			require.NotPanics(t, func() { hero = mapHero(d) },
				"REGRESSION: mapHero must not panic on any nullable combo")

			raw, err := json.Marshal(hero)
			require.NoError(t, err)
			var wire map[string]any
			require.NoError(t, json.Unmarshal(raw, &wire))

			_, hasPremiere := wire["premiere_date"]
			_, hasLanguage := wire["original_language"]
			_, hasCountries := wire["countries"]

			assert.Equal(t, tc.expectPremiere, hasPremiere,
				"premiere_date key presence — omitempty contract")
			assert.Equal(t, tc.expectLanguage, hasLanguage,
				"original_language key presence — omitempty contract")
			assert.Equal(t, tc.expectCountries, hasCountries,
				"countries key presence — omitempty contract")

			if tc.expectPremiere {
				assert.Equal(t, tc.wantPremiereValue, wire["premiere_date"])
			}
			if tc.expectLanguage {
				assert.Equal(t, tc.wantLanguageValue, wire["original_language"])
			}
			if tc.expectCountries {
				arr, ok := wire["countries"].([]any)
				require.True(t, ok, "countries should be a JSON array")
				assert.Len(t, arr, tc.wantCountriesLen)
			}
		})
	}
}
