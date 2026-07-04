package persistence

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/internal/catalog/domain/series"
	ports "github.com/alexmorbo/seasonfill/internal/shared/dataports"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
	"github.com/alexmorbo/seasonfill/internal/shared/testhelpers"
)

// TestSeriesMediaTextsRepository_D0 is the D-0 dual-backend suite for the
// Story 584a series_media_texts foundation (per-language poster/backdrop
// storage). Runs against BOTH SQLite (always) and testcontainers Postgres
// (SEASONFILL_TEST_POSTGRES_ENABLE=1) so the real ON CONFLICT upsert path —
// the one whose bare excluded.* orphan branches trip SQLSTATE 42601 on
// Postgres — is exercised on the production dialect.
//
// Covers, per [[seasonfill-test-quality-bar]]:
//   - happy insert + exact Get round-tripping all six nullable columns
//   - Get miss → ports.ErrNotFound (NULL/error pair)
//   - COALESCE-preserve 2-phase: full row (poster+backdrop+hashes+enriched_at)
//     → poster-only write → backdrop_asset/backdrop_hash/enriched_at PRESERVED,
//     poster rolled forward (regression guard vs. story 552 + bare-excluded)
//   - GetWithFallback §5.6 tiers: ru-RU hit, en-US fallback, neither → NotFound
//   - ListByIDsWithFallback mixed set + empty input
//   - Upsert validation rejects zero series_id / empty language (error pairs)
func TestSeriesMediaTextsRepository_D0(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()

			// seed inserts one series row (FK target) and returns its id +
			// a fresh series_media_texts repo bound to the same DB.
			seed := func(t *testing.T) (domain.SeriesID, *SeriesMediaTextsRepository) {
				t.Helper()
				gdb := backend.NewDB(t)
				seriesID, err := NewSeriesRepository(gdb).Upsert(ctx, sampleCanon("Media Texts Show"))
				require.NoError(t, err)
				return seriesID, NewSeriesMediaTextsRepository(gdb)
			}

			// seedN inserts n distinct series rows on ONE db and returns their
			// ids + a repo bound to that db (for ListByIDsWithFallback).
			seedN := func(t *testing.T, n int) ([]domain.SeriesID, *SeriesMediaTextsRepository) {
				t.Helper()
				gdb := backend.NewDB(t)
				srepo := NewSeriesRepository(gdb)
				ids := make([]domain.SeriesID, n)
				for i := range n {
					// Distinct natural keys so Upsert inserts n separate
					// series rows (sampleCanon reuses a fixed tmdb/tvdb/imdb).
					c := sampleCanon(fmt.Sprintf("Media Texts Show %d", i))
					c.TMDBID = ptrTMDBID(9000 + i)
					c.TVDBID = ptrTVDBID(9100 + i)
					c.IMDBID = ptrIMDBID(fmt.Sprintf("tt90000%02d", i))
					id, err := srepo.Upsert(ctx, c)
					require.NoError(t, err)
					ids[i] = id
				}
				return ids, NewSeriesMediaTextsRepository(gdb)
			}

			t.Run("insert_and_get_roundtrips_all_columns", func(t *testing.T) {
				sid, repo := seed(t)
				stamp := time.Date(2026, 7, 1, 9, 0, 0, 0, time.UTC)
				require.NoError(t, repo.Upsert(ctx, series.SeriesMediaText{
					SeriesID:      sid,
					Language:      "ru-RU",
					PosterAsset:   new("/ru-poster.jpg"),
					PosterHash:    new("posterhash-ru"),
					BackdropAsset: new("/ru-backdrop.jpg"),
					BackdropHash:  new("backdrophash-ru"),
					EnrichedAt:    &stamp,
				}))
				got, err := repo.Get(ctx, sid, "ru-RU")
				require.NoError(t, err)
				assert.Equal(t, sid, got.SeriesID)
				assert.Equal(t, "ru-RU", got.Language)
				require.NotNil(t, got.PosterAsset)
				assert.Equal(t, "/ru-poster.jpg", *got.PosterAsset)
				require.NotNil(t, got.PosterHash)
				assert.Equal(t, "posterhash-ru", *got.PosterHash)
				require.NotNil(t, got.BackdropAsset)
				assert.Equal(t, "/ru-backdrop.jpg", *got.BackdropAsset)
				require.NotNil(t, got.BackdropHash)
				assert.Equal(t, "backdrophash-ru", *got.BackdropHash)
				require.NotNil(t, got.EnrichedAt)
				assert.True(t, got.EnrichedAt.Equal(stamp))
			})

			t.Run("get_not_found", func(t *testing.T) {
				sid, repo := seed(t)
				_, err := repo.Get(ctx, sid, "ru-RU")
				assert.ErrorIs(t, err, ports.ErrNotFound)
			})

			t.Run("idempotent_reupsert_single_row", func(t *testing.T) {
				sid, repo := seed(t)
				row := series.SeriesMediaText{SeriesID: sid, Language: "en-US", PosterAsset: new("/p.jpg")}
				require.NoError(t, repo.Upsert(ctx, row))
				require.NoError(t, repo.Upsert(ctx, row))
				out, err := repo.ListByIDsWithFallback(ctx, []domain.SeriesID{sid}, "en-US")
				require.NoError(t, err)
				require.Len(t, out, 1, "re-upsert must not duplicate the composite-PK row")
			})

			// The load-bearing regression case — the real OnConflict SQL must
			// PRESERVE untouched columns (backdrop + enriched_at) on a
			// poster-only partial write. A bare excluded.* upsert would BLANK
			// them (and trip SQLSTATE 42601 on Postgres for orphan branches).
			t.Run("coalesce_preserve_poster_only_write", func(t *testing.T) {
				sid, repo := seed(t)
				stamp := time.Date(2026, 6, 30, 12, 0, 0, 0, time.UTC)
				require.NoError(t, repo.Upsert(ctx, series.SeriesMediaText{
					SeriesID:      sid,
					Language:      "ru-RU",
					PosterAsset:   new("/poster-v1.jpg"),
					PosterHash:    new("phash-v1"),
					BackdropAsset: new("/backdrop-v1.jpg"),
					BackdropHash:  new("bhash-v1"),
					EnrichedAt:    &stamp,
				}))
				// Partial re-write: only poster fields set; backdrop + enriched nil.
				require.NoError(t, repo.Upsert(ctx, series.SeriesMediaText{
					SeriesID:    sid,
					Language:    "ru-RU",
					PosterAsset: new("/poster-v2.jpg"),
					PosterHash:  new("phash-v2"),
				}))
				got, err := repo.Get(ctx, sid, "ru-RU")
				require.NoError(t, err)
				require.NotNil(t, got.PosterAsset)
				assert.Equal(t, "/poster-v2.jpg", *got.PosterAsset, "poster rolls forward")
				require.NotNil(t, got.PosterHash)
				assert.Equal(t, "phash-v2", *got.PosterHash)
				require.NotNil(t, got.BackdropAsset, "COALESCE must PRESERVE backdrop_asset, not blank it")
				assert.Equal(t, "/backdrop-v1.jpg", *got.BackdropAsset)
				require.NotNil(t, got.BackdropHash, "COALESCE must PRESERVE backdrop_hash, not blank it")
				assert.Equal(t, "bhash-v1", *got.BackdropHash)
				require.NotNil(t, got.EnrichedAt, "COALESCE must PRESERVE enriched_at, not blank it")
				assert.True(t, got.EnrichedAt.Equal(stamp))
			})

			t.Run("upsert_rejects_zero_series_id", func(t *testing.T) {
				_, repo := seed(t)
				err := repo.Upsert(ctx, series.SeriesMediaText{SeriesID: 0, Language: "ru-RU"})
				require.Error(t, err)
				assert.Contains(t, err.Error(), "series_id must be non-zero")
			})

			t.Run("upsert_rejects_empty_language", func(t *testing.T) {
				sid, repo := seed(t)
				err := repo.Upsert(ctx, series.SeriesMediaText{SeriesID: sid, Language: ""})
				require.Error(t, err)
				assert.Contains(t, err.Error(), "language must be non-empty")
			})

			t.Run("get_with_fallback_ru_present", func(t *testing.T) {
				sid, repo := seed(t)
				require.NoError(t, repo.Upsert(ctx, series.SeriesMediaText{SeriesID: sid, Language: "en-US", PosterAsset: new("/en.jpg")}))
				require.NoError(t, repo.Upsert(ctx, series.SeriesMediaText{SeriesID: sid, Language: "ru-RU", PosterAsset: new("/ru.jpg")}))
				got, err := repo.GetWithFallback(ctx, sid, "ru-RU")
				require.NoError(t, err)
				assert.Equal(t, "ru-RU", got.Language, "§5.6 tier-1: ru must NOT be shadowed by en-US")
				require.NotNil(t, got.PosterAsset)
				assert.Equal(t, "/ru.jpg", *got.PosterAsset)
			})

			t.Run("get_with_fallback_ru_absent_en_present", func(t *testing.T) {
				sid, repo := seed(t)
				require.NoError(t, repo.Upsert(ctx, series.SeriesMediaText{SeriesID: sid, Language: "en-US", PosterAsset: new("/en.jpg")}))
				got, err := repo.GetWithFallback(ctx, sid, "ru-RU")
				require.NoError(t, err)
				assert.Equal(t, "en-US", got.Language, "§5.6 tier-2: en-US fallback")
				require.NotNil(t, got.PosterAsset)
				assert.Equal(t, "/en.jpg", *got.PosterAsset)
			})

			t.Run("get_with_fallback_neither_not_found", func(t *testing.T) {
				sid, repo := seed(t)
				_, err := repo.GetWithFallback(ctx, sid, "ru-RU")
				assert.ErrorIs(t, err, ports.ErrNotFound)
			})

			t.Run("list_by_ids_fallback_mixed", func(t *testing.T) {
				ids, repo := seedN(t, 3)
				// id[0] — ru + en (ru wins)
				// id[1] — en only (falls back to en-US)
				// id[2] — none (absent from map → caller keeps canon)
				require.NoError(t, repo.Upsert(ctx, series.SeriesMediaText{SeriesID: ids[0], Language: "en-US", PosterAsset: new("/0-en.jpg")}))
				require.NoError(t, repo.Upsert(ctx, series.SeriesMediaText{SeriesID: ids[0], Language: "ru-RU", PosterAsset: new("/0-ru.jpg")}))
				require.NoError(t, repo.Upsert(ctx, series.SeriesMediaText{SeriesID: ids[1], Language: "en-US", PosterAsset: new("/1-en.jpg")}))

				out, err := repo.ListByIDsWithFallback(ctx, ids, "ru-RU")
				require.NoError(t, err)
				require.Len(t, out, 2, "ids 0,1 surface; id 2 has no row")

				assert.Equal(t, "ru-RU", out[ids[0]].Language)
				require.NotNil(t, out[ids[0]].PosterAsset)
				assert.Equal(t, "/0-ru.jpg", *out[ids[0]].PosterAsset)

				assert.Equal(t, "en-US", out[ids[1]].Language, "en-US fallback")
				require.NotNil(t, out[ids[1]].PosterAsset)
				assert.Equal(t, "/1-en.jpg", *out[ids[1]].PosterAsset)

				_, ok := out[ids[2]]
				assert.False(t, ok, "series with no row in either lang is absent")
			})

			t.Run("list_empty_input_returns_empty_non_nil_map", func(t *testing.T) {
				_, repo := seed(t)
				out, err := repo.ListByIDsWithFallback(ctx, nil, "ru-RU")
				require.NoError(t, err)
				require.NotNil(t, out)
				assert.Empty(t, out)
			})

			t.Run("list_empty_lang_normalises_to_en_US", func(t *testing.T) {
				sid, repo := seed(t)
				require.NoError(t, repo.Upsert(ctx, series.SeriesMediaText{SeriesID: sid, Language: "en-US", PosterAsset: new("/en.jpg")}))
				out, err := repo.ListByIDsWithFallback(ctx, []domain.SeriesID{sid}, "")
				require.NoError(t, err)
				require.Len(t, out, 1)
				assert.Equal(t, "en-US", out[sid].Language)
			})

			// W15-2 — posters have no original_title analogue, so the
			// any-lang tier is the terminal never-empty guarantee: a series
			// carrying ONLY a foreign poster row, requested under en-US with
			// no en-US row, now resolves via the any-lang pass.
			t.Run("any_lang_tier_full_ladder", func(t *testing.T) {
				ids, repo := seedN(t, 4)
				// ids[0] — en-US poster (requested lang) → tier 1
				// ids[1] — only ru-RU poster (foreign)   → tier 3 any-lang
				// ids[2] — only fr-FR poster             → tier 3 any-lang
				// ids[3] — zero rows                     → key absent
				require.NoError(t, repo.Upsert(ctx, series.SeriesMediaText{SeriesID: ids[0], Language: "en-US", PosterAsset: new("/0-en.jpg")}))
				require.NoError(t, repo.Upsert(ctx, series.SeriesMediaText{SeriesID: ids[1], Language: "ru-RU", PosterAsset: new("/1-ru.jpg")}))
				require.NoError(t, repo.Upsert(ctx, series.SeriesMediaText{SeriesID: ids[2], Language: "fr-FR", PosterAsset: new("/2-fr.jpg")}))

				out, err := repo.ListByIDsWithFallback(ctx, ids, "en-US")
				require.NoError(t, err)
				require.Len(t, out, 3, "ids 0,1,2 surface via the ladder; id 3 has no row")
				assert.Equal(t, "en-US", out[ids[0]].Language)
				assert.Equal(t, "ru-RU", out[ids[1]].Language, "any-lang tier: only-foreign poster resolves under en-US")
				require.NotNil(t, out[ids[1]].PosterAsset)
				assert.Equal(t, "/1-ru.jpg", *out[ids[1]].PosterAsset)
				assert.Equal(t, "fr-FR", out[ids[2]].Language)
				_, ok := out[ids[3]]
				assert.False(t, ok, "zero rows → key absent (caller keeps canon)")
			})

			t.Run("any_lang_tier_deterministic_lowest_language", func(t *testing.T) {
				ids, repo := seedN(t, 1)
				require.NoError(t, repo.Upsert(ctx, series.SeriesMediaText{SeriesID: ids[0], Language: "fr-FR", PosterAsset: new("/fr.jpg")}))
				require.NoError(t, repo.Upsert(ctx, series.SeriesMediaText{SeriesID: ids[0], Language: "de-DE", PosterAsset: new("/de.jpg")}))
				out, err := repo.ListByIDsWithFallback(ctx, ids, "en-US")
				require.NoError(t, err)
				require.Len(t, out, 1)
				assert.Equal(t, "de-DE", out[ids[0]].Language, "language ASC → de-DE before fr-FR")
			})
		})
	}
}
