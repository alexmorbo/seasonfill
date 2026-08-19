package enrichment

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/internal/shared/clients/tmdb"
)

// realMovieTranslationsPayload is a trimmed but structurally faithful
// GET /movie/438148?append_to_response=translations response. The localized
// title lives under translations.translations[*].data.title (the MOVIE shape),
// NOT data.name (the TV shape) — which is exactly the key that was silently
// dropped. Decoded via real json.Unmarshal so the json tag is under test.
const realMovieTranslationsPayload = `{
  "id": 438148,
  "title": "Dune",
  "original_title": "Dune",
  "overview": "A noble family becomes embroiled in a war for control over the galaxy's most valuable asset.",
  "tagline": "Beyond fear, destiny awaits.",
  "poster_path": "/en_poster.jpg",
  "backdrop_path": "/en_backdrop.jpg",
  "translations": {
    "translations": [
      {
        "iso_3166_1": "US",
        "iso_639_1": "en",
        "name": "English",
        "data": {
          "homepage": "",
          "overview": "A noble family becomes embroiled in a war for control over the galaxy's most valuable asset.",
          "runtime": 155,
          "tagline": "Beyond fear, destiny awaits.",
          "title": "Dune"
        }
      },
      {
        "iso_3166_1": "RU",
        "iso_639_1": "ru",
        "name": "Russian",
        "data": {
          "homepage": "",
          "overview": "Наследник знаменитого дома Атрейдес отправляется на опасную планету Арракис.",
          "runtime": 155,
          "tagline": "Выйди за пределы страха.",
          "title": "Дюна"
        }
      }
    ]
  }
}`

// TestMovieWorker_LocalizedTitle_FromRealJSON is the regression guard for U-1:
// the movie translation mapper must read data.title (movie shape), so the ru-RU
// i18n row gets the real Russian title — not an empty string. Exercises the FULL
// worker path against a json.Unmarshal-decoded MovieResponse (would fail under the
// old TVTranslationData{Name json:"name"} decode).
func TestMovieWorker_LocalizedTitle_FromRealJSON(t *testing.T) {
	var resp tmdb.MovieResponse
	require.NoError(t, json.Unmarshal([]byte(realMovieTranslationsPayload), &resp))

	// Guard the decode itself: the movie `title` key must populate the real
	// struct field (this alone would have caught the bug).
	require.NotNil(t, resp.Translations)
	require.Len(t, resp.Translations.Translations, 2)
	var ruTitle string
	for _, tr := range resp.Translations.Translations {
		if tr.ISO6391 == "ru" {
			ruTitle = tr.Data.Title
		}
	}
	require.Equal(t, "Дюна", ruTitle, "movie translation data.title must decode (not empty)")

	tmdbClient := &fakeMovieTMDB{resp: &resp}
	canon := &fakeMovieCanon{getResp: movieCanonWithTMDB(55, 438148)}
	i18n := &fakeMovieI18n{}
	w, err := NewMovieWorker(MovieWorkerDeps{
		TMDB:   tmdbClient,
		Movies: canon,
		I18n:   i18n,
		Clock:  func() time.Time { return time.Date(2026, 8, 19, 0, 0, 0, 0, time.UTC) },
	})
	require.NoError(t, err)

	require.NoError(t, w.HandleForced(context.Background(), 55))

	titlesByLang := make(map[string]string, len(i18n.writes))
	for _, wr := range i18n.writes {
		titlesByLang[wr.lang] = wr.title
	}

	// Base language row from the response root.
	assert.Equal(t, "Dune", titlesByLang["en-US"], "en-US base title from response root")
	// The bug: this was "" before the fix.
	assert.Equal(t, "Дюна", titlesByLang["ru-RU"], "ru-RU localized title must come from data.title, not be empty")
	assert.NotEmpty(t, titlesByLang["ru-RU"], "regression: localized movie title must never be empty")
}
