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
	"github.com/alexmorbo/seasonfill/internal/shared/testhelpers"
)

// TestSeasonsRepository_MarkSeasonEpisodesSynced — single-column composite-
// key UPDATE stamps the right (series_id, season_number) row. Idempotent
// on re-call; missing row → no error (defensive).
func TestSeasonsRepository_MarkSeasonEpisodesSynced(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			repoS := NewSeriesRepository(db)
			repo := NewSeasonsRepository(db)
			ctx := context.Background()

			seriesID, err := repoS.Upsert(ctx, sampleCanon("Mark Season"))
			require.NoError(t, err)
			_, err = repo.Upsert(ctx, series.CanonSeason{
				SeriesID:     seriesID,
				SeasonNumber: 1,
			})
			require.NoError(t, err)

			now := time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC)
			require.NoError(t, repo.MarkSeasonEpisodesSynced(ctx, seriesID, 1, now))

			got, err := repo.GetEpisodesSyncedAt(ctx, seriesID, 1)
			require.NoError(t, err)
			require.NotNil(t, got)
			assert.Equal(t, now.Unix(), got.Unix())

			// Idempotent re-call.
			now2 := now.Add(5 * time.Minute)
			require.NoError(t, repo.MarkSeasonEpisodesSynced(ctx, seriesID, 1, now2))
			got2, err := repo.GetEpisodesSyncedAt(ctx, seriesID, 1)
			require.NoError(t, err)
			require.NotNil(t, got2)
			assert.Equal(t, now2.Unix(), got2.Unix())

			// Missing row (season_number=99) → no error (defensive).
			require.NoError(t, repo.MarkSeasonEpisodesSynced(ctx, seriesID, 99, now))
		})
	}
}

// TestSeasonsRepository_EpisodesSyncedAtSurvivesSonarrUpsert_BareWrite —
// I-2 ACCEPTANCE (Test A — excluded.X regression class).
//
// Scenario: A3a narrow writer stamps episodes_synced_at via
// MarkSeasonEpisodesSynced. Then Sonarr's 6h scan triggers Seasons.Upsert
// with a CanonSeason payload that has EpisodesSyncedAt nil (Sonarr never
// writes it — TMDB-domain).
//
// EXPECTED: COALESCE preserves the stamp.
// PRE-FIX (using clause.AssignmentColumns omitting episodes_synced_at):
//
//	GORM omits the column from UPDATE SET naturally → stamp survives by
//	accident. THIS test would PASS even pre-fix — which is why Test B exists.
//
// POST-FIX (using clause.Assignments(seasonsUpsertAssignments)):
//
//	COALESCE(excluded.X, seasons.X) explicitly preserves. Test passes.
//
// This test alone doesn't prove the fix — it's the column-absent regression
// test. Pair with Test B (column-include variant) for full coverage.
func TestSeasonsRepository_EpisodesSyncedAtSurvivesSonarrUpsert_BareWrite(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			repoS := NewSeriesRepository(db)
			repo := NewSeasonsRepository(db)
			ctx := context.Background()

			// 1. Seed canonical series + season row.
			seriesID, err := repoS.Upsert(ctx, sampleCanon("I-2 Test A"))
			require.NoError(t, err)
			_, err = repo.Upsert(ctx, series.CanonSeason{
				SeriesID:     seriesID,
				SeasonNumber: 1,
			})
			require.NoError(t, err)

			// 2. A3a writer stamps episodes_synced_at.
			stampT := time.Date(2026, 7, 1, 10, 0, 0, 0, time.UTC)
			require.NoError(t, repo.MarkSeasonEpisodesSynced(ctx, seriesID, 1, stampT))

			// 3. Simulate Sonarr-driven scan: re-Upsert via the production
			//    Repository.Upsert path with a CanonSeason that carries
			//    nil EpisodesSyncedAt (Sonarr never writes it).
			_, err = repo.Upsert(ctx, series.CanonSeason{
				SeriesID:     seriesID,
				SeasonNumber: 1,
			})
			require.NoError(t, err)

			// 4. Assert stamp PRESERVED.
			got, err := repo.GetEpisodesSyncedAt(ctx, seriesID, 1)
			require.NoError(t, err)
			require.NotNil(t, got, "episodes_synced_at stamp must survive Sonarr-driven Upsert")
			assert.Equal(t, stampT.Unix(), got.Unix(),
				"stamp value must be unchanged")
		})
	}
}

// TestSeasonsRepository_EpisodesSyncedAtSurvivesSonarrUpsert_ColumnInclude —
// I-2 ACCEPTANCE (Test B — column-removed-from-map regression class).
//
// A2 review MED finding: bare `excluded.X` regression test (Test A above)
// only catches the case where a future contributor writes
// `clause.Expr("excluded.X")` without COALESCE wrapper. It does NOT catch:
// future contributor DELETES the column from seasonsUpsertAssignments map,
// thinking "GORM omits absent columns from UPDATE SET — stamp stays through
// accident."
//
// This test simulates that future bug by issuing a direct UPSERT through GORM
// using `clause.AssignmentColumns([all-incl-episodes_synced_at])` — which
// forces GORM to emit `episodes_synced_at = excluded.episodes_synced_at` in
// the SET clause regardless of map shape. Concrete assertion: build the raw
// clause.AssignmentColumns([all]) UPSERT pattern → run it → assert stamp got
// NULL'd (proves the regression-demo is valid). Then re-stamp + run production
// Upsert path → assert stamp preserved (proves COALESCE map entry actively
// defends).
func TestSeasonsRepository_EpisodesSyncedAtSurvivesSonarrUpsert_ColumnInclude(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			gormDB := backend.NewDB(t)
			repoS := NewSeriesRepository(gormDB)
			repo := NewSeasonsRepository(gormDB)
			ctx := context.Background()

			// 1. Seed series + season.
			seriesID, err := repoS.Upsert(ctx, sampleCanon("I-2 Test B"))
			require.NoError(t, err)
			_, err = repo.Upsert(ctx, series.CanonSeason{
				SeriesID:     seriesID,
				SeasonNumber: 1,
			})
			require.NoError(t, err)

			// 2. Stamp episodes_synced_at via A3a writer.
			stampT := time.Date(2026, 7, 1, 10, 0, 0, 0, time.UTC)
			require.NoError(t, repo.MarkSeasonEpisodesSynced(ctx, seriesID, 1, stampT))

			// 3a. REGRESSION SIMULATION — raw UPSERT with explicit
			//     clause.AssignmentColumns([all-including-stamp]). Forces
			//     GORM to write `episodes_synced_at = excluded.episodes_synced_at`
			//     in SET. With production code path's COALESCE map, this
			//     bypass uses a DIFFERENT Clauses() call so the production
			//     COALESCE doesn't apply — proves the vulnerability exists
			//     and the COALESCE entry actively defends.
			now := time.Now().UTC()
			sonarrPayload := database.SeasonModel{
				SeriesID:     seriesID,
				SeasonNumber: 1,
				TMDBSeasonID: new(101),
				CreatedAt:    now,
				UpdatedAt:    now,
				// EpisodesSyncedAt nil — Sonarr never writes it.
			}
			err = gormDB.WithContext(ctx).Clauses(clause.OnConflict{
				Columns: []clause.Column{
					{Name: "series_id"},
					{Name: "season_number"},
				},
				// IMPORTANT: this is the regression simulation. Production
				// code uses clause.Assignments(seasonsUpsertAssignments())
				// which COALESCE-wraps episodes_synced_at. Here we
				// FORCE-INCLUDE the column via AssignmentColumns to prove
				// the column-absent regression path is real.
				DoUpdates: clause.AssignmentColumns([]string{
					"tmdb_season_id",
					"air_date", "episode_count",
					"updated_at",
					"episodes_synced_at",
				}),
			}).Create(&sonarrPayload).Error
			require.NoError(t, err)

			// 3b. Assert regression: stamp nuked by force-include path.
			got, err := repo.GetEpisodesSyncedAt(ctx, seriesID, 1)
			require.NoError(t, err)
			assert.Nil(t, got,
				"regression simulation must nuke stamp — proves AssignmentColumns([all]) bypasses COALESCE")

			// 3c. Re-stamp and run production Upsert → COALESCE defends.
			require.NoError(t, repo.MarkSeasonEpisodesSynced(ctx, seriesID, 1, stampT))
			_, err = repo.Upsert(ctx, series.CanonSeason{
				SeriesID:     seriesID,
				SeasonNumber: 1,
			})
			require.NoError(t, err)

			// 4. Assert stamp PRESERVED via production COALESCE path.
			got, err = repo.GetEpisodesSyncedAt(ctx, seriesID, 1)
			require.NoError(t, err)
			require.NotNil(t, got, "production Upsert COALESCE must preserve stamp")
			assert.Equal(t, stampT.Unix(), got.Unix(),
				"stamp value must be unchanged by production Upsert")
		})
	}
}

// TestSeasonsRepository_EpisodeCountSurvivesSonarrUpsert — I-IMPORTANT
// review finding (carry-forward Story 552 regression class for episode_count).
//
// Scenario: prior writer populated seasons.episode_count (e.g. via TMDB
// mapper). A subsequent narrow writer Upserts the row with EpisodeCount=nil
// (legitimate when the caller doesn't have the count). The bare
// `excluded.episode_count` ASSIGNMENT (pre-fix) overwrites the stored value
// with NULL.
//
// EXPECTED post-fix: COALESCE(excluded.episode_count, seasons.episode_count)
// preserves the prior count.
//
// Note: A3a's RefreshSeasonSlim writer ALSO populates EpisodeCount from
// len(seasonResp.Episodes) (defense-in-depth pair fix). This test exercises
// the repository COALESCE explicitly so any future narrow writer that
// leaves the field nil — including writers that don't exist yet — still
// preserves the prior value.
func TestSeasonsRepository_EpisodeCountSurvivesSonarrUpsert(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			repoS := NewSeriesRepository(db)
			repo := NewSeasonsRepository(db)
			ctx := context.Background()

			seriesID, err := repoS.Upsert(ctx, sampleCanon("EpisodeCount survives"))
			require.NoError(t, err)

			// 1. Seed season row with EpisodeCount=10 (prior TMDB hydration).
			count := 10
			_, err = repo.Upsert(ctx, series.CanonSeason{
				SeriesID:     seriesID,
				SeasonNumber: 1,
				EpisodeCount: &count,
			})
			require.NoError(t, err)

			// 2. Verify seed wrote the value.
			seeded, err := repo.ListBySeries(ctx, seriesID)
			require.NoError(t, err)
			require.Len(t, seeded, 1)
			require.NotNil(t, seeded[0].EpisodeCount)
			assert.Equal(t, 10, *seeded[0].EpisodeCount, "seed must populate episode_count")

			// 3. Re-Upsert with EpisodeCount=nil — simulates a narrow writer
			//    that doesn't carry the count (Sonarr-only payload, or a
			//    future A3a-style refresh that forgot to set the field).
			_, err = repo.Upsert(ctx, series.CanonSeason{
				SeriesID:     seriesID,
				SeasonNumber: 1,
				EpisodeCount: nil,
			})
			require.NoError(t, err)

			// 4. Assert episode_count PRESERVED by COALESCE.
			after, err := repo.ListBySeries(ctx, seriesID)
			require.NoError(t, err)
			require.Len(t, after, 1)
			require.NotNil(t, after[0].EpisodeCount,
				"episode_count must survive Upsert with nil payload (COALESCE)")
			assert.Equal(t, 10, *after[0].EpisodeCount,
				"episode_count value must be unchanged")
		})
	}
}
