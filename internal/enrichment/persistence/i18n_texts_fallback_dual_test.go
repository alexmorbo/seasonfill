package persistence

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"github.com/alexmorbo/seasonfill/internal/catalog/domain/series"
	ports "github.com/alexmorbo/seasonfill/internal/shared/dataports"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
	"github.com/alexmorbo/seasonfill/internal/shared/testhelpers"
)

// TestSeriesTexts_GetWithFallback_DualBackend_PRDSection5_6 is the D-7
// (468b) read-side proof for the §5.6 i18n fallback chain: it walks the
// SQLite + Postgres testcontainer matrix on the SAME helper
// (pickLanguageFallback) the composer's branch a / branch b consume in
// production and asserts every PRD scenario lands the right row.
//
// Sibling to the existing TestSeriesTextsRepository_FallbackThreeScenarios
// — that test seeds three table-driven scenarios in one nested loop;
// this one teases each branch into its own t.Run for sharper failure
// signal under -race + extra COALESCE-shield assertions the older test
// does not exercise.
func TestSeriesTexts_GetWithFallback_DualBackend_PRDSection5_6(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()

			t.Run("requested_lang_present_returns_requested", func(t *testing.T) {
				db := backend.NewDB(t)
				seriesID, err := NewSeriesRepository(db).Upsert(ctx, sampleCanon("Severance"))
				require.NoError(t, err)
				repo := NewSeriesTextsRepository(db)
				require.NoError(t, repo.Upsert(ctx, series.SeriesText{
					SeriesID: seriesID, Language: "ru-RU", Title: new("Разделение"),
				}))
				require.NoError(t, repo.Upsert(ctx, series.SeriesText{
					SeriesID: seriesID, Language: "en-US", Title: new("Severance"),
				}))
				got, err := repo.GetWithFallback(ctx, seriesID, "ru-RU")
				require.NoError(t, err)
				assert.Equal(t, "ru-RU", got.Language)
				require.NotNil(t, got.Title)
				assert.Equal(t, "Разделение", *got.Title)
			})

			t.Run("en_us_fallback_when_requested_absent", func(t *testing.T) {
				db := backend.NewDB(t)
				seriesID, err := NewSeriesRepository(db).Upsert(ctx, sampleCanon("Andor"))
				require.NoError(t, err)
				repo := NewSeriesTextsRepository(db)
				require.NoError(t, repo.Upsert(ctx, series.SeriesText{
					SeriesID: seriesID, Language: "en-US", Title: new("Andor"),
				}))
				got, err := repo.GetWithFallback(ctx, seriesID, "ru-RU")
				require.NoError(t, err)
				assert.Equal(t, "en-US", got.Language,
					"§5.6 second-tier: requested missing ⇒ en-US row wins")
			})

			t.Run("first_available_when_no_en_us", func(t *testing.T) {
				db := backend.NewDB(t)
				seriesID, err := NewSeriesRepository(db).Upsert(ctx, sampleCanon("Foundation"))
				require.NoError(t, err)
				repo := NewSeriesTextsRepository(db)
				// 'de-DE' < 'fr-FR' lexicographically — language ASC
				// tiebreaker MUST land on de-DE.
				require.NoError(t, repo.Upsert(ctx, series.SeriesText{
					SeriesID: seriesID, Language: "fr-FR", Title: new("Fondation"),
				}))
				require.NoError(t, repo.Upsert(ctx, series.SeriesText{
					SeriesID: seriesID, Language: "de-DE", Title: new("Stiftung"),
				}))
				got, err := repo.GetWithFallback(ctx, seriesID, "ru-RU")
				require.NoError(t, err)
				assert.Equal(t, "de-DE", got.Language,
					"§5.6 third-tier: language ASC tiebreaker must be deterministic across dialects")
			})

			t.Run("not_found_when_no_rows", func(t *testing.T) {
				db := backend.NewDB(t)
				seriesID, err := NewSeriesRepository(db).Upsert(ctx, sampleCanon("Empty"))
				require.NoError(t, err)
				repo := NewSeriesTextsRepository(db)
				_, err = repo.GetWithFallback(ctx, seriesID, "ru-RU")
				require.Error(t, err)
				assert.True(t, errors.Is(err, ports.ErrNotFound),
					"no-rows must surface ports.ErrNotFound so the composer flips to a degraded source")
			})

			// COALESCE shield regression (memory `seasonfill-upsert-coalesce-pattern`):
			// a follow-up Upsert that leaves EnrichedAt nil MUST NOT
			// blank a previously-stamped freshness column. The 464c COALESCE
			// in the production DoUpdates makes the older value survive.
			t.Run("upsert_nil_enriched_at_preserves_existing_stamp", func(t *testing.T) {
				db := backend.NewDB(t)
				seriesID, err := NewSeriesRepository(db).Upsert(ctx, sampleCanon("Shogun"))
				require.NoError(t, err)
				repo := NewSeriesTextsRepository(db)
				stamp := time.Date(2026, 6, 22, 12, 0, 0, 0, time.UTC)
				require.NoError(t, repo.Upsert(ctx, series.SeriesText{
					SeriesID:   seriesID,
					Language:   "en-US",
					Title:      new("Shogun v1"),
					EnrichedAt: &stamp,
				}))
				// Second upsert without EnrichedAt — the Sonarr-side write
				// shape leaves it nil. COALESCE must preserve the stamp.
				require.NoError(t, repo.Upsert(ctx, series.SeriesText{
					SeriesID: seriesID, Language: "en-US", Title: new("Shogun v2"),
				}))
				got, err := repo.GetWithFallback(ctx, seriesID, "en-US")
				require.NoError(t, err)
				require.NotNil(t, got.EnrichedAt,
					"COALESCE shield must preserve the original enriched_at across nil-EnrichedAt upsert")
				assert.True(t, got.EnrichedAt.Equal(stamp),
					"preserved stamp must equal the original (got=%s want=%s)", got.EnrichedAt, stamp)
				require.NotNil(t, got.Title)
				assert.Equal(t, "Shogun v2", *got.Title, "title still updates")
			})
		})
	}
}

// TestEpisodeTexts_GetWithFallback_DualBackend_PRDSection5_6 mirrors
// the series-side coverage for episode_texts — same fallback chain,
// same COALESCE shield. Episode-level fallback drives the season
// accordion in the seriesdetail composer (composer.go:499) so the
// regression net mirrors the series-side branch.
func TestEpisodeTexts_GetWithFallback_DualBackend_PRDSection5_6(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()

			seedEpisode := func(t *testing.T, backend testhelpers.Backend) (*gorm.DB, domain.EpisodeID) {
				t.Helper()
				db := backend.NewDB(t)
				seriesID, err := NewSeriesRepository(db).Upsert(ctx, sampleCanon("Severance"))
				require.NoError(t, err)
				epIDRaw, err := NewEpisodesRepository(db).Upsert(ctx, series.CanonEpisode{
					SeriesID: seriesID, SeasonNumber: 1, EpisodeNumber: 1,
				})
				require.NoError(t, err)
				return db, domain.EpisodeID(epIDRaw)
			}

			t.Run("requested_lang_present_returns_requested", func(t *testing.T) {
				db, epID := seedEpisode(t, backend)
				repo := NewEpisodeTextsRepository(db)
				require.NoError(t, repo.Upsert(ctx, series.EpisodeText{
					EpisodeID: epID, Language: "ru-RU", Title: new("Пилот"),
				}))
				require.NoError(t, repo.Upsert(ctx, series.EpisodeText{
					EpisodeID: epID, Language: "en-US", Title: new("Pilot"),
				}))
				got, err := repo.GetWithFallback(ctx, epID, "ru-RU")
				require.NoError(t, err)
				assert.Equal(t, "ru-RU", got.Language)
			})

			t.Run("en_us_fallback_and_coalesce_shield", func(t *testing.T) {
				db, epID := seedEpisode(t, backend)
				repo := NewEpisodeTextsRepository(db)
				stamp := time.Date(2026, 6, 22, 12, 0, 0, 0, time.UTC)
				require.NoError(t, repo.Upsert(ctx, series.EpisodeText{
					EpisodeID:  epID,
					Language:   "en-US",
					Title:      new("Pilot v1"),
					EnrichedAt: &stamp,
				}))
				// fallback: requested ru missing ⇒ en wins.
				got, err := repo.GetWithFallback(ctx, epID, "ru-RU")
				require.NoError(t, err)
				assert.Equal(t, "en-US", got.Language)

				// COALESCE: re-upsert nil EnrichedAt MUST NOT blank.
				require.NoError(t, repo.Upsert(ctx, series.EpisodeText{
					EpisodeID: epID, Language: "en-US", Title: new("Pilot v2"),
				}))
				got, err = repo.GetWithFallback(ctx, epID, "en-US")
				require.NoError(t, err)
				require.NotNil(t, got.EnrichedAt,
					"COALESCE shield must preserve episode enriched_at across nil-EnrichedAt upsert")
				assert.True(t, got.EnrichedAt.Equal(stamp))
			})

			t.Run("not_found_when_no_rows", func(t *testing.T) {
				db, epID := seedEpisode(t, backend)
				repo := NewEpisodeTextsRepository(db)
				_, err := repo.GetWithFallback(ctx, epID, "ru-RU")
				require.Error(t, err)
				assert.True(t, errors.Is(err, ports.ErrNotFound))
			})
		})
	}
}
