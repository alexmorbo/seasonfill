package persistence

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/internal/catalog/domain/series"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
	"github.com/alexmorbo/seasonfill/internal/shared/testhelpers"
)

func TestEnrichmentCoverageRepository_EmptyDB(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			gdb := backend.NewDB(t)

			got, err := NewEnrichmentCoverageRepository(gdb).EnrichmentCoverage(ctx)
			require.NoError(t, err)
			assert.Equal(t, int64(0), got.LibraryTotal)
			assert.Equal(t, int64(0), got.PosterCoveredByLang["en-US"])
			assert.Equal(t, int64(0), got.CheckedEmpty["poster"])
			assert.Equal(t, int64(0), got.Unenriched["no_tmdb_id"])
			assert.Equal(t, int64(0), got.Unenriched["never_synced"])
		})
	}
}

func TestEnrichmentCoverageRepository_Coverage(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			gdb := backend.NewDB(t)

			seriesRepo := NewSeriesRepository(gdb)
			mediaRepo := NewSeriesMediaTextsRepository(gdb)
			now := time.Now().UTC()

			// insertCache creates one non-deleted library projection row for a
			// canonical series_id (bounded raw insert — only NOT-NULL /
			// no-default columns; portable ? binds + a bound updated_at).
			insertCache := func(t *testing.T, sid domain.SeriesID, sonarr int) {
				t.Helper()
				require.NoError(t, gdb.Exec(
					`INSERT INTO series_cache
					   (instance_name, sonarr_series_id, series_id, title_slug, updated_at)
					 VALUES (?, ?, ?, ?, ?)`,
					"homelab", sonarr, int64(sid), fmt.Sprintf("slug-%d", sonarr), now,
				).Error)
			}

			// S1: TMDB, synced, library, NON-EMPTY en-US + ru-RU posters → covered both langs.
			c1 := sampleCanon("S1")
			c1.TMDBID, c1.TVDBID, c1.IMDBID = ptrTMDBID(2001), ptrTVDBID(3001), ptrIMDBID("tt2000001")
			sid1, err := seriesRepo.Upsert(ctx, c1)
			require.NoError(t, err)
			require.NoError(t, seriesRepo.MarkTMDBSynced(ctx, sid1, now))
			insertCache(t, sid1, 1)
			pEN, pRU := "/p/en1.jpg", "/p/ru1.jpg"
			require.NoError(t, mediaRepo.Upsert(ctx, series.SeriesMediaText{
				SeriesID: sid1, Language: "en-US", PosterAsset: &pEN, PosterCheckedAt: &now,
			}))
			require.NoError(t, mediaRepo.Upsert(ctx, series.SeriesMediaText{
				SeriesID: sid1, Language: "ru-RU", PosterAsset: &pRU, PosterCheckedAt: &now,
			}))

			// S2: TMDB, synced, library, en-US CHECKED-BUT-EMPTY poster
			// (poster_asset NULL + poster_checked_at SET) → NOT covered, checked_empty poster=1.
			c2 := sampleCanon("S2")
			c2.TMDBID, c2.TVDBID, c2.IMDBID = ptrTMDBID(2002), ptrTVDBID(3002), ptrIMDBID("tt2000002")
			sid2, err := seriesRepo.Upsert(ctx, c2)
			require.NoError(t, err)
			require.NoError(t, seriesRepo.MarkTMDBSynced(ctx, sid2, now))
			insertCache(t, sid2, 2)
			require.NoError(t, mediaRepo.Upsert(ctx, series.SeriesMediaText{
				SeriesID: sid2, Language: "en-US", PosterAsset: nil, PosterCheckedAt: &now,
			}))

			// S3: NO tmdb_id (Sonarr-only), library → unenriched reason=no_tmdb_id.
			c3 := sampleCanon("S3")
			c3.TMDBID, c3.TVDBID, c3.IMDBID = nil, ptrTVDBID(3003), nil
			sid3, err := seriesRepo.Upsert(ctx, c3)
			require.NoError(t, err)
			insertCache(t, sid3, 3)

			// S4: TMDB, NOT synced, library → unenriched reason=never_synced.
			c4 := sampleCanon("S4")
			c4.TMDBID, c4.TVDBID, c4.IMDBID = ptrTMDBID(2004), ptrTVDBID(3004), ptrIMDBID("tt2000004")
			sid4, err := seriesRepo.Upsert(ctx, c4)
			require.NoError(t, err)
			insertCache(t, sid4, 4)

			got, err := NewEnrichmentCoverageRepository(gdb).EnrichmentCoverage(ctx)
			require.NoError(t, err)

			assert.Equal(t, int64(4), got.LibraryTotal, "4 non-deleted library series")
			assert.Equal(t, int64(1), got.PosterCoveredByLang["en-US"], "only S1 has a non-empty en-US poster")
			assert.Equal(t, int64(1), got.PosterCoveredByLang["ru-RU"], "only S1 has a non-empty ru-RU poster")
			assert.Equal(t, int64(1), got.CheckedEmpty["poster"], "S2 en-US is checked-but-empty")
			assert.Equal(t, int64(0), got.CheckedEmpty["backdrop"], "no backdrop markers seeded")
			assert.Equal(t, int64(1), got.Unenriched["no_tmdb_id"], "only S3 has NULL tmdb_id")
			assert.Equal(t, int64(1), got.Unenriched["never_synced"], "only S4 is tmdb+unsynced")
		})
	}
}
