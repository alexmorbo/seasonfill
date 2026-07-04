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

// TestSeasonTextsRepository_D0 is the D-0 dual-backend suite for the E-1 B3a
// season_texts foundation. Runs against BOTH SQLite (always) and testcontainers
// Postgres (SEASONFILL_TEST_POSTGRES_ENABLE=1) so the real ON CONFLICT upsert
// path — the one that NULLed columns via bare excluded.* in the phase-1 D-0
// catches — is exercised on the production dialect.
//
// Covers, per [[seasonfill-test-quality-bar]]:
//   - happy insert + exact Get
//   - Get miss → ports.ErrNotFound (NULL/error pair)
//   - idempotent re-upsert (one row)
//   - COALESCE-preserve 2-phase (story 580 Test-B): full row → partial write
//     (name only, overview=NULL, enriched_at=NULL) → overview + enriched_at
//     PRESERVED, name rolled forward
//   - Upsert validation rejects zero series_id / empty language (error pairs)
//   - season 0 (TMDB Specials) is a valid key
//   - ListBySeriesWithFallback §5.6 tiers: lang hit, en-US fallback, mixed,
//     absent-key, empty-lang normalisation, requested-is-en-US single pass
func TestSeasonTextsRepository_D0(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()

			// seed inserts one series row (FK target) and returns its id +
			// a fresh season_texts repo bound to the same DB.
			seed := func(t *testing.T) (domain.SeriesID, *SeasonTextsRepository) {
				t.Helper()
				gdb := backend.NewDB(t)
				seriesID, err := NewSeriesRepository(gdb).Upsert(ctx, sampleCanon("Season Texts Show"))
				require.NoError(t, err)
				return seriesID, NewSeasonTextsRepository(gdb)
			}

			t.Run("insert_and_get", func(t *testing.T) {
				sid, repo := seed(t)
				require.NoError(t, repo.Upsert(ctx, series.SeasonText{
					SeriesID:     sid,
					SeasonNumber: 1,
					Language:     "ru-RU",
					Name:         new("Сезон 1"),
					Overview:     new("Описание сезона"),
				}))
				got, err := repo.Get(ctx, sid, 1, "ru-RU")
				require.NoError(t, err)
				assert.Equal(t, sid, got.SeriesID)
				assert.Equal(t, 1, got.SeasonNumber)
				assert.Equal(t, "ru-RU", got.Language)
				require.NotNil(t, got.Name)
				assert.Equal(t, "Сезон 1", *got.Name)
				require.NotNil(t, got.Overview)
				assert.Equal(t, "Описание сезона", *got.Overview)
			})

			t.Run("get_not_found", func(t *testing.T) {
				sid, repo := seed(t)
				_, err := repo.Get(ctx, sid, 7, "ru-RU")
				assert.ErrorIs(t, err, ports.ErrNotFound)
			})

			t.Run("idempotent_reupsert_single_row", func(t *testing.T) {
				sid, repo := seed(t)
				row := series.SeasonText{SeriesID: sid, SeasonNumber: 2, Language: "en-US", Name: new("Season 2")}
				require.NoError(t, repo.Upsert(ctx, row))
				require.NoError(t, repo.Upsert(ctx, row))
				out, err := repo.ListBySeriesWithFallback(ctx, sid, "en-US")
				require.NoError(t, err)
				require.Len(t, out, 1, "re-upsert must not duplicate the composite-PK row")
			})

			// Test-B — the real OnConflict SQL must PRESERVE untouched columns.
			t.Run("coalesce_preserve_2phase", func(t *testing.T) {
				sid, repo := seed(t)
				stamp := time.Date(2026, 6, 30, 12, 0, 0, 0, time.UTC)
				require.NoError(t, repo.Upsert(ctx, series.SeasonText{
					SeriesID:     sid,
					SeasonNumber: 3,
					Language:     "ru-RU",
					Name:         new("v1"),
					Overview:     new("overview-v1"),
					EnrichedAt:   &stamp,
				}))
				// Partial re-write: only Name set; Overview + EnrichedAt nil.
				// A bare excluded.* upsert would BLANK overview + enriched_at.
				require.NoError(t, repo.Upsert(ctx, series.SeasonText{
					SeriesID:     sid,
					SeasonNumber: 3,
					Language:     "ru-RU",
					Name:         new("v2"),
				}))
				got, err := repo.Get(ctx, sid, 3, "ru-RU")
				require.NoError(t, err)
				require.NotNil(t, got.Name)
				assert.Equal(t, "v2", *got.Name, "name rolls forward")
				require.NotNil(t, got.Overview, "COALESCE must PRESERVE overview, not blank it")
				assert.Equal(t, "overview-v1", *got.Overview)
				require.NotNil(t, got.EnrichedAt, "COALESCE must PRESERVE enriched_at, not blank it")
				assert.True(t, got.EnrichedAt.Equal(stamp))
			})

			t.Run("upsert_rejects_zero_series_id", func(t *testing.T) {
				_, repo := seed(t)
				err := repo.Upsert(ctx, series.SeasonText{SeriesID: 0, SeasonNumber: 1, Language: "ru-RU"})
				require.Error(t, err)
				assert.Contains(t, err.Error(), "series_id must be non-zero")
			})

			t.Run("upsert_rejects_empty_language", func(t *testing.T) {
				sid, repo := seed(t)
				err := repo.Upsert(ctx, series.SeasonText{SeriesID: sid, SeasonNumber: 1, Language: ""})
				require.Error(t, err)
				assert.Contains(t, err.Error(), "language must be non-empty")
			})

			t.Run("season_zero_specials_is_valid", func(t *testing.T) {
				sid, repo := seed(t)
				require.NoError(t, repo.Upsert(ctx, series.SeasonText{
					SeriesID: sid, SeasonNumber: 0, Language: "en-US", Name: new("Specials"),
				}))
				got, err := repo.Get(ctx, sid, 0, "en-US")
				require.NoError(t, err)
				require.NotNil(t, got.Name)
				assert.Equal(t, "Specials", *got.Name)
			})

			t.Run("list_by_series_fallback_tiers", func(t *testing.T) {
				sid, repo := seed(t)
				// season 1 — ru + en (ru wins)
				// season 2 — en only (falls back to en-US)
				// season 3 — ru only (no fallback needed)
				// season 4 — none (absent from map → caller uses canon)
				require.NoError(t, repo.Upsert(ctx, series.SeasonText{SeriesID: sid, SeasonNumber: 1, Language: "en-US", Name: new("S1 en")}))
				require.NoError(t, repo.Upsert(ctx, series.SeasonText{SeriesID: sid, SeasonNumber: 1, Language: "ru-RU", Name: new("S1 ru")}))
				require.NoError(t, repo.Upsert(ctx, series.SeasonText{SeriesID: sid, SeasonNumber: 2, Language: "en-US", Name: new("S2 en")}))
				require.NoError(t, repo.Upsert(ctx, series.SeasonText{SeriesID: sid, SeasonNumber: 3, Language: "ru-RU", Name: new("S3 ru")}))

				out, err := repo.ListBySeriesWithFallback(ctx, sid, "ru-RU")
				require.NoError(t, err)
				require.Len(t, out, 3, "seasons 1,2,3 surface; season 4 has no row")

				require.NotNil(t, out[1].Name)
				assert.Equal(t, "ru-RU", out[1].Language)
				assert.Equal(t, "S1 ru", *out[1].Name, "§5.6 tier-1: ru must NOT be shadowed by en-US")

				assert.Equal(t, "en-US", out[2].Language, "§5.6 tier-2: en-US fallback")
				require.NotNil(t, out[2].Name)
				assert.Equal(t, "S2 en", *out[2].Name)

				assert.Equal(t, "ru-RU", out[3].Language)

				_, ok := out[4]
				assert.False(t, ok, "season with no row in either lang is absent")
			})

			t.Run("list_empty_series_returns_empty_map", func(t *testing.T) {
				sid, repo := seed(t)
				out, err := repo.ListBySeriesWithFallback(ctx, sid, "ru-RU")
				require.NoError(t, err)
				assert.Empty(t, out)
			})

			t.Run("list_empty_lang_normalises_to_en_US", func(t *testing.T) {
				sid, repo := seed(t)
				require.NoError(t, repo.Upsert(ctx, series.SeasonText{SeriesID: sid, SeasonNumber: 1, Language: "en-US", Name: new("S1 en")}))
				out, err := repo.ListBySeriesWithFallback(ctx, sid, "")
				require.NoError(t, err)
				require.Len(t, out, 1)
				assert.Equal(t, "en-US", out[1].Language)
			})

			t.Run("list_requested_en_US_single_pass", func(t *testing.T) {
				sid, repo := seed(t)
				require.NoError(t, repo.Upsert(ctx, series.SeasonText{SeriesID: sid, SeasonNumber: 1, Language: "en-US", Name: new("S1 en")}))
				out, err := repo.ListBySeriesWithFallback(ctx, sid, "en-US")
				require.NoError(t, err)
				require.Len(t, out, 1)
				assert.Equal(t, "en-US", out[1].Language)
			})

			// W15-2 CONTRACT — season NAME is deliberately EXCLUDED from the
			// any-lang tier. A season with ONLY a foreign (fr-FR) row,
			// requested en-US with no en-US row, must yield an ABSENT key so
			// the composer uses the FE numbered label ("Season N") rather
			// than leaking a foreign-language season name. This guards the
			// exclusion decision: unlike the sibling text/poster batch reads,
			// season_texts stays strictly two-tier.
			t.Run("name_excluded_from_any_lang_tier_key_absent", func(t *testing.T) {
				sid, repo := seed(t)
				require.NoError(t, repo.Upsert(ctx, series.SeasonText{SeriesID: sid, SeasonNumber: 1, Language: "fr-FR", Name: new("Saison 1")}))
				out, err := repo.ListBySeriesWithFallback(ctx, sid, "en-US")
				require.NoError(t, err)
				_, ok := out[1]
				assert.False(t, ok, "foreign-only season name must NOT leak via any-lang — key absent, caller uses numbered label")
			})
		})
	}
}
