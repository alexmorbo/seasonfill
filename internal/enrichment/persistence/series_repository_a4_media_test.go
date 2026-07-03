package persistence

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm/clause"

	"github.com/alexmorbo/seasonfill/internal/catalog/domain/series"
	database "github.com/alexmorbo/seasonfill/internal/shared/db"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
	"github.com/alexmorbo/seasonfill/internal/shared/testhelpers"
)

// TestSeriesRepository_MarkMediaSynced — single-column UPDATE stamps
// the row and bumps updated_at. Idempotent on re-call; missing row →
// no error (defensive).
func TestSeriesRepository_MarkMediaSynced(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			repo := NewSeriesRepository(db)
			ctx := context.Background()

			seriesID, err := repo.Upsert(ctx, sampleCanon("Mark Media"))
			require.NoError(t, err)

			now := time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC)
			require.NoError(t, repo.MarkMediaSynced(ctx, seriesID, now))

			canon, err := repo.Get(ctx, seriesID)
			require.NoError(t, err)
			require.NotNil(t, canon.EnrichmentMediaSyncedAt)
			assert.Equal(t, now.Unix(), canon.EnrichmentMediaSyncedAt.Unix())

			// Idempotent re-call.
			now2 := now.Add(5 * time.Minute)
			require.NoError(t, repo.MarkMediaSynced(ctx, seriesID, now2))
			canon2, err := repo.Get(ctx, seriesID)
			require.NoError(t, err)
			require.NotNil(t, canon2.EnrichmentMediaSyncedAt)
			assert.Equal(t, now2.Unix(), canon2.EnrichmentMediaSyncedAt.Unix())

			// Missing row → no error (defensive).
			require.NoError(t, repo.MarkMediaSynced(ctx, domain.SeriesID(999_999), now))
		})
	}
}

// TestSeriesRepository_MediaAssetsSurviveSonarrUpsert_BareWrite — Test A
// (excluded.X regression class) on series.{poster_asset, backdrop_asset,
// enrichment_media_synced_at} columns.
//
// Scenario: A4 narrow writer populates + stamps the three media columns.
// Then Sonarr's 6h scan triggers Series.Upsert with a canonOut payload
// carrying nil PosterAsset/BackdropAsset/EnrichmentMediaSyncedAt (Sonarr
// never writes them — all three are TMDB-domain).
//
// EXPECTED: COALESCE on seriesUpsertAssignments (line 792/793/818 —
// shipped A2) preserves all three values.
//
// This test alone doesn't prove the fix actively defends — Test B does.
// Kept here as the column-present regression test.
func TestSeriesRepository_MediaAssetsSurviveSonarrUpsert_BareWrite(t *testing.T) {
	t.Parallel()

	type mediaCase struct {
		name     string
		stampFn  func(t *testing.T, repo *SeriesRepository, ctx context.Context, id domain.SeriesID)
		assertFn func(t *testing.T, canon series.Canon)
	}

	// S-E3a — poster_asset/backdrop_asset are no longer round-tripped through
	// series.Canon (mappers stopped copying them; series art lives in
	// series_media_texts). Only the enrichment_media_synced_at stamp column is
	// still written+read via the canon Upsert path, so it is the surviving
	// COALESCE case here.
	cases := []mediaCase{
		{
			name: "enrichment_media_synced_at",
			stampFn: func(t *testing.T, repo *SeriesRepository, ctx context.Context, id domain.SeriesID) {
				require.NoError(t, repo.MarkMediaSynced(ctx, id, time.Date(2026, 7, 1, 10, 0, 0, 0, time.UTC)))
			},
			assertFn: func(t *testing.T, canon series.Canon) {
				require.NotNil(t, canon.EnrichmentMediaSyncedAt, "stamp must survive Sonarr-driven Upsert via COALESCE")
			},
		},
	}

	for _, backend := range testhelpers.AllBackends(t) {
		for _, tc := range cases {
			t.Run(backend.Name+"/"+tc.name, func(t *testing.T) {
				t.Parallel()
				db := backend.NewDB(t)
				repo := NewSeriesRepository(db)
				ctx := context.Background()

				// 1. Seed base canon row.
				seriesID, err := repo.Upsert(ctx, sampleCanon("A4 Media Test A "+tc.name))
				require.NoError(t, err)

				// 2. A4 writer populates media column.
				tc.stampFn(t, repo, ctx, seriesID)

				// Sanity: value landed.
				pre, err := repo.Get(ctx, seriesID)
				require.NoError(t, err)
				tc.assertFn(t, pre)

				// 3. Simulate Sonarr-driven scan: re-Upsert via production
				//    Upsert path with a Canon that carries nil for the tested
				//    media column.
				c := sampleCanon("A4 Media Test A " + tc.name + " (Sonarr resync)")
				c.ID = seriesID
				_, err = repo.Upsert(ctx, c)
				require.NoError(t, err)

				// 4. Assert value PRESERVED.
				post, err := repo.Get(ctx, seriesID)
				require.NoError(t, err)
				tc.assertFn(t, post)
			})
		}
	}
}

// TestSeriesRepository_MediaAssetsSurviveSonarrUpsert_ColumnInclude —
// Test B (column-removed-from-map regression class).
//
// Simulates a future contributor removing the COALESCE entry from
// seriesUpsertAssignments by issuing a raw GORM UPSERT with explicit
// clause.AssignmentColumns([all-incl-media]). If COALESCE-by-accident-only
// protected the column, this would nuke it. Then re-populates + runs the
// production Upsert path to prove COALESCE actively defends.
//
// The three media columns share the same defense mechanism (COALESCE
// entries in seriesUpsertAssignments), so a single loop covers them.
func TestSeriesRepository_MediaAssetsSurviveSonarrUpsert_ColumnInclude(t *testing.T) {
	t.Parallel()

	type mediaCase struct {
		name          string
		column        string
		writeFn       func(t *testing.T, repo *SeriesRepository, ctx context.Context, id domain.SeriesID)
		payloadMutate func(m *database.SeriesModel, id domain.SeriesID)
		assertFn      func(t *testing.T, canon series.Canon)
	}

	// S-E3a — poster_asset/backdrop_asset are no longer round-tripped through
	// series.Canon; only the enrichment_media_synced_at stamp still travels the
	// canon Upsert path, so it is the surviving force-include regression case.
	cases := []mediaCase{
		{
			name:   "enrichment_media_synced_at",
			column: "enrichment_media_synced_at",
			writeFn: func(t *testing.T, repo *SeriesRepository, ctx context.Context, id domain.SeriesID) {
				require.NoError(t, repo.MarkMediaSynced(ctx, id, time.Date(2026, 7, 1, 10, 0, 0, 0, time.UTC)))
			},
			payloadMutate: func(m *database.SeriesModel, _ domain.SeriesID) {
				// EnrichmentMediaSyncedAt nil.
			},
			assertFn: func(t *testing.T, canon series.Canon) {
				require.NotNil(t, canon.EnrichmentMediaSyncedAt, "production Upsert COALESCE must preserve stamp")
			},
		},
	}

	for _, backend := range testhelpers.AllBackends(t) {
		for _, tc := range cases {
			t.Run(backend.Name+"/"+tc.name, func(t *testing.T) {
				t.Parallel()
				gormDB := backend.NewDB(t)
				repo := NewSeriesRepository(gormDB)
				ctx := context.Background()

				// 1. Seed canon + populate column via A4-shape write.
				seriesID, err := repo.Upsert(ctx, sampleCanon("A4 Media Test B "+tc.name))
				require.NoError(t, err)
				tc.writeFn(t, repo, ctx, seriesID)

				// 2a. REGRESSION SIMULATION — raw UPSERT with explicit
				//     AssignmentColumns([all-incl-target-column]). Bypasses
				//     production COALESCE map (uses a different Clauses()
				//     call). Proves the vulnerability is real if a future
				//     contributor edits the production map.
				now := time.Now().UTC()
				tmdbID := domain.TMDBID(424242)
				sonarrPayload := database.SeriesModel{
					ID:              seriesID,
					OriginalTitle:   new("A4 Media Test B (force-include sim)"),
					TMDBID:          &tmdbID,
					Hydration:       "full",
					OriginCountries: []byte("[]"),
					CreatedAt:       now,
					UpdatedAt:       now,
					// tested column nil per payloadMutate.
				}
				tc.payloadMutate(&sonarrPayload, seriesID)
				err = gormDB.WithContext(ctx).Clauses(clause.OnConflict{
					Columns: []clause.Column{{Name: "id"}},
					DoUpdates: clause.AssignmentColumns([]string{
						"original_title", "tmdb_id", "hydration",
						tc.column, // FORCE-INCLUDE — proves the regression
						"updated_at",
					}),
				}).Create(&sonarrPayload).Error
				require.NoError(t, err)

				// 2b. Assert regression: value nuked by force-include path.
				canon, err := repo.Get(ctx, seriesID)
				require.NoError(t, err)
				// The tested column should now be nil (nuked). Each case
				// assertion below runs AFTER production Upsert restore,
				// so the regression check is done inline.
				var pre any
				if tc.column == "enrichment_media_synced_at" {
					pre = canon.EnrichmentMediaSyncedAt
				}
				assert.Nil(t, pre, "regression simulation must nuke %s — proves AssignmentColumns([all]) bypasses COALESCE", tc.column)

				// 2c. Re-populate via A4 writer, then run production Upsert →
				//     COALESCE defends.
				tc.writeFn(t, repo, ctx, seriesID)
				c := sampleCanon("A4 Media Test B " + tc.name + " (production Upsert)")
				c.ID = seriesID
				_, err = repo.Upsert(ctx, c)
				require.NoError(t, err)

				// 3. Assert value PRESERVED via production COALESCE path.
				post, err := repo.Get(ctx, seriesID)
				require.NoError(t, err)
				tc.assertFn(t, post)
			})
		}
	}
}
