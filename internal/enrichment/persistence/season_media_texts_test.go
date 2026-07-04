package persistence

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/internal/catalog/domain/series"
	ports "github.com/alexmorbo/seasonfill/internal/shared/dataports"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
	"github.com/alexmorbo/seasonfill/internal/shared/testhelpers"
)

// TestSeasonMediaTextsRepository_D0 — S-C2 dual-backend suite. Exercises the real
// ON CONFLICT COALESCE upsert on the 3-column composite key across SQLite +
// testcontainers Postgres. Covers happy insert + roundtrip, Get miss →
// ErrNotFound, COALESCE-preserve partial write, validation error pairs, and the
// §5.6 ListBySeriesWithFallback tiers (ru present / en-US fallback / absent).
func TestSeasonMediaTextsRepository_D0(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()

			seed := func(t *testing.T) (domain.SeriesID, *SeasonMediaTextsRepository) {
				t.Helper()
				gdb := backend.NewDB(t)
				seriesID, err := NewSeriesRepository(gdb).Upsert(ctx, sampleCanon("Season Media Show"))
				require.NoError(t, err)
				return seriesID, NewSeasonMediaTextsRepository(gdb)
			}

			t.Run("insert_and_get_roundtrips_all_columns", func(t *testing.T) {
				sid, repo := seed(t)
				stamp := time.Date(2026, 7, 3, 9, 0, 0, 0, time.UTC)
				require.NoError(t, repo.Upsert(ctx, series.SeasonMediaText{
					SeriesID: sid, SeasonNumber: 1, Language: "ru-RU",
					PosterAsset: new("/ru-s1.jpg"), PosterHash: new("hash-ru"),
					BackdropAsset: new("/ru-bd.jpg"), BackdropHash: new("bhash-ru"),
					EnrichedAt: &stamp,
				}))
				got, err := repo.Get(ctx, sid, 1, "ru-RU")
				require.NoError(t, err)
				assert.Equal(t, sid, got.SeriesID)
				assert.Equal(t, 1, got.SeasonNumber)
				assert.Equal(t, "ru-RU", got.Language)
				require.NotNil(t, got.PosterAsset)
				assert.Equal(t, "/ru-s1.jpg", *got.PosterAsset)
				require.NotNil(t, got.PosterHash)
				assert.Equal(t, "hash-ru", *got.PosterHash)
				require.NotNil(t, got.BackdropAsset)
				assert.Equal(t, "/ru-bd.jpg", *got.BackdropAsset)
				require.NotNil(t, got.EnrichedAt)
				assert.True(t, got.EnrichedAt.Equal(stamp))
			})

			t.Run("get_not_found", func(t *testing.T) {
				sid, repo := seed(t)
				_, err := repo.Get(ctx, sid, 1, "ru-RU")
				assert.ErrorIs(t, err, ports.ErrNotFound)
			})

			// Load-bearing regression: poster-only partial write must PRESERVE
			// backdrop + enriched_at (COALESCE), not blank them.
			t.Run("coalesce_preserve_poster_only_write", func(t *testing.T) {
				sid, repo := seed(t)
				stamp := time.Date(2026, 7, 2, 12, 0, 0, 0, time.UTC)
				require.NoError(t, repo.Upsert(ctx, series.SeasonMediaText{
					SeriesID: sid, SeasonNumber: 2, Language: "ru-RU",
					PosterAsset: new("/p-v1.jpg"), PosterHash: new("ph-v1"),
					BackdropAsset: new("/bd-v1.jpg"), BackdropHash: new("bh-v1"),
					EnrichedAt: &stamp,
				}))
				require.NoError(t, repo.Upsert(ctx, series.SeasonMediaText{
					SeriesID: sid, SeasonNumber: 2, Language: "ru-RU",
					PosterAsset: new("/p-v2.jpg"), PosterHash: new("ph-v2"),
				}))
				got, err := repo.Get(ctx, sid, 2, "ru-RU")
				require.NoError(t, err)
				assert.Equal(t, "/p-v2.jpg", *got.PosterAsset, "poster rolls forward")
				assert.Equal(t, "ph-v2", *got.PosterHash)
				require.NotNil(t, got.BackdropAsset, "COALESCE must PRESERVE backdrop_asset")
				assert.Equal(t, "/bd-v1.jpg", *got.BackdropAsset)
				require.NotNil(t, got.BackdropHash)
				assert.Equal(t, "bh-v1", *got.BackdropHash)
				require.NotNil(t, got.EnrichedAt, "COALESCE must PRESERVE enriched_at")
				assert.True(t, got.EnrichedAt.Equal(stamp))
			})

			t.Run("distinct_season_numbers_coexist", func(t *testing.T) {
				sid, repo := seed(t)
				require.NoError(t, repo.Upsert(ctx, series.SeasonMediaText{SeriesID: sid, SeasonNumber: 1, Language: "en-US", PosterAsset: new("/s1.jpg")}))
				require.NoError(t, repo.Upsert(ctx, series.SeasonMediaText{SeriesID: sid, SeasonNumber: 2, Language: "en-US", PosterAsset: new("/s2.jpg")}))
				out, err := repo.ListBySeriesWithFallback(ctx, sid, "en-US")
				require.NoError(t, err)
				require.Len(t, out, 2)
				assert.Equal(t, "/s1.jpg", *out[1].PosterAsset)
				assert.Equal(t, "/s2.jpg", *out[2].PosterAsset)
			})

			t.Run("upsert_rejects_zero_series_id", func(t *testing.T) {
				_, repo := seed(t)
				err := repo.Upsert(ctx, series.SeasonMediaText{SeriesID: 0, SeasonNumber: 1, Language: "ru-RU"})
				require.Error(t, err)
				assert.Contains(t, err.Error(), "series_id must be non-zero")
			})

			t.Run("upsert_rejects_empty_language", func(t *testing.T) {
				sid, repo := seed(t)
				err := repo.Upsert(ctx, series.SeasonMediaText{SeriesID: sid, SeasonNumber: 1, Language: ""})
				require.Error(t, err)
				assert.Contains(t, err.Error(), "language must be non-empty")
			})

			t.Run("list_fallback_ru_present_beats_en", func(t *testing.T) {
				sid, repo := seed(t)
				require.NoError(t, repo.Upsert(ctx, series.SeasonMediaText{SeriesID: sid, SeasonNumber: 1, Language: "en-US", PosterAsset: new("/en.jpg")}))
				require.NoError(t, repo.Upsert(ctx, series.SeasonMediaText{SeriesID: sid, SeasonNumber: 1, Language: "ru-RU", PosterAsset: new("/ru.jpg")}))
				out, err := repo.ListBySeriesWithFallback(ctx, sid, "ru-RU")
				require.NoError(t, err)
				assert.Equal(t, "ru-RU", out[1].Language)
				assert.Equal(t, "/ru.jpg", *out[1].PosterAsset)
			})

			t.Run("list_fallback_ru_absent_en_present", func(t *testing.T) {
				sid, repo := seed(t)
				require.NoError(t, repo.Upsert(ctx, series.SeasonMediaText{SeriesID: sid, SeasonNumber: 1, Language: "en-US", PosterAsset: new("/en.jpg")}))
				out, err := repo.ListBySeriesWithFallback(ctx, sid, "ru-RU")
				require.NoError(t, err)
				assert.Equal(t, "en-US", out[1].Language, "§5.6 tier-2 en-US fallback")
				assert.Equal(t, "/en.jpg", *out[1].PosterAsset)
			})

			t.Run("list_neither_absent_from_map", func(t *testing.T) {
				sid, repo := seed(t)
				out, err := repo.ListBySeriesWithFallback(ctx, sid, "ru-RU")
				require.NoError(t, err)
				_, ok := out[1]
				assert.False(t, ok, "no row in either lang → caller keeps canon")
			})

			t.Run("list_empty_lang_normalises_to_en_US", func(t *testing.T) {
				sid, repo := seed(t)
				require.NoError(t, repo.Upsert(ctx, series.SeasonMediaText{SeriesID: sid, SeasonNumber: 1, Language: "en-US", PosterAsset: new("/en.jpg")}))
				out, err := repo.ListBySeriesWithFallback(ctx, sid, "")
				require.NoError(t, err)
				require.Len(t, out, 1)
				assert.Equal(t, "en-US", out[1].Language)
			})

			// W15-2 — season poster is name-free, so the any-lang tier is
			// safe: a season carrying ONLY a foreign poster row, requested
			// under en-US with no en-US row, now resolves via the any-lang
			// pass — keyed by season_number.
			t.Run("any_lang_tier_foreign_only_poster", func(t *testing.T) {
				sid, repo := seed(t)
				// season 1 — only ru-RU poster (foreign) → tier 3 any-lang
				// season 2 — en-US poster (requested)    → tier 1
				// season 3 — zero rows                   → key absent
				require.NoError(t, repo.Upsert(ctx, series.SeasonMediaText{SeriesID: sid, SeasonNumber: 1, Language: "ru-RU", PosterAsset: new("/s1-ru.jpg")}))
				require.NoError(t, repo.Upsert(ctx, series.SeasonMediaText{SeriesID: sid, SeasonNumber: 2, Language: "en-US", PosterAsset: new("/s2-en.jpg")}))
				out, err := repo.ListBySeriesWithFallback(ctx, sid, "en-US")
				require.NoError(t, err)
				require.Len(t, out, 2, "seasons 1,2 surface via the ladder; season 3 has no row")
				assert.Equal(t, "ru-RU", out[1].Language, "any-lang tier: only-foreign season poster resolves under en-US")
				require.NotNil(t, out[1].PosterAsset)
				assert.Equal(t, "/s1-ru.jpg", *out[1].PosterAsset)
				assert.Equal(t, "en-US", out[2].Language)
				_, ok := out[3]
				assert.False(t, ok, "season with no poster row → key absent (caller keeps canon)")
			})

			t.Run("any_lang_tier_deterministic_lowest_language", func(t *testing.T) {
				sid, repo := seed(t)
				require.NoError(t, repo.Upsert(ctx, series.SeasonMediaText{SeriesID: sid, SeasonNumber: 1, Language: "fr-FR", PosterAsset: new("/fr.jpg")}))
				require.NoError(t, repo.Upsert(ctx, series.SeasonMediaText{SeriesID: sid, SeasonNumber: 1, Language: "de-DE", PosterAsset: new("/de.jpg")}))
				out, err := repo.ListBySeriesWithFallback(ctx, sid, "en-US")
				require.NoError(t, err)
				require.Len(t, out, 1)
				assert.Equal(t, "de-DE", out[1].Language, "language ASC → de-DE before fr-FR")
			})
		})
	}
}
