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

// TestEpisodeTexts_ListByEpisodeIDsWithFallback_DualBackend is the D-0
// dual-backend (sqlite + Postgres testcontainer) regression net for
// Story 550's batch fallback. Mirrors the sibling
// TestEpisodeTexts_GetWithFallback_DualBackend_PRDSection5_6 single-row
// coverage so the contract diff between per-row and batch paths is
// caught at unit scope.
//
// PRD §5.6 tiers covered:
//   - requested-lang hit (tier 1)
//   - en-US fallback (tier 2)
//   - no-row → key absent (no third-tier match attempt — batch path
//     deliberately drops first-available semantics, see story-550)
//   - mixed seed (subset of episodes have lang, rest fall back) — the
//     hot path SVU exercises.
//   - empty episodeIDs slice → empty map (cheap path, no SQL issued).
//   - COALESCE shield: a nil-EnrichedAt upsert MUST NOT blank a row
//     that the batch projection then sources.
func TestEpisodeTexts_ListByEpisodeIDsWithFallback_DualBackend(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()

			// seedEpisodes returns N freshly-inserted episode ids for
			// a fresh series. The caller picks which ones to seed
			// with text rows.
			seedEpisodes := func(t *testing.T, n int) (db any, ids []domain.EpisodeID, repo *EpisodeTextsRepository) {
				t.Helper()
				gdb := backend.NewDB(t)
				sid, err := NewSeriesRepository(gdb).Upsert(ctx, sampleCanon("Severance"))
				require.NoError(t, err)
				episodes := NewEpisodesRepository(gdb)
				ids = make([]domain.EpisodeID, n)
				for i := range n {
					epRaw, err := episodes.Upsert(ctx, series.CanonEpisode{
						SeriesID:      sid,
						SeasonNumber:  1,
						EpisodeNumber: i + 1,
					})
					require.NoError(t, err)
					ids[i] = domain.EpisodeID(epRaw)
				}
				return gdb, ids, NewEpisodeTextsRepository(gdb)
			}

			t.Run("all_episodes_have_lang_row_returns_lang", func(t *testing.T) {
				_, ids, repo := seedEpisodes(t, 3)
				for _, id := range ids {
					require.NoError(t, repo.Upsert(ctx, series.EpisodeText{
						EpisodeID: id, Language: "ru-RU", Title: new("ру-заголовок"),
					}))
				}
				out, err := repo.ListByEpisodeIDsWithFallback(ctx, ids, "ru-RU")
				require.NoError(t, err)
				require.Len(t, out, 3)
				for _, id := range ids {
					got, ok := out[id]
					require.True(t, ok, "episode %d must be in the map", id)
					assert.Equal(t, "ru-RU", got.Language)
					require.NotNil(t, got.Title)
					assert.Equal(t, "ру-заголовок", *got.Title)
				}
			})

			t.Run("mixed_seed_falls_back_to_en_US_per_episode", func(t *testing.T) {
				_, ids, repo := seedEpisodes(t, 4)
				// ids[0], ids[1] — only en-US (will fall back)
				// ids[2]         — both ru-RU + en-US (ru-RU wins)
				// ids[3]         — only ru-RU (no fallback needed)
				for _, id := range []domain.EpisodeID{ids[0], ids[1], ids[2]} {
					require.NoError(t, repo.Upsert(ctx, series.EpisodeText{
						EpisodeID: id, Language: "en-US", Title: new("en-title"),
					}))
				}
				for _, id := range []domain.EpisodeID{ids[2], ids[3]} {
					require.NoError(t, repo.Upsert(ctx, series.EpisodeText{
						EpisodeID: id, Language: "ru-RU", Title: new("ru-title"),
					}))
				}
				out, err := repo.ListByEpisodeIDsWithFallback(ctx, ids, "ru-RU")
				require.NoError(t, err)
				require.Len(t, out, 4, "every episode that has *any* row must surface in the map")
				assert.Equal(t, "en-US", out[ids[0]].Language)
				assert.Equal(t, "en-US", out[ids[1]].Language)
				assert.Equal(t, "ru-RU", out[ids[2]].Language)
				assert.Equal(t, "ru-RU", out[ids[3]].Language)
				require.NotNil(t, out[ids[2]].Title)
				assert.Equal(t, "ru-title", *out[ids[2]].Title,
					"§5.6 tier-1: a row in the requested language must NOT be shadowed by en-US")
			})

			t.Run("no_rows_returns_empty_map", func(t *testing.T) {
				_, ids, repo := seedEpisodes(t, 2)
				out, err := repo.ListByEpisodeIDsWithFallback(ctx, ids, "ru-RU")
				require.NoError(t, err)
				assert.Empty(t, out,
					"episodes with NO rows in either lang or en-US MUST be absent — caller leaves Text nil")
			})

			t.Run("requested_is_en_US_returns_en_US", func(t *testing.T) {
				_, ids, repo := seedEpisodes(t, 2)
				for _, id := range ids {
					require.NoError(t, repo.Upsert(ctx, series.EpisodeText{
						EpisodeID: id, Language: "en-US", Title: new("en-only"),
					}))
				}
				// When lang == fallbackLanguage the second-pass branch
				// is skipped — verify the first pass still resolves.
				out, err := repo.ListByEpisodeIDsWithFallback(ctx, ids, "en-US")
				require.NoError(t, err)
				require.Len(t, out, 2)
				for _, id := range ids {
					assert.Equal(t, "en-US", out[id].Language)
				}
			})

			t.Run("empty_episode_ids_returns_empty_no_sql", func(t *testing.T) {
				_, _, repo := seedEpisodes(t, 0)
				out, err := repo.ListByEpisodeIDsWithFallback(ctx, []domain.EpisodeID{}, "ru-RU")
				require.NoError(t, err)
				assert.Empty(t, out, "empty input → empty map, no SQL issued")
			})

			t.Run("empty_lang_normalises_to_en_US", func(t *testing.T) {
				_, ids, repo := seedEpisodes(t, 2)
				for _, id := range ids {
					require.NoError(t, repo.Upsert(ctx, series.EpisodeText{
						EpisodeID: id, Language: "en-US", Title: new("en-title"),
					}))
				}
				// Mirrors the per-row helper (lang="" → en-US).
				out, err := repo.ListByEpisodeIDsWithFallback(ctx, ids, "")
				require.NoError(t, err)
				require.Len(t, out, 2)
				for _, id := range ids {
					assert.Equal(t, "en-US", out[id].Language)
				}
			})

			t.Run("coalesce_shield_preserves_enriched_at", func(t *testing.T) {
				_, ids, repo := seedEpisodes(t, 1)
				stamp := time.Date(2026, 6, 30, 12, 0, 0, 0, time.UTC)
				require.NoError(t, repo.Upsert(ctx, series.EpisodeText{
					EpisodeID:  ids[0],
					Language:   "ru-RU",
					Title:      new("v1"),
					EnrichedAt: &stamp,
				}))
				// Sonarr-side-style re-upsert with nil EnrichedAt MUST
				// NOT blank — same shield the per-row dual-backend
				// test covers, re-verified through the batch path.
				require.NoError(t, repo.Upsert(ctx, series.EpisodeText{
					EpisodeID: ids[0], Language: "ru-RU", Title: new("v2"),
				}))
				out, err := repo.ListByEpisodeIDsWithFallback(ctx, ids, "ru-RU")
				require.NoError(t, err)
				got, ok := out[ids[0]]
				require.True(t, ok)
				require.NotNil(t, got.EnrichedAt,
					"COALESCE shield must survive through the batch projection")
				assert.True(t, got.EnrichedAt.Equal(stamp))
				require.NotNil(t, got.Title)
				assert.Equal(t, "v2", *got.Title, "title still rolls forward")
			})

			// W15-2 — the never-empty ladder's third (any-lang) tier: an
			// episode carrying ONLY a foreign row, requested under en-US
			// with no en-US row, now resolves via the any-lang pass.
			t.Run("any_lang_tier_full_ladder", func(t *testing.T) {
				_, ids, repo := seedEpisodes(t, 4)
				// ids[0] — en-US row (requested lang) → tier 1
				// ids[1] — only ru-RU (foreign) row   → tier 3 any-lang
				// ids[2] — only fr-FR row             → tier 3 any-lang
				// ids[3] — zero rows                  → key absent
				require.NoError(t, repo.Upsert(ctx, series.EpisodeText{EpisodeID: ids[0], Language: "en-US", Title: new("en-title")}))
				require.NoError(t, repo.Upsert(ctx, series.EpisodeText{EpisodeID: ids[1], Language: "ru-RU", Title: new("ру-title")}))
				require.NoError(t, repo.Upsert(ctx, series.EpisodeText{EpisodeID: ids[2], Language: "fr-FR", Title: new("fr-title")}))

				out, err := repo.ListByEpisodeIDsWithFallback(ctx, ids, "en-US")
				require.NoError(t, err)
				require.Len(t, out, 3, "ids 0,1,2 surface via the ladder; id 3 has no row at all")
				assert.Equal(t, "en-US", out[ids[0]].Language)
				assert.Equal(t, "ru-RU", out[ids[1]].Language, "any-lang tier: only-foreign episode row resolves under en-US")
				assert.Equal(t, "fr-FR", out[ids[2]].Language)
				_, ok := out[ids[3]]
				assert.False(t, ok, "zero text rows → key absent")
			})

			t.Run("any_lang_tier_deterministic_lowest_language", func(t *testing.T) {
				_, ids, repo := seedEpisodes(t, 1)
				require.NoError(t, repo.Upsert(ctx, series.EpisodeText{EpisodeID: ids[0], Language: "fr-FR", Title: new("fr")}))
				require.NoError(t, repo.Upsert(ctx, series.EpisodeText{EpisodeID: ids[0], Language: "de-DE", Title: new("de")}))
				out, err := repo.ListByEpisodeIDsWithFallback(ctx, ids, "en-US")
				require.NoError(t, err)
				require.Len(t, out, 1)
				assert.Equal(t, "de-DE", out[ids[0]].Language, "language ASC → de-DE before fr-FR")
			})
		})
	}
}
