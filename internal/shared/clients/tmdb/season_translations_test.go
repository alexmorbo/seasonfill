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
	assert.Contains(t, seenQuery, "append_to_response=translations",
		"S-C: season request MUST ask for translations; got %q", seenQuery)
	assert.NotContains(t, seenQuery, "images",
		"S-C must NOT request images (that is S-C2)")

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
