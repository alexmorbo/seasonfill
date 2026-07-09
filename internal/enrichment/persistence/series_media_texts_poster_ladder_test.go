package persistence

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/internal/catalog/domain/series"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
	"github.com/alexmorbo/seasonfill/internal/shared/testhelpers"
)

// TestSeriesMediaTextsRepository_PosterLadder is the Story 1110 poster-presence
// ladder suite. The batch ListByIDsWithFallback must key each tier on POSTER
// presence, not row presence, so a Story-1081b confirmed-absent row
// (poster_asset NULL + poster_checked_at SET) never shadows a lower-tier row
// that carries a real poster. Runs on BOTH backends (SQLite + testcontainers PG).
func TestSeriesMediaTextsRepository_PosterLadder(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()

			// seedN inserts n distinct series rows on ONE db and returns their
			// ids + a series_media_texts repo bound to that db.
			seedN := func(t *testing.T, n int) ([]domain.SeriesID, *SeriesMediaTextsRepository) {
				t.Helper()
				gdb := backend.NewDB(t)
				srepo := NewSeriesRepository(gdb)
				ids := make([]domain.SeriesID, n)
				for i := range n {
					c := sampleCanon(fmt.Sprintf("Poster Ladder Show %d", i))
					c.TMDBID = ptrTMDBID(11000 + i)
					c.TVDBID = ptrTVDBID(11100 + i)
					c.IMDBID = ptrIMDBID(fmt.Sprintf("tt110000%02d", i))
					id, err := srepo.Upsert(ctx, c)
					require.NoError(t, err)
					ids[i] = id
				}
				return ids, NewSeriesMediaTextsRepository(gdb)
			}

			checked := time.Date(2026, 7, 9, 0, 0, 0, 0, time.UTC)

			// THE bug — ru-RU confirmed-absent (poster NULL + poster_checked_at
			// SET) must NOT block the en-US poster. Request ru-RU → en-US poster.
			t.Run("confirmed_absent_ru_falls_through_to_en_poster", func(t *testing.T) {
				ids, repo := seedN(t, 1)
				require.NoError(t, repo.Upsert(ctx, series.SeriesMediaText{
					SeriesID:        ids[0],
					Language:        "ru-RU",
					PosterAsset:     nil, // confirmed-absent
					PosterCheckedAt: &checked,
				}))
				require.NoError(t, repo.Upsert(ctx, series.SeriesMediaText{
					SeriesID:    ids[0],
					Language:    "en-US",
					PosterAsset: new("/en-real.jpg"),
				}))

				out, err := repo.ListByIDsWithFallback(ctx, ids, "ru-RU")
				require.NoError(t, err)
				require.Len(t, out, 1)
				got := out[ids[0]]
				assert.Equal(t, "en-US", got.Language, "poster-presence ladder: en-US poster wins over ru-RU confirmed-absent")
				require.NotNil(t, got.PosterAsset)
				assert.Equal(t, "/en-real.jpg", *got.PosterAsset)
			})

			// No regression — ru-RU real poster present, request ru-RU → ru-RU.
			t.Run("ru_real_poster_is_not_downgraded", func(t *testing.T) {
				ids, repo := seedN(t, 1)
				require.NoError(t, repo.Upsert(ctx, series.SeriesMediaText{
					SeriesID: ids[0], Language: "ru-RU", PosterAsset: new("/ru-real.jpg"),
				}))
				require.NoError(t, repo.Upsert(ctx, series.SeriesMediaText{
					SeriesID: ids[0], Language: "en-US", PosterAsset: new("/en-real.jpg"),
				}))
				out, err := repo.ListByIDsWithFallback(ctx, ids, "ru-RU")
				require.NoError(t, err)
				require.Len(t, out, 1)
				assert.Equal(t, "ru-RU", out[ids[0]].Language)
				require.NotNil(t, out[ids[0]].PosterAsset)
				assert.Equal(t, "/ru-real.jpg", *out[ids[0]].PosterAsset)
			})

			// Any-lang tier — only a non-requested, non-en-US language carries a
			// real poster (de-DE). ru-RU + en-US are both confirmed-absent.
			t.Run("any_lang_supplies_poster_when_ru_and_en_absent", func(t *testing.T) {
				ids, repo := seedN(t, 1)
				require.NoError(t, repo.Upsert(ctx, series.SeriesMediaText{
					SeriesID: ids[0], Language: "ru-RU", PosterAsset: nil, PosterCheckedAt: &checked,
				}))
				require.NoError(t, repo.Upsert(ctx, series.SeriesMediaText{
					SeriesID: ids[0], Language: "en-US", PosterAsset: nil, PosterCheckedAt: &checked,
				}))
				require.NoError(t, repo.Upsert(ctx, series.SeriesMediaText{
					SeriesID: ids[0], Language: "de-DE", PosterAsset: new("/de-real.jpg"),
				}))
				out, err := repo.ListByIDsWithFallback(ctx, ids, "ru-RU")
				require.NoError(t, err)
				require.Len(t, out, 1)
				assert.Equal(t, "de-DE", out[ids[0]].Language, "any-lang tier: de-DE poster resolves past two confirmed-absent tiers")
				require.NotNil(t, out[ids[0]].PosterAsset)
				assert.Equal(t, "/de-real.jpg", *out[ids[0]].PosterAsset)
			})

			// No poster in ANY language → the id is PRESENT (primary seeds the
			// row) with an empty poster; the caller renders the sentinel.
			t.Run("no_poster_anywhere_id_present_with_empty_poster", func(t *testing.T) {
				ids, repo := seedN(t, 1)
				require.NoError(t, repo.Upsert(ctx, series.SeriesMediaText{
					SeriesID: ids[0], Language: "ru-RU", PosterAsset: nil, PosterCheckedAt: &checked,
				}))
				out, err := repo.ListByIDsWithFallback(ctx, ids, "ru-RU")
				require.NoError(t, err)
				got, ok := out[ids[0]]
				require.True(t, ok, "the id stays PRESENT (row seeded) so the caller can read a backdrop / render sentinel")
				assert.True(t, got.PosterAsset == nil || *got.PosterAsset == "", "poster stays empty — caller renders the sentinel legitimately")
			})

			// Backdrop preservation guard — a confirmed-absent poster row that
			// DOES carry a backdrop must still yield a map entry with that
			// backdrop when no poster exists in any language.
			t.Run("backdrop_preserved_when_no_poster_anywhere", func(t *testing.T) {
				ids, repo := seedN(t, 1)
				require.NoError(t, repo.Upsert(ctx, series.SeriesMediaText{
					SeriesID:        ids[0],
					Language:        "ru-RU",
					PosterAsset:     nil, // confirmed-absent poster
					PosterCheckedAt: &checked,
					BackdropAsset:   new("/ru-backdrop.jpg"),
				}))
				out, err := repo.ListByIDsWithFallback(ctx, ids, "ru-RU")
				require.NoError(t, err)
				got, ok := out[ids[0]]
				require.True(t, ok)
				require.NotNil(t, got.BackdropAsset, "backdrop-carrying row must not be dropped")
				assert.Equal(t, "/ru-backdrop.jpg", *got.BackdropAsset)
			})

			// Empty input contract — empty map, no SQL (early return).
			t.Run("empty_input_returns_empty_non_nil_map", func(t *testing.T) {
				_, repo := seedN(t, 1)
				out, err := repo.ListByIDsWithFallback(ctx, nil, "ru-RU")
				require.NoError(t, err)
				require.NotNil(t, out)
				assert.Empty(t, out)
			})
		})
	}
}
