package persistence

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	database "github.com/alexmorbo/seasonfill/internal/shared/db"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
	"github.com/alexmorbo/seasonfill/internal/shared/testhelpers"
)

// gapNow — deterministic anchor for the aired/future boundary. aired
// episodes air an hour before it, future ones an hour after.
var gapNow = time.Date(2026, 6, 25, 12, 0, 0, 0, time.UTC)

// gapDetailCap mirrors the usecase's gapDetailEpisodeCap — a generous
// safety cap; the repo tests seed far fewer rows so nothing is clipped.
const gapDetailCap = 5000

// seedEpisode inserts a canonical episodes row with an explicit id,
// series_id, season/episode number and (nullable) air_date. Direct model
// Create keeps full control over the air_date column (aired / future /
// NULL) that the gap predicate turns on.
func seedEpisode(t *testing.T, db *gorm.DB, epID int64, seriesID domain.SeriesID, season, episode int, airDate *time.Time) {
	t.Helper()
	row := database.EpisodeModel{
		ID:            epID,
		SeriesID:      seriesID,
		SeasonNumber:  season,
		EpisodeNumber: episode,
		AirDate:       airDate,
		CreatedAt:     gapNow,
		UpdatedAt:     gapNow,
	}
	require.NoError(t, db.Create(&row).Error)
}

// seedEpisodeState inserts a per-instance episode_states row with
// explicit monitored / has_file / deleted_at. deletedAt nil = live row.
func seedEpisodeState(t *testing.T, db *gorm.DB, instance string, epID domain.EpisodeID, monitored, hasFile bool, deletedAt *time.Time) {
	t.Helper()
	row := database.EpisodeStateModel{
		InstanceName: domain.InstanceName(instance),
		EpisodeID:    epID,
		Monitored:    monitored,
		HasFile:      hasFile,
		UpdatedAt:    gapNow,
		DeletedAt:    deletedAt,
	}
	require.NoError(t, db.Create(&row).Error)
}

// seedGapMatrix seeds the full predicate matrix in the given instance and
// returns nothing — the callers assert against the repo. Series/episode
// ids are namespaced by a base offset so multiple instances can coexist.
//
// Series base+1 (season 1, MIXED): one real gap + every negative case.
// Series base+2 (season 1, WHOLE): all aired-monitored fileless.
// Series base+3 (season 2, PARTIAL): one gap, one has_file → not whole.
func seedGapMatrix(t *testing.T, db *gorm.DB, instance string, seriesBase domain.SeriesID, epBase int64) {
	t.Helper()
	aired := gapNow.Add(-time.Hour)
	future := gapNow.Add(time.Hour)

	// --- Series base+1: mixed season 1 ---
	s1 := seriesBase + 1
	seedHealthSeries(t, db, s1, nil, nil, nil)
	seedEpisode(t, db, epBase+0, s1, 1, 1, &aired)  // aired, monitored, no file → GAP
	seedEpisode(t, db, epBase+1, s1, 1, 2, &future) // FUTURE monitored fileless → NOT a gap
	seedEpisode(t, db, epBase+2, s1, 1, 3, nil)     // NULL air_date monitored fileless → NOT a gap
	seedEpisode(t, db, epBase+3, s1, 1, 4, &aired)  // aired monitored HAS FILE → NOT a gap
	seedEpisode(t, db, epBase+4, s1, 1, 5, &aired)  // aired NOT monitored fileless → NOT a gap
	seedEpisode(t, db, epBase+5, s1, 0, 1, &aired)  // SPECIAL (season 0) aired monitored fileless → NOT a gap
	seedEpisode(t, db, epBase+6, s1, 1, 6, &aired)  // aired monitored fileless but SOFT-DELETED → NOT a gap
	seedEpisodeState(t, db, instance, domain.EpisodeID(epBase+0), true, false, nil)
	seedEpisodeState(t, db, instance, domain.EpisodeID(epBase+1), true, false, nil)
	seedEpisodeState(t, db, instance, domain.EpisodeID(epBase+2), true, false, nil)
	seedEpisodeState(t, db, instance, domain.EpisodeID(epBase+3), true, true, nil)
	seedEpisodeState(t, db, instance, domain.EpisodeID(epBase+4), false, false, nil)
	seedEpisodeState(t, db, instance, domain.EpisodeID(epBase+5), true, false, nil)
	seedEpisodeState(t, db, instance, domain.EpisodeID(epBase+6), true, false, &gapNow)

	// --- Series base+2: whole season 1 missing ---
	s2 := seriesBase + 2
	seedHealthSeries(t, db, s2, nil, nil, nil)
	seedEpisode(t, db, epBase+10, s2, 1, 1, &aired)
	seedEpisode(t, db, epBase+11, s2, 1, 2, &aired)
	seedEpisodeState(t, db, instance, domain.EpisodeID(epBase+10), true, false, nil)
	seedEpisodeState(t, db, instance, domain.EpisodeID(epBase+11), true, false, nil)

	// --- Series base+3: partial season 2 (one has file) ---
	s3 := seriesBase + 3
	seedHealthSeries(t, db, s3, nil, nil, nil)
	seedEpisode(t, db, epBase+20, s3, 2, 1, &aired)
	seedEpisode(t, db, epBase+21, s3, 2, 2, &aired)
	seedEpisodeState(t, db, instance, domain.EpisodeID(epBase+20), true, false, nil)
	seedEpisodeState(t, db, instance, domain.EpisodeID(epBase+21), true, true, nil)
}

func TestGapRepository_MatrixAndWholeSeason(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			repo := NewGapRepository(db)
			ctx := context.Background()

			seedGapMatrix(t, db, "main", 100, 1000)

			// Only 4 real gaps: (s101 e1000), (s102 e1010, e1011), (s103 e1020).
			missing, err := repo.MissingEpisodeCount(ctx, "main", gapNow)
			require.NoError(t, err)
			assert.Equal(t, 4, missing, "future/null/hasfile/unmonitored/special/deleted are all excluded")

			whole, err := repo.WholeSeasonMissingCount(ctx, "main", gapNow)
			require.NoError(t, err)
			assert.Equal(t, 1, whole, "only series 102 season 1 is wholly missing")

			// RANK: series ordered by gap count DESC, series_id ASC tiebreak.
			// 102 has 2 gaps, 101 and 103 have 1 each → [102, 101, 103].
			ranks, err := repo.GapSeriesRanked(ctx, "main", gapNow, 50)
			require.NoError(t, err)
			require.Len(t, ranks, 3)
			assert.Equal(t, domain.SeriesID(102), ranks[0].SeriesID)
			assert.Equal(t, 2, ranks[0].GapCount)
			assert.Equal(t, "Series 102", ranks[0].Title)
			assert.Equal(t, domain.SeriesID(101), ranks[1].SeriesID)
			assert.Equal(t, 1, ranks[1].GapCount)
			assert.Equal(t, "Series 101", ranks[1].Title)
			assert.Equal(t, domain.SeriesID(103), ranks[2].SeriesID)
			assert.Equal(t, 1, ranks[2].GapCount)

			// DETAIL for the ranked ids — ordered by series_id, season, episode.
			ids := make([]domain.SeriesID, 0, len(ranks))
			for _, rk := range ranks {
				ids = append(ids, rk.SeriesID)
			}
			rows, err := repo.GapEpisodesForSeries(ctx, "main", gapNow, ids, gapDetailCap)
			require.NoError(t, err)
			require.Len(t, rows, 4)

			// series 101 season 1 — mixed: 2 aired-monitored (gap + has_file), 1 missing.
			assert.Equal(t, domain.SeriesID(101), rows[0].SeriesID)
			assert.Equal(t, "Series 101", rows[0].Title)
			assert.Equal(t, 1, rows[0].SeasonNumber)
			assert.Equal(t, 1, rows[0].EpisodeNumber)
			assert.Equal(t, domain.EpisodeID(1000), rows[0].EpisodeID)
			require.NotNil(t, rows[0].AirDate)
			assert.Equal(t, 2, rows[0].SeasonAiredMonitored)
			assert.Equal(t, 1, rows[0].SeasonMissing)

			// series 102 season 1 — whole season, both rows carry AM=2 miss=2.
			assert.Equal(t, domain.SeriesID(102), rows[1].SeriesID)
			assert.Equal(t, 2, rows[1].SeasonAiredMonitored)
			assert.Equal(t, 2, rows[1].SeasonMissing)
			assert.Equal(t, domain.SeriesID(102), rows[2].SeriesID)

			// series 103 season 2 — partial: AM=2 miss=1.
			assert.Equal(t, domain.SeriesID(103), rows[3].SeriesID)
			assert.Equal(t, 2, rows[3].SeasonNumber)
			assert.Equal(t, 2, rows[3].SeasonAiredMonitored)
			assert.Equal(t, 1, rows[3].SeasonMissing)

			// Empty id list short-circuits to an empty result (invalid IN () guard).
			empty, err := repo.GapEpisodesForSeries(ctx, "main", gapNow, nil, gapDetailCap)
			require.NoError(t, err)
			assert.Empty(t, empty)
		})
	}
}

// TestGapRepository_FutureNotCounted isolates the critical false-positive
// guard: a monitored, fileless, NOT-YET-AIRED episode must never be a gap.
func TestGapRepository_FutureNotCounted(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			repo := NewGapRepository(db)
			ctx := context.Background()

			future := gapNow.Add(time.Hour)
			seedHealthSeries(t, db, 1, nil, nil, nil)
			seedEpisode(t, db, 1, 1, 1, 1, &future)
			seedEpisodeState(t, db, "main", 1, true, false, nil)

			missing, err := repo.MissingEpisodeCount(ctx, "main", gapNow)
			require.NoError(t, err)
			assert.Equal(t, 0, missing, "a future monitored fileless episode is NOT a gap")

			ranks, err := repo.GapSeriesRanked(ctx, "main", gapNow, 50)
			require.NoError(t, err)
			assert.Empty(t, ranks, "no gaps → no ranked series")

			// Exactly at the boundary (air_date == now) counts as aired.
			seedHealthSeries(t, db, 2, nil, nil, nil)
			seedEpisode(t, db, 2, 2, 1, 1, &gapNow)
			seedEpisodeState(t, db, "main", 2, true, false, nil)

			missing, err = repo.MissingEpisodeCount(ctx, "main", gapNow)
			require.NoError(t, err)
			assert.Equal(t, 1, missing, "air_date == now counts as aired (<=)")

			ranks, err = repo.GapSeriesRanked(ctx, "main", gapNow, 50)
			require.NoError(t, err)
			require.Len(t, ranks, 1)
			assert.Equal(t, domain.SeriesID(2), ranks[0].SeriesID)
			assert.Equal(t, 1, ranks[0].GapCount)

			rows, err := repo.GapEpisodesForSeries(ctx, "main", gapNow, []domain.SeriesID{ranks[0].SeriesID}, gapDetailCap)
			require.NoError(t, err)
			require.Len(t, rows, 1)
			assert.Equal(t, domain.EpisodeID(2), rows[0].EpisodeID)
		})
	}
}

// TestGapRepository_PerInstanceIsolation — a gap in instance A must not be
// attributed to instance B, and the instance param scopes every query.
func TestGapRepository_PerInstanceIsolation(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			repo := NewGapRepository(db)
			ctx := context.Background()

			aired := gapNow.Add(-time.Hour)
			seedHealthSeries(t, db, 1, nil, nil, nil)
			seedEpisode(t, db, 1, 1, 1, 1, &aired)
			// Gap present only in instance "a".
			seedEpisodeState(t, db, "a", 1, true, false, nil)
			// Same episode in "b" HAS a file → not a gap there.
			seedEpisodeState(t, db, "b", 1, true, true, nil)

			names, err := repo.DistinctInstances(ctx)
			require.NoError(t, err)
			assert.Equal(t, []string{"a", "b"}, names, "distinct instances, ordered")

			aMissing, err := repo.MissingEpisodeCount(ctx, "a", gapNow)
			require.NoError(t, err)
			assert.Equal(t, 1, aMissing)

			bMissing, err := repo.MissingEpisodeCount(ctx, "b", gapNow)
			require.NoError(t, err)
			assert.Equal(t, 0, bMissing, "instance b has the file — no gap leaks from a")

			aRanks, err := repo.GapSeriesRanked(ctx, "a", gapNow, 50)
			require.NoError(t, err)
			require.Len(t, aRanks, 1)
			assert.Equal(t, domain.SeriesID(1), aRanks[0].SeriesID)

			aRows, err := repo.GapEpisodesForSeries(ctx, "a", gapNow, []domain.SeriesID{1}, gapDetailCap)
			require.NoError(t, err)
			assert.Len(t, aRows, 1)

			bRanks, err := repo.GapSeriesRanked(ctx, "b", gapNow, 50)
			require.NoError(t, err)
			assert.Empty(t, bRanks, "instance b has no gaps")
		})
	}
}

// TestGapRepository_EmptyAndHealthy — no episode_states, and a fully
// file-complete instance, both yield zero counts / empty drill-down.
func TestGapRepository_EmptyAndHealthy(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			repo := NewGapRepository(db)
			ctx := context.Background()

			// Empty DB.
			names, err := repo.DistinctInstances(ctx)
			require.NoError(t, err)
			assert.Empty(t, names)

			missing, err := repo.MissingEpisodeCount(ctx, "main", gapNow)
			require.NoError(t, err)
			assert.Equal(t, 0, missing)

			whole, err := repo.WholeSeasonMissingCount(ctx, "main", gapNow)
			require.NoError(t, err)
			assert.Equal(t, 0, whole)

			ranks, err := repo.GapSeriesRanked(ctx, "main", gapNow, 50)
			require.NoError(t, err)
			assert.Empty(t, ranks)

			rows, err := repo.GapEpisodesForSeries(ctx, "main", gapNow, nil, gapDetailCap)
			require.NoError(t, err)
			assert.Empty(t, rows)

			// Fully healthy instance — aired monitored, all with files.
			aired := gapNow.Add(-time.Hour)
			seedHealthSeries(t, db, 1, nil, nil, nil)
			seedEpisode(t, db, 1, 1, 1, 1, &aired)
			seedEpisode(t, db, 2, 1, 1, 2, &aired)
			seedEpisodeState(t, db, "main", 1, true, true, nil)
			seedEpisodeState(t, db, "main", 2, true, true, nil)

			missing, err = repo.MissingEpisodeCount(ctx, "main", gapNow)
			require.NoError(t, err)
			assert.Equal(t, 0, missing)

			whole, err = repo.WholeSeasonMissingCount(ctx, "main", gapNow)
			require.NoError(t, err)
			assert.Equal(t, 0, whole)

			ranks, err = repo.GapSeriesRanked(ctx, "main", gapNow, 50)
			require.NoError(t, err)
			assert.Empty(t, ranks, "healthy instance has no gaps")

			names, err = repo.DistinctInstances(ctx)
			require.NoError(t, err)
			assert.Equal(t, []string{"main"}, names, "healthy instance still appears (has episode_states rows)")
		})
	}
}
