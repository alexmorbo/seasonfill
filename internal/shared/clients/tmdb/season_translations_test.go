package tmdb

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// GetSeason must request append_to_response=translations (S-C) and decode the
// season-level translations[] array into typed SeasonTranslation rows.
func TestGetSeason_RequestsTranslations_Decodes(t *testing.T) {
	t.Parallel()

	var seenPath, seenQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenPath = r.URL.Path
		seenQuery = r.URL.RawQuery
		_, _ = io.WriteString(w, `{
			"id": 3572,
			"name": "Season 1",
			"overview": "English season overview.",
			"air_date": "2008-01-20",
			"season_number": 1,
			"poster_path": "/p.jpg",
			"episodes": [
				{"id": 62085, "name": "Pilot", "overview": "ep", "episode_number": 1, "season_number": 1}
			],
			"translations": {
				"translations": [
					{"iso_639_1": "en", "iso_3166_1": "US", "data": {"name": "Season 1", "overview": "English season overview."}},
					{"iso_639_1": "ru", "iso_3166_1": "RU", "data": {"name": "Сезон 1", "overview": "Русское описание сезона."}}
				]
			}
		}`)
	}))
	t.Cleanup(srv.Close)

	c := mustNew(t, srv.URL, "test-key")
	defer c.Close()

	got, err := c.GetSeason(context.Background(), 1396, 1, "en-US")
	require.NoError(t, err)

	assert.Equal(t, "/tv/1396/season/1", seenPath)
	assert.Contains(t, seenQuery, "append_to_response=translations%2Cimages",
		"S-C2: season request MUST ask for translations,images; got %q", seenQuery)
	assert.Contains(t, seenQuery, "include_image_language=en%2Cru%2Cnull",
		"S-C2: season request MUST carry the en,ru,null image-language union; got %q", seenQuery)

	require.NotNil(t, got.Translations)
	require.Len(t, got.Translations.Translations, 2)
	byLang := map[string]SeasonTranslation{}
	for _, tr := range got.Translations.Translations {
		byLang[tr.ISO6391] = tr
	}
	require.Contains(t, byLang, "ru")
	assert.Equal(t, "Сезон 1", byLang["ru"].Data.Name)
	assert.Equal(t, "Русское описание сезона.", byLang["ru"].Data.Overview)
	assert.Equal(t, "Season 1", byLang["en"].Data.Name)
}

// Story 1090 — GetSeason must add aggregate_credits to append_to_response and
// decode the per-season cast into SeasonResponse.AggregateCredits.Cast so the
// worker can compute per-person max(season_number) for the last_appearance sort.
func TestGetSeason_RequestsAggregateCredits_Decodes(t *testing.T) {
	t.Parallel()

	var seenQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenQuery = r.URL.RawQuery
		_, _ = io.WriteString(w, `{
			"id": 3572,
			"name": "Season 2",
			"season_number": 2,
			"episodes": [],
			"aggregate_credits": {
				"cast": [
					{"id": 100, "name": "Actor A", "order": 0, "total_episode_count": 12,
						"roles": [{"credit_id": "c1", "character": "Hero", "episode_count": 12}]},
					{"id": 200, "name": "Actor B", "order": 1, "total_episode_count": 3,
						"roles": [{"credit_id": "c2", "character": "Sidekick", "episode_count": 3}]}
				],
				"crew": []
			}
		}`)
	}))
	t.Cleanup(srv.Close)

	c := mustNew(t, srv.URL, "test-key")
	defer c.Close()

	got, err := c.GetSeason(context.Background(), 1396, 2, "en-US")
	require.NoError(t, err)

	assert.Contains(t, seenQuery, "aggregate_credits",
		"Story 1090: season request MUST ask for aggregate_credits; got %q", seenQuery)

	require.NotNil(t, got.AggregateCredits)
	require.Len(t, got.AggregateCredits.Cast, 2)
	assert.Equal(t, int64(100), got.AggregateCredits.Cast[0].ID)
	assert.Equal(t, 0, got.AggregateCredits.Cast[0].Order)
	assert.Equal(t, int64(200), got.AggregateCredits.Cast[1].ID)
	assert.Equal(t, 1, got.AggregateCredits.Cast[1].Order)
}

// A season payload without an aggregate_credits sub-resource decodes to a nil
// AggregateCredits pointer (nilable-object contract) — no panic downstream.
func TestGetSeason_NoAggregateCredits_NilPointer(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"id":1,"name":"S","season_number":1,"episodes":[]}`)
	}))
	t.Cleanup(srv.Close)
	c := mustNew(t, srv.URL, "test-key")
	defer c.Close()

	got, err := c.GetSeason(context.Background(), 1, 1, "en-US")
	require.NoError(t, err)
	assert.Nil(t, got.AggregateCredits)
}

// A season payload without a translations sub-resource decodes to a nil
// Translations pointer (nilable-array contract) — no panic downstream.
func TestGetSeason_NoTranslations_NilPointer(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"id":1,"name":"S","season_number":1,"episodes":[]}`)
	}))
	t.Cleanup(srv.Close)
	c := mustNew(t, srv.URL, "test-key")
	defer c.Close()

	got, err := c.GetSeason(context.Background(), 1, 1, "en-US")
	require.NoError(t, err)
	assert.Nil(t, got.Translations)
}
