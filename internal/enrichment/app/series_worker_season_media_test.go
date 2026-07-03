package enrichment

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/internal/shared/clients/tmdb"
)

func TestBuildSeasonMediaTextWrites(t *testing.T) {
	now := time.Date(2026, 7, 3, 0, 0, 0, 0, time.UTC)

	t.Run("empty_images_call_lang_uses_root_poster", func(t *testing.T) {
		resp := &tmdb.SeasonResponse{PosterPath: "/root.jpg"} // Images nil
		w := buildSeasonMediaTextWrites(context.Background(), nil, resp, 7, 1, "ru-RU", now)
		require.Len(t, w, 1, "only the call lang gets a row (root fallback)")
		assert.Equal(t, "ru-RU", w[0].Language)
		require.NotNil(t, w[0].PosterAsset)
		assert.Equal(t, "/root.jpg", *w[0].PosterAsset)
		assert.Nil(t, w[0].PosterHash, "nil resolver → nil hash")
		assert.Nil(t, w[0].BackdropAsset, "season images carry no backdrops")
	})

	t.Run("empty_images_no_root_skips_all", func(t *testing.T) {
		resp := &tmdb.SeasonResponse{} // no images, no root poster
		w := buildSeasonMediaTextWrites(context.Background(), nil, resp, 7, 1, "en-US", now)
		assert.Empty(t, w)
	})

	t.Run("per_lang_posters_differ", func(t *testing.T) {
		resp := &tmdb.SeasonResponse{
			PosterPath: "/root-en.jpg",
			Images: &tmdb.SeasonImages{Posters: []tmdb.TVImage{
				{FilePath: "/en.jpg", ISO6391: new("en"), VoteAverage: 5},
				{FilePath: "/ru.jpg", ISO6391: new("ru"), VoteAverage: 5},
			}},
		}
		// call lang = en-US.
		w := buildSeasonMediaTextWrites(context.Background(), nil, resp, 7, 1, "en-US", now)
		byLang := map[string]string{}
		for _, r := range w {
			byLang[r.Language] = *r.PosterAsset
		}
		require.Len(t, w, 2)
		assert.Equal(t, "/en.jpg", byLang["en-US"])
		assert.Equal(t, "/ru.jpg", byLang["ru-RU"], "ru row picks the ru-tagged poster (strict)")
	})

	t.Run("call_lang_poster_does_not_poison_non_call_lang", func(t *testing.T) {
		// call = ru-RU, ONLY a ru-tagged poster exists. The non-call en-US row
		// must NOT be written with that ru poster via a lax fallback — the
		// strict tier for non-call langs matches en-tagged posters ONLY, so
		// en-US is skipped (no poison of the universal en-US fallback tier).
		resp := &tmdb.SeasonResponse{
			PosterPath: "/root-ru.jpg",
			Images: &tmdb.SeasonImages{Posters: []tmdb.TVImage{
				{FilePath: "/ru.jpg", ISO6391: new("ru"), VoteAverage: 5},
			}},
		}
		w := buildSeasonMediaTextWrites(context.Background(), nil, resp, 7, 1, "ru-RU", now)
		require.Len(t, w, 1, "only ru-RU (call) row; en-US skipped — strict, no poison")
		assert.Equal(t, "ru-RU", w[0].Language)
		// ru call lang: full chain picks its own ru-tagged poster.
		require.NotNil(t, w[0].PosterAsset)
		assert.Equal(t, "/ru.jpg", *w[0].PosterAsset)
	})
}
