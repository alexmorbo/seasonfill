package persistence

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/internal/catalog/domain/series"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
	"github.com/alexmorbo/seasonfill/internal/shared/testhelpers"
)

// readSeasonPosterAsset reads seasons.poster_asset for (series_id,
// season_number). Returns nil when the column is NULL.
func readSeasonPosterAsset(t *testing.T, repo *SeasonsRepository, seriesID domain.SeriesID, seasonNumber int) *string {
	t.Helper()
	list, err := repo.ListBySeries(context.Background(), seriesID)
	require.NoError(t, err)
	for _, s := range list {
		if s.SeasonNumber == seasonNumber {
			return s.PosterAsset
		}
	}
	t.Fatalf("season %d not found for series_id=%d", seasonNumber, seriesID)
	return nil
}

// TestSeasonsRepository_PosterAssetRefreshOnWrite — Test A (happy-path
// TMDB-owned refresh convention).
//
// Scenario: A4 writer populates seasons.poster_asset with a fresh TMDB
// path over a prior stale value. seasons_repository.go:158 uses BARE
// `excluded.poster_asset` on purpose (TMDB-owned refresh-on-write
// convention). If a future contributor accidentally COALESCE-wraps the
// column, the fresh-poster refresh becomes a no-op → Story 552 mirror
// class (silent staleness).
//
// EXPECTED: bare-excluded honors the new value verbatim.
func TestSeasonsRepository_PosterAssetRefreshOnWrite(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			repoS := NewSeriesRepository(db)
			repo := NewSeasonsRepository(db)
			ctx := context.Background()

			seriesID, err := repoS.Upsert(ctx, sampleCanon("A4 Poster Refresh"))
			require.NoError(t, err)

			// 1. Seed season with old poster.
			oldPath := "/old.jpg"
			_, err = repo.Upsert(ctx, series.CanonSeason{
				SeriesID:     seriesID,
				SeasonNumber: 1,
				Name:         new("Season 1"),
				PosterAsset:  &oldPath,
			})
			require.NoError(t, err)
			require.NotNil(t, readSeasonPosterAsset(t, repo, seriesID, 1))
			assert.Equal(t, oldPath, *readSeasonPosterAsset(t, repo, seriesID, 1))

			// 2. A4 writes fresh TMDB poster path (mirrors A4 payload shape:
			//    poster_asset + Name/Overview/AirDate populated).
			newPath := "/new.jpg"
			_, err = repo.Upsert(ctx, series.CanonSeason{
				SeriesID:     seriesID,
				SeasonNumber: 1,
				Name:         new("Season 1"),
				Overview:     new("Refreshed overview"),
				PosterAsset:  &newPath,
			})
			require.NoError(t, err)

			// 3. Assert fresh value took hold via bare excluded.
			got := readSeasonPosterAsset(t, repo, seriesID, 1)
			require.NotNil(t, got)
			assert.Equal(t, newPath, *got,
				"bare excluded.poster_asset must refresh with fresh TMDB path")
		})
	}
}

// TestSeasonsRepository_PosterAssetNilWriteBlanksPrior — Test B
// (mutation-resistant regression demo — Story 552 class direct exposure).
//
// PROOF of the writer-populates-always CONTRACT bind. This test locks in
// the observed behavior of the current production seasons_repository:
//
//	bare `excluded.poster_asset` in seasonsUpsertAssignments (line 158)
//	is evaluated on EVERY Upsert (clause.Assignments explicitly names
//	the column in SET). A nil PosterAsset in CanonSeason maps to a NULL
//	excluded.poster_asset — the SET clause BLANKS the prior value.
//
// This is a Story 552 mirror class LATENT in the seasons repository. It
// stays safe under A4 ONLY because A4's writer ALWAYS populates
// PosterAsset before Upsert (skip-if-empty filter drops nil-path
// tv.Seasons entries entirely, so seasonPayloads carries no nil-poster
// row).
//
// Test purpose: LOCK IN this contract via a REGRESSION-DEMO test. If a
// future contributor writes a narrow-media writer that forgets to
// populate PosterAsset before Seasons.Upsert, the fresh-poster path
// silently NULLs seasons.poster_asset — operator symptom #2 REGRESSES
// (grey placeholders return).
//
// The mirror correctness assertion (writer-populates-always contract) is
// exercised at the worker layer via
// TestSeriesWorker_RefreshMediaAssets_SeasonsPosterAlwaysPopulated —
// that test asserts every A4 writer payload row carries a non-nil
// PosterAsset. Together the two tests span writer contract + persistence
// contract.
func TestSeasonsRepository_PosterAssetNilWriteBlanksPrior(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			repoS := NewSeriesRepository(db)
			repo := NewSeasonsRepository(db)
			ctx := context.Background()

			seriesID, err := repoS.Upsert(ctx, sampleCanon("A4 Poster Nil-Write Regression"))
			require.NoError(t, err)

			// 1. Seed season with canonical poster.
			existing := "/canonical.jpg"
			_, err = repo.Upsert(ctx, series.CanonSeason{
				SeriesID:     seriesID,
				SeasonNumber: 1,
				Name:         new("Season 1"),
				PosterAsset:  &existing,
			})
			require.NoError(t, err)
			pre := readSeasonPosterAsset(t, repo, seriesID, 1)
			require.NotNil(t, pre)
			assert.Equal(t, existing, *pre)

			// 2. Simulate a broken narrow-media writer that forgot to
			//    populate PosterAsset (regression: nil PosterAsset in
			//    CanonSeason). The bare `excluded.poster_asset` assignment
			//    NULLs the column. THIS IS INTENTIONAL DOCUMENTATION of
			//    the writer-populates-always contract A4 relies on.
			_, err = repo.Upsert(ctx, series.CanonSeason{
				SeriesID:     seriesID,
				SeasonNumber: 1,
				Name:         new("Season 1 Renamed"),
				PosterAsset:  nil, // regression simulation
			})
			require.NoError(t, err)

			// 3. Assert nil-write BLANKS prior. Locks in the contract —
			//    if a future contributor silently switches to COALESCE
			//    (making nil-write preserve), this test will fail loudly,
			//    forcing the reviewer to re-audit the TMDB-owned refresh-
			//    on-write invariant (fresh poster removal must clear the
			//    column when TMDB signals removal upstream).
			post := readSeasonPosterAsset(t, repo, seriesID, 1)
			assert.Nil(t, post,
				"bare excluded.poster_asset MUST blank prior on nil-write — writer-populates-always contract for A4 relies on skip-empty filter dropping nil rows entirely")
		})
	}
}
