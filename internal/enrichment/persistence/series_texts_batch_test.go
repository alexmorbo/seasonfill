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

// TestSeriesTexts_ListByIDsWithFallback_DualBackend — Story 565
// (B-recs-lang). Dual-backend regression net mirroring the sibling
// TestEpisodeTexts_ListByEpisodeIDsWithFallback_DualBackend so the
// series-level batch fallback preserves the same §5.6 two-tier posture
// used by the recommendations composer.
//
// PRD §5.6 tiers covered:
//   - requested-lang hit (tier 1)
//   - en-US fallback (tier 2)
//   - no-row → key absent (batch path deliberately drops the third
//     first-available tier, see story-550 rationale)
//   - mixed seed (subset of series have lang, rest fall back)
//   - empty seriesIDs slice → empty map, no SQL
//   - COALESCE shield on enriched_at survives through the batch
func TestSeriesTexts_ListByIDsWithFallback_DualBackend(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()

			// seedSeries returns N freshly-inserted series ids. The caller
			// picks which ones to seed with series_texts rows. Each canon
			// gets a unique TMDBID so the tmdb_id partial-unique index
			// doesn't collapse the rows into one.
			seedSeries := func(t *testing.T, n int) (ids []domain.SeriesID, repo *SeriesTextsRepository) {
				t.Helper()
				gdb := backend.NewDB(t)
				seriesRepo := NewSeriesRepository(gdb)
				ids = make([]domain.SeriesID, n)
				for i := range n {
					canon := sampleCanon("series-" + string(rune('A'+i)))
					canon.TMDBID = ptrTMDBID(20000 + i)
					canon.TVDBID = ptrTVDBID(30000 + i)
					sid, err := seriesRepo.Upsert(ctx, canon)
					require.NoError(t, err)
					ids[i] = sid
				}
				return ids, NewSeriesTextsRepository(gdb)
			}

			t.Run("all_series_have_lang_row_returns_lang", func(t *testing.T) {
				ids, repo := seedSeries(t, 3)
				for _, id := range ids {
					require.NoError(t, repo.Upsert(ctx, series.SeriesText{
						SeriesID: id, Language: "ru-RU", Title: new("ру-заголовок"),
					}))
				}
				out, err := repo.ListByIDsWithFallback(ctx, ids, "ru-RU")
				require.NoError(t, err)
				require.Len(t, out, 3)
				for _, id := range ids {
					got, ok := out[id]
					require.True(t, ok, "series %d must be in the map", id)
					assert.Equal(t, "ru-RU", got.Language)
					require.NotNil(t, got.Title)
					assert.Equal(t, "ру-заголовок", *got.Title)
				}
			})

			t.Run("mixed_seed_falls_back_to_en_US_per_series", func(t *testing.T) {
				ids, repo := seedSeries(t, 4)
				// ids[0], ids[1] — only en-US (will fall back)
				// ids[2]         — both ru-RU + en-US (ru-RU wins)
				// ids[3]         — only ru-RU (no fallback needed)
				for _, id := range []domain.SeriesID{ids[0], ids[1], ids[2]} {
					require.NoError(t, repo.Upsert(ctx, series.SeriesText{
						SeriesID: id, Language: "en-US", Title: new("en-title"),
					}))
				}
				for _, id := range []domain.SeriesID{ids[2], ids[3]} {
					require.NoError(t, repo.Upsert(ctx, series.SeriesText{
						SeriesID: id, Language: "ru-RU", Title: new("ru-title"),
					}))
				}
				out, err := repo.ListByIDsWithFallback(ctx, ids, "ru-RU")
				require.NoError(t, err)
				require.Len(t, out, 4, "every series that has *any* row must surface in the map")
				assert.Equal(t, "en-US", out[ids[0]].Language)
				assert.Equal(t, "en-US", out[ids[1]].Language)
				assert.Equal(t, "ru-RU", out[ids[2]].Language)
				assert.Equal(t, "ru-RU", out[ids[3]].Language)
				require.NotNil(t, out[ids[2]].Title)
				assert.Equal(t, "ru-title", *out[ids[2]].Title,
					"§5.6 tier-1: a row in the requested language must NOT be shadowed by en-US")
			})

			t.Run("no_rows_returns_empty_map", func(t *testing.T) {
				ids, repo := seedSeries(t, 2)
				out, err := repo.ListByIDsWithFallback(ctx, ids, "ru-RU")
				require.NoError(t, err)
				assert.Empty(t, out,
					"series with NO rows in either lang or en-US MUST be absent — caller keeps canon.Title")
			})

			t.Run("requested_is_en_US_returns_en_US", func(t *testing.T) {
				ids, repo := seedSeries(t, 2)
				for _, id := range ids {
					require.NoError(t, repo.Upsert(ctx, series.SeriesText{
						SeriesID: id, Language: "en-US", Title: new("en-only"),
					}))
				}
				// When lang == fallbackLanguage the second-pass branch
				// is skipped — verify the first pass still resolves.
				out, err := repo.ListByIDsWithFallback(ctx, ids, "en-US")
				require.NoError(t, err)
				require.Len(t, out, 2)
				for _, id := range ids {
					assert.Equal(t, "en-US", out[id].Language)
				}
			})

			t.Run("empty_series_ids_returns_empty_no_sql", func(t *testing.T) {
				_, repo := seedSeries(t, 0)
				out, err := repo.ListByIDsWithFallback(ctx, []domain.SeriesID{}, "ru-RU")
				require.NoError(t, err)
				assert.Empty(t, out, "empty input → empty map, no SQL issued")
			})

			t.Run("empty_lang_normalises_to_en_US", func(t *testing.T) {
				ids, repo := seedSeries(t, 2)
				for _, id := range ids {
					require.NoError(t, repo.Upsert(ctx, series.SeriesText{
						SeriesID: id, Language: "en-US", Title: new("en-title"),
					}))
				}
				out, err := repo.ListByIDsWithFallback(ctx, ids, "")
				require.NoError(t, err)
				require.Len(t, out, 2)
				for _, id := range ids {
					assert.Equal(t, "en-US", out[id].Language)
				}
			})

			t.Run("coalesce_shield_preserves_enriched_at", func(t *testing.T) {
				ids, repo := seedSeries(t, 1)
				stamp := time.Date(2026, 6, 30, 12, 0, 0, 0, time.UTC)
				require.NoError(t, repo.Upsert(ctx, series.SeriesText{
					SeriesID:   ids[0],
					Language:   "ru-RU",
					Title:      new("v1"),
					EnrichedAt: &stamp,
				}))
				// Sonarr-side-style re-upsert with nil EnrichedAt MUST
				// NOT blank — mirror the per-row COALESCE shield check.
				require.NoError(t, repo.Upsert(ctx, series.SeriesText{
					SeriesID: ids[0], Language: "ru-RU", Title: new("v2"),
				}))
				out, err := repo.ListByIDsWithFallback(ctx, ids, "ru-RU")
				require.NoError(t, err)
				got, ok := out[ids[0]]
				require.True(t, ok)
				require.NotNil(t, got.EnrichedAt,
					"COALESCE shield must survive through the batch projection")
				assert.True(t, got.EnrichedAt.Equal(stamp))
				require.NotNil(t, got.Title)
				assert.Equal(t, "v2", *got.Title, "title still rolls forward")
			})

			// W15-2 — the never-empty ladder's third (any-lang) tier: a
			// series carrying ONLY a foreign row, requested under en-US
			// with no en-US row, now resolves via the any-lang pass
			// instead of dropping out of the map.
			t.Run("any_lang_tier_full_ladder", func(t *testing.T) {
				ids, repo := seedSeries(t, 4)
				// ids[0] — en-US row (requested lang) → tier 1
				// ids[1] — only ru-RU (foreign) row   → tier 3 any-lang
				// ids[2] — only fr-FR row             → tier 3 any-lang
				// ids[3] — zero rows                  → key absent
				require.NoError(t, repo.Upsert(ctx, series.SeriesText{SeriesID: ids[0], Language: "en-US", Title: new("en-title")}))
				require.NoError(t, repo.Upsert(ctx, series.SeriesText{SeriesID: ids[1], Language: "ru-RU", Title: new("ру-title")}))
				require.NoError(t, repo.Upsert(ctx, series.SeriesText{SeriesID: ids[2], Language: "fr-FR", Title: new("fr-title")}))

				out, err := repo.ListByIDsWithFallback(ctx, ids, "en-US")
				require.NoError(t, err)
				require.Len(t, out, 3, "ids 0,1,2 surface via the ladder; id 3 has no row at all")
				assert.Equal(t, "en-US", out[ids[0]].Language)
				assert.Equal(t, "ru-RU", out[ids[1]].Language, "any-lang tier: only-foreign row resolves under en-US")
				assert.Equal(t, "fr-FR", out[ids[2]].Language)
				_, ok := out[ids[3]]
				assert.False(t, ok, "zero text rows → key absent (caller applies original_title terminal tier)")
			})

			// W15-2 — the any-lang pick is deterministic: with two foreign
			// rows and no requested/en-US row, ORDER BY language ASC wins.
			t.Run("any_lang_tier_deterministic_lowest_language", func(t *testing.T) {
				ids, repo := seedSeries(t, 1)
				require.NoError(t, repo.Upsert(ctx, series.SeriesText{SeriesID: ids[0], Language: "fr-FR", Title: new("fr")}))
				require.NoError(t, repo.Upsert(ctx, series.SeriesText{SeriesID: ids[0], Language: "de-DE", Title: new("de")}))
				out, err := repo.ListByIDsWithFallback(ctx, ids, "en-US")
				require.NoError(t, err)
				require.Len(t, out, 1)
				assert.Equal(t, "de-DE", out[ids[0]].Language, "language ASC → de-DE before fr-FR")
			})
		})
	}
}
