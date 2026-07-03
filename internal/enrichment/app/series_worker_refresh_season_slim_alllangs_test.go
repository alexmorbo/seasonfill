package enrichment

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/internal/catalog/domain/series"
	"github.com/alexmorbo/seasonfill/internal/shared/clients/tmdb"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
)

// seasonWithTranslations returns a season-8 payload whose ROOT is the call-lang
// (ru-RU here) and whose translations[] carry both en + ru season name/overview.
func seasonWithTranslations() *tmdb.SeasonResponse {
	return &tmdb.SeasonResponse{
		ID:           555,
		Name:         "Сезон 8",           // root = call lang (ru-RU)
		Overview:     "Русское описание.", // root = call lang
		AirDate:      "2026-01-01",
		SeasonNumber: 8,
		Episodes: []tmdb.SeasonEpisode{
			{ID: 1001, Name: "Эпизод 1", Overview: "о", SeasonNumber: 8, EpisodeNumber: 1, AirDate: "2026-01-01", EpisodeType: "standard"},
		},
		Translations: &tmdb.SeasonTranslations{
			Translations: []tmdb.SeasonTranslation{
				{ISO6391: "en", ISO31661: "US", Data: tmdb.SeasonTranslationData{Name: "Season 8", Overview: "English overview."}},
				{ISO6391: "ru", ISO31661: "RU", Data: tmdb.SeasonTranslationData{Name: "Сезон 8", Overview: "Русское описание."}},
			},
		},
	}
}

// One GetSeason(+translations) → BOTH en-US and ru-RU season_texts rows, single
// TMDB call. This is the S-C core: opening a season under ru-RU also lands the
// en-US row (#976 fix).
func TestSeasonSlim_AllLangs_WritesEnAndRuFromOnePayload(t *testing.T) {
	t.Parallel()
	tmdbID := domain.TMDBID(42)
	f := newSlimFixture(t, &tmdbID, nil)
	f.tmdb.seasons[8] = seasonWithTranslations()

	require.NoError(t, f.worker.RefreshSeasonSlim(context.Background(), 1, 8, "ru-RU", true))
	assert.Equal(t, 1, countCall(f.tmdb, "GetSeason"), "exactly ONE TMDB call for all langs")

	byLang := map[string]string{}   // language -> name
	ovByLang := map[string]string{} // language -> overview
	for _, r := range f.seasonTexts.rows {
		require.NotNil(t, r.Name)
		byLang[r.Language] = *r.Name
		if r.Overview != nil {
			ovByLang[r.Language] = *r.Overview
		}
	}
	require.Len(t, f.seasonTexts.rows, 2, "both supported langs written")
	assert.Equal(t, "Season 8", byLang["en-US"])
	assert.Equal(t, "English overview.", ovByLang["en-US"])
	assert.Equal(t, "Сезон 8", byLang["ru-RU"])
	assert.Equal(t, "Русское описание.", ovByLang["ru-RU"])
}

// Empty data.overview in a translation entry → per-FIELD fallback to root
// (which for the call lang is the call-lang overview).
func TestSeasonSlim_AllLangs_EmptyOverview_RootFallback(t *testing.T) {
	t.Parallel()
	tmdbID := domain.TMDBID(42)
	f := newSlimFixture(t, &tmdbID, nil)
	resp := seasonWithTranslations()
	// ru entry loses its overview → must fall back to root ("Русское описание.").
	resp.Translations.Translations[1].Data.Overview = ""
	f.tmdb.seasons[8] = resp

	require.NoError(t, f.worker.RefreshSeasonSlim(context.Background(), 1, 8, "ru-RU", true))
	var ruOverview *string
	for _, r := range f.seasonTexts.rows {
		if r.Language == "ru-RU" {
			ruOverview = r.Overview
		}
	}
	require.NotNil(t, ruOverview)
	assert.Equal(t, "Русское описание.", *ruOverview,
		"empty translation overview → root fallback (call-lang root)")
}

// NON-call-lang direction of the empty-field case (#973 "seasons RU-in-EN"
// aimed at the fallback tier): opening under ru-RU where the en entry carries a
// name but an EMPTY overview must NOT bleed the call-lang (Russian) root text
// into the en-US row. The en-US overview stays nil (COALESCE-preserving),
// while its name is the translated "Season 8". The ru-RU row is unaffected.
func TestSeasonSlim_AllLangs_NonCallLang_EmptyOverview_NoRootBleed(t *testing.T) {
	t.Parallel()
	tmdbID := domain.TMDBID(42)
	f := newSlimFixture(t, &tmdbID, nil)
	resp := seasonWithTranslations()
	// en entry keeps its name but loses its overview. Root is the CALL lang
	// (ru-RU) here, so a root fallback would poison en-US with Russian text.
	resp.Translations.Translations[0].Data.Overview = ""
	f.tmdb.seasons[8] = resp

	require.NoError(t, f.worker.RefreshSeasonSlim(context.Background(), 1, 8, "ru-RU", true))

	var enName, enOverview, ruOverview *string
	for _, r := range f.seasonTexts.rows {
		switch r.Language {
		case "en-US":
			enName, enOverview = r.Name, r.Overview
		case "ru-RU":
			ruOverview = r.Overview
		}
	}
	require.NotNil(t, enName)
	assert.Equal(t, "Season 8", *enName, "en name uses its own translation entry")
	assert.Nil(t, enOverview,
		"non-call lang empty overview must be nil (nonEmptyStringPtr) — NOT the call-lang root text; COALESCE preserves the prior value")
	require.NotNil(t, ruOverview)
	assert.Equal(t, "Русское описание.", *ruOverview, "call-lang row unaffected")
}

// REGRESSION (row-survival): a pre-existing season_texts row must SURVIVE an
// empty-root + empty-Translations refresh. The append-only fake cannot delete,
// so survival == the pre-seeded row still present AND zero new Upsert calls (the
// worker skips content-less writes, and the repo COALESCE would preserve on a
// real partial write).
func TestSeasonSlim_AllLangs_EmptyTranslations_PreExistingRowSurvives(t *testing.T) {
	t.Parallel()
	tmdbID := domain.TMDBID(42)
	f := newSlimFixture(t, &tmdbID, nil)
	// Pre-seed a good en-US row as if a prior refresh had populated it.
	preName := "Season 8"
	preOverview := "English overview."
	f.seasonTexts.rows = []series.SeasonText{{
		SeriesID:     1,
		SeasonNumber: 8,
		Language:     "en-US",
		Name:         &preName,
		Overview:     &preOverview,
	}}
	f.tmdb.seasons[8] = &tmdb.SeasonResponse{
		ID:           555,
		Name:         "",
		Overview:     "",
		SeasonNumber: 8,
		AirDate:      "2027-01-01",
		Translations: &tmdb.SeasonTranslations{Translations: []tmdb.SeasonTranslation{}},
	}

	require.NoError(t, f.worker.RefreshSeasonSlim(context.Background(), 1, 8, "ru-RU", true))

	assert.False(t, hasCall(f.rec.list(), "SeasonTexts.Upsert"),
		"no content-less write attempted, so a real repo's COALESCE preserves the prior row")
	require.Len(t, f.seasonTexts.rows, 1, "pre-existing row survives the empty refresh")
	row := f.seasonTexts.rows[0]
	assert.Equal(t, "en-US", row.Language)
	require.NotNil(t, row.Name)
	require.NotNil(t, row.Overview)
	assert.Equal(t, "Season 8", *row.Name)
	assert.Equal(t, "English overview.", *row.Overview)
}

// A supported lang with NO translation entry AND that is not the call lang is
// SKIPPED entirely (row stays absent so the per-lang probe stays Stale).
func TestSeasonSlim_AllLangs_MissingNonCallLang_Skipped(t *testing.T) {
	t.Parallel()
	tmdbID := domain.TMDBID(42)
	f := newSlimFixture(t, &tmdbID, nil)
	resp := seasonWithTranslations()
	// Drop the en entry; call lang is ru-RU → only ru-RU row should be written.
	resp.Translations.Translations = resp.Translations.Translations[1:] // keep ru only
	f.tmdb.seasons[8] = resp

	require.NoError(t, f.worker.RefreshSeasonSlim(context.Background(), 1, 8, "ru-RU", true))
	require.Len(t, f.seasonTexts.rows, 1)
	assert.Equal(t, "ru-RU", f.seasonTexts.rows[0].Language)
}

// REGRESSION (F-R2-3): a completely empty translations payload with an empty
// root under a non-content season → NO season_texts row is written at all, so
// the worker cannot wipe an existing row (repo COALESCE + worker skip-both).
func TestSeasonSlim_AllLangs_EmptyEverything_NoWrite(t *testing.T) {
	t.Parallel()
	tmdbID := domain.TMDBID(42)
	f := newSlimFixture(t, &tmdbID, nil)
	f.tmdb.seasons[8] = &tmdb.SeasonResponse{
		ID:           555,
		Name:         "",
		Overview:     "",
		SeasonNumber: 8,
		AirDate:      "2027-01-01",
		Translations: &tmdb.SeasonTranslations{Translations: []tmdb.SeasonTranslation{}},
	}
	require.NoError(t, f.worker.RefreshSeasonSlim(context.Background(), 1, 8, "ru-RU", true))
	assert.Empty(t, f.seasonTexts.rows, "empty root + empty translations → zero season_texts writes")
	assert.False(t, hasCall(f.rec.list(), "SeasonTexts.Upsert"))
	// stamp still fires (probe-storm guard) — episodes path unchanged.
	assert.True(t, hasCall(f.rec.list(), "Seasons.MarkSeasonEpisodesSynced"))
}
