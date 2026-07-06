package persistence

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/internal/catalog/domain/series"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
	"github.com/alexmorbo/seasonfill/internal/shared/testhelpers"
)

// TestSeriesMediaTextsRepository_GetBackdropAnyLang_W1815 proves the W18-15 read
// fallback: a per-COLUMN any-language backdrop that skips NULL backdrops, so a
// poster-only requested-language row never shadows another language's backdrop.
// Dual-backend (SQLite always + testcontainers Postgres when enabled) so the raw
// CASE ORDER BY runs on the production dialect.
func TestSeriesMediaTextsRepository_GetBackdropAnyLang_W1815(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()

			seed := func(t *testing.T) (domain.SeriesID, *SeriesMediaTextsRepository) {
				t.Helper()
				gdb := backend.NewDB(t)
				sid, err := NewSeriesRepository(gdb).Upsert(ctx, sampleCanon("Backdrop AnyLang Show"))
				require.NoError(t, err)
				return sid, NewSeriesMediaTextsRepository(gdb)
			}

			// The load-bearing case — requested-lang row (ru-RU) is POSTER-ONLY
			// (backdrop NULL); en-US row HAS a backdrop. GetWithFallback would
			// pick the ru row and yield a NULL backdrop → hero placeholder.
			t.Run("prefer_lang_null_backdrop_falls_to_en", func(t *testing.T) {
				sid, repo := seed(t)
				require.NoError(t, repo.Upsert(ctx, series.SeriesMediaText{
					SeriesID: sid, Language: "ru-RU", PosterAsset: new("/ru-poster.jpg"),
				}))
				require.NoError(t, repo.Upsert(ctx, series.SeriesMediaText{
					SeriesID: sid, Language: "en-US", PosterAsset: new("/en-poster.jpg"), BackdropAsset: new("/en-backdrop.jpg"),
				}))
				got, err := repo.GetBackdropAnyLang(ctx, sid, "ru-RU")
				require.NoError(t, err)
				require.NotNil(t, got, "must recover en-US backdrop when ru row is poster-only")
				assert.Equal(t, "/en-backdrop.jpg", *got)
			})

			// Requested-lang backdrop wins when present (not shadowed by en-US).
			t.Run("prefer_lang_backdrop_wins", func(t *testing.T) {
				sid, repo := seed(t)
				require.NoError(t, repo.Upsert(ctx, series.SeriesMediaText{
					SeriesID: sid, Language: "ru-RU", BackdropAsset: new("/ru-backdrop.jpg"),
				}))
				require.NoError(t, repo.Upsert(ctx, series.SeriesMediaText{
					SeriesID: sid, Language: "en-US", BackdropAsset: new("/en-backdrop.jpg"),
				}))
				got, err := repo.GetBackdropAnyLang(ctx, sid, "ru-RU")
				require.NoError(t, err)
				require.NotNil(t, got)
				assert.Equal(t, "/ru-backdrop.jpg", *got, "requested lang preferred over en-US")
			})

			// Any-lang tier — neither requested nor en-US carries a backdrop; the
			// lowest-language row wins deterministically (language ASC).
			t.Run("any_lang_tier_lowest_language", func(t *testing.T) {
				sid, repo := seed(t)
				require.NoError(t, repo.Upsert(ctx, series.SeriesMediaText{
					SeriesID: sid, Language: "fr-FR", BackdropAsset: new("/fr-backdrop.jpg"),
				}))
				require.NoError(t, repo.Upsert(ctx, series.SeriesMediaText{
					SeriesID: sid, Language: "de-DE", BackdropAsset: new("/de-backdrop.jpg"),
				}))
				got, err := repo.GetBackdropAnyLang(ctx, sid, "ru-RU")
				require.NoError(t, err)
				require.NotNil(t, got)
				assert.Equal(t, "/de-backdrop.jpg", *got, "language ASC → de-DE before fr-FR")
			})

			// NULL pair — rows exist but ALL backdrops NULL → nil, no error.
			t.Run("all_backdrops_null_returns_nil", func(t *testing.T) {
				sid, repo := seed(t)
				require.NoError(t, repo.Upsert(ctx, series.SeriesMediaText{
					SeriesID: sid, Language: "ru-RU", PosterAsset: new("/ru-poster.jpg"),
				}))
				require.NoError(t, repo.Upsert(ctx, series.SeriesMediaText{
					SeriesID: sid, Language: "en-US", PosterAsset: new("/en-poster.jpg"),
				}))
				got, err := repo.GetBackdropAnyLang(ctx, sid, "ru-RU")
				require.NoError(t, err)
				assert.Nil(t, got, "no backdrop anywhere → nil, no error")
			})

			// No rows at all → nil, no error.
			t.Run("no_rows_returns_nil", func(t *testing.T) {
				sid, repo := seed(t)
				got, err := repo.GetBackdropAnyLang(ctx, sid, "ru-RU")
				require.NoError(t, err)
				assert.Nil(t, got)
			})

			// Empty preferLang normalises to the en-US preference tier.
			t.Run("empty_prefer_lang_normalises_en", func(t *testing.T) {
				sid, repo := seed(t)
				require.NoError(t, repo.Upsert(ctx, series.SeriesMediaText{
					SeriesID: sid, Language: "ru-RU", BackdropAsset: new("/ru-backdrop.jpg"),
				}))
				require.NoError(t, repo.Upsert(ctx, series.SeriesMediaText{
					SeriesID: sid, Language: "en-US", BackdropAsset: new("/en-backdrop.jpg"),
				}))
				got, err := repo.GetBackdropAnyLang(ctx, sid, "")
				require.NoError(t, err)
				require.NotNil(t, got)
				assert.Equal(t, "/en-backdrop.jpg", *got, "empty prefer → en-US preferred")
			})
		})
	}
}
