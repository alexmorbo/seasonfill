package persistence

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/internal/catalog/domain/series"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
	"github.com/alexmorbo/seasonfill/internal/shared/testhelpers"
)

// TestSeriesMediaTextsRepository_GetPosterAnyLang_Story1081a proves the
// Story 1081a per-COLUMN any-language poster reader: a per-language row whose
// poster_asset is NULL or empty never shadows another language's real poster.
// Mirrors TestSeriesMediaTextsRepository_GetBackdropAnyLang_W1815. Dual-
// backend (SQLite always + testcontainers Postgres when enabled).
func TestSeriesMediaTextsRepository_GetPosterAnyLang_Story1081a(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()

			seed := func(t *testing.T) (domain.SeriesID, *SeriesMediaTextsRepository) {
				t.Helper()
				gdb := backend.NewDB(t)
				sid, err := NewSeriesRepository(gdb).Upsert(ctx, sampleCanon("Poster AnyLang Show"))
				require.NoError(t, err)
				return sid, NewSeriesMediaTextsRepository(gdb)
			}

			// The load-bearing case — requested-lang row (ru-RU) is
			// confirmed-absent (poster NULL, checked_at set); en-US row HAS a
			// poster. GetPosterAnyLang must recover the en-US poster.
			t.Run("confirmed_absent_falls_to_en", func(t *testing.T) {
				sid, repo := seed(t)
				now := time.Now().UTC()
				require.NoError(t, repo.Upsert(ctx, series.SeriesMediaText{
					SeriesID: sid, Language: "ru-RU", PosterAsset: nil, PosterCheckedAt: &now,
				}))
				require.NoError(t, repo.Upsert(ctx, series.SeriesMediaText{
					SeriesID: sid, Language: "en-US", PosterAsset: new("/en-poster.jpg"),
				}))
				got, err := repo.GetPosterAnyLang(ctx, sid, "ru-RU")
				require.NoError(t, err)
				require.NotNil(t, got, "must recover en-US poster when ru row is confirmed-absent")
				assert.Equal(t, "/en-poster.jpg", *got)
			})

			// Requested-lang poster wins when present (not shadowed by en-US).
			t.Run("prefer_lang_poster_wins", func(t *testing.T) {
				sid, repo := seed(t)
				require.NoError(t, repo.Upsert(ctx, series.SeriesMediaText{
					SeriesID: sid, Language: "ru-RU", PosterAsset: new("/ru-poster.jpg"),
				}))
				require.NoError(t, repo.Upsert(ctx, series.SeriesMediaText{
					SeriesID: sid, Language: "en-US", PosterAsset: new("/en-poster.jpg"),
				}))
				got, err := repo.GetPosterAnyLang(ctx, sid, "ru-RU")
				require.NoError(t, err)
				require.NotNil(t, got)
				assert.Equal(t, "/ru-poster.jpg", *got, "requested lang preferred over en-US")
			})

			// Any-lang tier — neither requested nor en-US carries a poster; the
			// lowest-language row wins deterministically (language ASC).
			t.Run("any_lang_tier_lowest_language", func(t *testing.T) {
				sid, repo := seed(t)
				require.NoError(t, repo.Upsert(ctx, series.SeriesMediaText{
					SeriesID: sid, Language: "fr-FR", PosterAsset: new("/fr-poster.jpg"),
				}))
				require.NoError(t, repo.Upsert(ctx, series.SeriesMediaText{
					SeriesID: sid, Language: "de-DE", PosterAsset: new("/de-poster.jpg"),
				}))
				got, err := repo.GetPosterAnyLang(ctx, sid, "ru-RU")
				require.NoError(t, err)
				require.NotNil(t, got)
				assert.Equal(t, "/de-poster.jpg", *got, "language ASC → de-DE before fr-FR")
			})

			// NULL pair — rows exist but ALL posters NULL (truly art-less
			// series) → nil, no error. This is the case the hero must render a
			// monogram for.
			t.Run("all_posters_null_returns_nil", func(t *testing.T) {
				sid, repo := seed(t)
				now := time.Now().UTC()
				require.NoError(t, repo.Upsert(ctx, series.SeriesMediaText{
					SeriesID: sid, Language: "ru-RU", BackdropAsset: new("/ru-backdrop.jpg"), PosterCheckedAt: &now,
				}))
				require.NoError(t, repo.Upsert(ctx, series.SeriesMediaText{
					SeriesID: sid, Language: "en-US", BackdropAsset: new("/en-backdrop.jpg"), PosterCheckedAt: &now,
				}))
				got, err := repo.GetPosterAnyLang(ctx, sid, "ru-RU")
				require.NoError(t, err)
				assert.Nil(t, got, "no poster anywhere → nil, no error")
			})

			// No rows at all → nil, no error.
			t.Run("no_rows_returns_nil", func(t *testing.T) {
				sid, repo := seed(t)
				got, err := repo.GetPosterAnyLang(ctx, sid, "ru-RU")
				require.NoError(t, err)
				assert.Nil(t, got)
			})

			// Empty preferLang normalises to the en-US preference tier.
			t.Run("empty_prefer_lang_normalises_en", func(t *testing.T) {
				sid, repo := seed(t)
				require.NoError(t, repo.Upsert(ctx, series.SeriesMediaText{
					SeriesID: sid, Language: "ru-RU", PosterAsset: new("/ru-poster.jpg"),
				}))
				require.NoError(t, repo.Upsert(ctx, series.SeriesMediaText{
					SeriesID: sid, Language: "en-US", PosterAsset: new("/en-poster.jpg"),
				}))
				got, err := repo.GetPosterAnyLang(ctx, sid, "")
				require.NoError(t, err)
				require.NotNil(t, got)
				assert.Equal(t, "/en-poster.jpg", *got, "empty prefer → en-US preferred")
			})
		})
	}
}
