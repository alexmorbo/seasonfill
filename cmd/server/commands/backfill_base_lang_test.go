package commands

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"github.com/alexmorbo/seasonfill/internal/catalog/domain/series"
	enrichpersistence "github.com/alexmorbo/seasonfill/internal/enrichment/persistence"
	"github.com/alexmorbo/seasonfill/internal/logger"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
	"github.com/alexmorbo/seasonfill/internal/shared/testhelpers"
)

func TestRunBackfillBaseLang(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			log := logger.New(logger.Config{Level: "error", Format: "json"})

			// seed builds: one tmdb series WITHOUT en-US texts (should be
			// nudged) and one tmdb series WITH both en-US texts (untouched).
			seed := func(t *testing.T) (*gorm.DB, domain.SeriesID) {
				t.Helper()
				gdb := backend.NewDB(t)
				sr := enrichpersistence.NewSeriesRepository(gdb)
				str := enrichpersistence.NewSeriesTextsRepository(gdb)
				smr := enrichpersistence.NewSeriesMediaTextsRepository(gdb)

				// tmdb, deficient — plus a pre-set enrichment_tmdb_synced_at so
				// we can assert it gets cleared.
				cA := sampleCanonTMDB("Deficient", 2001)
				now := time.Now().UTC()
				cA.EnrichmentTMDBSyncedAt = &now
				sidA, err := sr.Upsert(ctx, cA)
				require.NoError(t, err)

				// tmdb, fully covered.
				cB := sampleCanonTMDB("Covered", 2002)
				cB.EnrichmentTMDBSyncedAt = &now
				sidB, err := sr.Upsert(ctx, cB)
				require.NoError(t, err)
				tb := "en b"
				require.NoError(t, str.Upsert(ctx, series.SeriesText{SeriesID: sidB, Language: "en-US", Title: &tb}))
				require.NoError(t, smr.Upsert(ctx, series.SeriesMediaText{SeriesID: sidB, Language: "en-US"}))

				return gdb, sidA
			}

			t.Run("dry-run mutates nothing", func(t *testing.T) {
				t.Parallel()
				gdb, sidA := seed(t)
				res, err := runBackfillBaseLang(ctx, gdb, true, log)
				require.NoError(t, err)
				assert.Equal(t, int64(1), res.TMDBNudged) // series A (B covered)
				// enrichment_tmdb_synced_at NOT cleared in dry-run.
				assert.False(t, syncedAtIsNull(t, gdb, sidA), "dry-run must leave synced_at set")
			})

			t.Run("apply nudges, idempotent", func(t *testing.T) {
				t.Parallel()
				gdb, sidA := seed(t)
				res, err := runBackfillBaseLang(ctx, gdb, false, log)
				require.NoError(t, err)
				assert.Equal(t, int64(1), res.TMDBNudged)

				// series A: enrichment_tmdb_synced_at cleared → cold-start picks up.
				assert.True(t, syncedAtIsNull(t, gdb, sidA), "apply must clear synced_at")

				// Second run re-nudges series A (still missing en-US texts); that
				// is harmless and idempotent (NULL→NULL).
				_, err = runBackfillBaseLang(ctx, gdb, false, log)
				require.NoError(t, err)
				assert.True(t, syncedAtIsNull(t, gdb, sidA), "re-nudge keeps synced_at NULL")
			})
		})
	}
}

// syncedAtIsNull reports whether series.enrichment_tmdb_synced_at IS NULL for
// the given id. A COUNT-based check keeps the assertion dialect-portable and
// sidesteps driver Scan quirks around NULL → *time.Time (SQLite rejects it).
func syncedAtIsNull(t *testing.T, gdb *gorm.DB, id domain.SeriesID) bool {
	t.Helper()
	var n int64
	require.NoError(t, gdb.Table("series").
		Where("id = ? AND enrichment_tmdb_synced_at IS NULL", id).
		Count(&n).Error)
	return n == 1
}

// --- local canon helpers (avoid cross-package test-helper import) ---

// sampleCanonTMDB builds a minimal full-hydration canon with a tmdb_id. Title
// lives in series_texts (canon carries no Title field post-S-E3a), so the
// backfill's TMDB-nudge path keys off the missing en-US texts, not a column.
func sampleCanonTMDB(_ string, tmdb domain.TMDBID) series.Canon {
	return series.Canon{
		Hydration: series.HydrationFull,
		TMDBID:    &tmdb,
	}
}
