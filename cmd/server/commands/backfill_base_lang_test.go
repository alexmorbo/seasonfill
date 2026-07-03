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
			// nudged), one tmdb series WITH both en-US texts (untouched), and
			// one tmdb-less series WITHOUT en-US texts (canon.title copied).
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

				// tmdb-less, deficient.
				cC := sampleCanonTMDBLess("Sonarr Only C")
				sidC, err := sr.Upsert(ctx, cC)
				require.NoError(t, err)
				// Legacy title column (pre-S-E3a) is what the backfill copies.
				seedLegacyTitle(t, gdb, sidC, "Sonarr Only C")

				return gdb, sidA
			}

			t.Run("dry-run mutates nothing", func(t *testing.T) {
				t.Parallel()
				gdb, sidA := seed(t)
				res, err := runBackfillBaseLang(ctx, gdb, true, log)
				require.NoError(t, err)
				assert.Equal(t, int64(1), res.TMDBNudged)  // series A (B covered)
				assert.Equal(t, int64(1), res.CanonCopied) // series C
				// enrichment_tmdb_synced_at NOT cleared in dry-run.
				assert.False(t, syncedAtIsNull(t, gdb, sidA), "dry-run must leave synced_at set")
			})

			t.Run("apply nudges + copies, idempotent", func(t *testing.T) {
				t.Parallel()
				gdb, sidA := seed(t)
				res, err := runBackfillBaseLang(ctx, gdb, false, log)
				require.NoError(t, err)
				assert.Equal(t, int64(1), res.TMDBNudged)
				assert.Equal(t, int64(1), res.CanonCopied)

				// series A: enrichment_tmdb_synced_at cleared → cold-start picks up.
				assert.True(t, syncedAtIsNull(t, gdb, sidA), "apply must clear synced_at")

				// series C got its canon.title copied into en-US series_texts.
				var cnt int64
				require.NoError(t, gdb.Table("series_texts st").
					Joins("JOIN series s ON s.id = st.series_id").
					Where("s.tmdb_id IS NULL AND st.language = ?", "en-US").
					Count(&cnt).Error)
				assert.Equal(t, int64(1), cnt)

				// Second run is a no-op.
				res2, err := runBackfillBaseLang(ctx, gdb, false, log)
				require.NoError(t, err)
				assert.Equal(t, int64(0), res2.CanonCopied)
				// series A already NULL → still selected by the deficient filter
				// (missing en-US texts), so it is re-nudged; that is harmless and
				// idempotent (NULL→NULL). Assert it does not error and stays NULL.
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

// S-E3a — canon no longer carries a Title field; the title lives in the
// legacy `series.title` DB column (still present this story) and in
// series_texts. Upsert(domain.Canon) no longer writes the title column, so
// tests that exercise the legacy-column backfill seed the column directly
// via seedLegacyTitle.
func sampleCanonTMDB(_ string, tmdb domain.TMDBID) series.Canon {
	return series.Canon{
		Hydration: series.HydrationFull,
		TMDBID:    &tmdb,
	}
}

func sampleCanonTMDBLess(_ string) series.Canon {
	tvdb := domain.TVDBID(4242)
	return series.Canon{
		Hydration: series.HydrationStub,
		TVDBID:    &tvdb,
	}
}

// seedLegacyTitle writes the legacy `series.title` column directly, simulating
// a row inserted before S-E3a stopped the canon mapper from copying it. The
// base-lang backfill reads this column (raw SQL) to copy tmdb-less titles into
// series_texts{en-US}.
func seedLegacyTitle(t *testing.T, gdb *gorm.DB, id domain.SeriesID, title string) {
	t.Helper()
	require.NoError(t, gdb.Table("series").Where("id = ?", id).Update("title", title).Error)
}
