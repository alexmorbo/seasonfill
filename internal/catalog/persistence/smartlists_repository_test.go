package persistence

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/datatypes"
	"gorm.io/gorm"

	database "github.com/alexmorbo/seasonfill/internal/shared/db"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
	"github.com/alexmorbo/seasonfill/internal/shared/testhelpers"
)

// listNow — deterministic anchor. aired episodes air an hour before it;
// "old" is 100d before (past the 90d hiatus cutoff); "soon" is 10d after
// (inside the 35d returning window); "far" is 100d after (beyond it).
var listNow = time.Date(2026, 6, 25, 12, 0, 0, 0, time.UTC)

const listLimit = 50

// seedListSeries inserts a canonical series row with an explicit status and
// (nullable) next_air_date — the two columns seedHealthSeries omits. status ""
// leaves series.status NULL.
func seedListSeries(t *testing.T, db *gorm.DB, id domain.SeriesID, status string, nextAir *time.Time) {
	t.Helper()
	title := fmt.Sprintf("Series %d", id)
	row := database.SeriesModel{
		ID:              id,
		Hydration:       "stub",
		OriginalTitle:   &title,
		OriginCountries: datatypes.JSON([]byte("[]")), // NOT NULL column
		NextAirDate:     nextAir,
		CreatedAt:       listNow,
		UpdatedAt:       listNow,
	}
	if status != "" {
		s := status
		row.Status = &s
	}
	require.NoError(t, db.Create(&row).Error)
}

// seedListEntry wires one series into an instance's library: series row +
// series_cache membership row. Caller seeds episodes/episode_states separately.
func seedListEntry(t *testing.T, db *gorm.DB, instance string, sonarrID domain.SonarrSeriesID, seriesID domain.SeriesID, status string, nextAir *time.Time) {
	t.Helper()
	seedListSeries(t, db, seriesID, status, nextAir)
	seedStatsCache(t, db, instance, sonarrID, seriesID, 0, 0, nil) // series_cache (needs series parent)
}

func TestSmartListsRepository_Shelves(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			repo := NewSmartListsRepository(db)
			ctx := context.Background()

			seedSonarrInstance(t, db, "main") // defensive parent

			aired := listNow.Add(-time.Hour)          // recent aired
			old := listNow.Add(-100 * 24 * time.Hour) // > 90d ago
			soon := listNow.Add(10 * 24 * time.Hour)  // inside 35d
			far := listNow.Add(100 * 24 * time.Hour)  // beyond 35d

			// Series 1 — ENDED with one aired monitored fileless ep → ended_incomplete.
			seedListEntry(t, db, "main", 1, 1, "Ended", nil)
			seedEpisode(t, db, 1000, 1, 1, 1, &aired)
			seedEpisodeState(t, db, "main", 1000, true, false, nil)

			// Series 2 — RETURNING, next episode soon → returning_soon.
			seedListEntry(t, db, "main", 2, 2, "Returning Series", &soon)

			// Series 3 — RETURNING, no next airing, last aired 100d ago → hiatus.
			seedListEntry(t, db, "main", 3, 3, "Returning Series", nil)
			seedEpisode(t, db, 3000, 3, 1, 1, &old)
			seedEpisodeState(t, db, "main", 3000, true, true, nil)

			// Series 4 — ENDED but the aired ep HAS a file → NOT ended_incomplete.
			seedListEntry(t, db, "main", 4, 4, "Ended", nil)
			seedEpisode(t, db, 4000, 4, 1, 1, &aired)
			seedEpisodeState(t, db, "main", 4000, true, true, nil)

			// Series 5 — RETURNING but next airing beyond 35d → NOT returning_soon.
			seedListEntry(t, db, "main", 5, 5, "Returning Series", &far)

			// Series 6 — RETURNING, no next airing, last aired RECENT → NOT hiatus.
			seedListEntry(t, db, "main", 6, 6, "Returning Series", nil)
			seedEpisode(t, db, 6000, 6, 1, 1, &aired)
			seedEpisodeState(t, db, "main", 6000, true, true, nil)

			until := listNow.Add(35 * 24 * time.Hour)
			cutoff := listNow.Add(-90 * 24 * time.Hour)

			// --- ended_incomplete ---
			ended, err := repo.EndedIncomplete(ctx, "main", listNow, listLimit)
			require.NoError(t, err)
			require.Len(t, ended, 1)
			assert.Equal(t, domain.SeriesID(1), ended[0].SeriesID)
			assert.Equal(t, domain.SonarrSeriesID(1), ended[0].SonarrID)
			assert.Equal(t, "Series 1", ended[0].Title)
			assert.Equal(t, 1, ended[0].MissingCount)
			endedCount, err := repo.EndedIncompleteCount(ctx, "main", listNow)
			require.NoError(t, err)
			assert.Equal(t, 1, endedCount)

			// --- returning_soon ---
			returning, err := repo.ReturningSoon(ctx, "main", listNow, until, listLimit)
			require.NoError(t, err)
			require.Len(t, returning, 1)
			assert.Equal(t, domain.SeriesID(2), returning[0].SeriesID)
			require.NotNil(t, returning[0].NextAirDate)
			assert.True(t, returning[0].NextAirDate.Equal(soon))
			returningCount, err := repo.ReturningSoonCount(ctx, "main", listNow, until)
			require.NoError(t, err)
			assert.Equal(t, 1, returningCount)

			// --- hiatus ---
			hiatus, err := repo.Hiatus(ctx, "main", listNow, cutoff, listLimit)
			require.NoError(t, err)
			require.Len(t, hiatus, 1)
			assert.Equal(t, domain.SeriesID(3), hiatus[0].SeriesID)
			require.NotNil(t, hiatus[0].LastAiredAt)
			assert.True(t, hiatus[0].LastAiredAt.Equal(old))
			hiatusCount, err := repo.HiatusCount(ctx, "main", listNow, cutoff)
			require.NoError(t, err)
			assert.Equal(t, 1, hiatusCount)
		})
	}
}

// TestSmartListsRepository_AiredBoundary — a FUTURE monitored fileless episode
// of an ended series is NOT an ended_incomplete gap (aired-only guard).
func TestSmartListsRepository_AiredBoundary(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			repo := NewSmartListsRepository(db)
			ctx := context.Background()

			seedSonarrInstance(t, db, "main")
			future := listNow.Add(time.Hour)

			seedListEntry(t, db, "main", 1, 1, "Ended", nil)
			seedEpisode(t, db, 1, 1, 1, 1, &future) // not yet aired
			seedEpisodeState(t, db, "main", 1, true, false, nil)

			ended, err := repo.EndedIncomplete(ctx, "main", listNow, listLimit)
			require.NoError(t, err)
			assert.Empty(t, ended, "a future monitored fileless episode is NOT an ended gap")
			cnt, err := repo.EndedIncompleteCount(ctx, "main", listNow)
			require.NoError(t, err)
			assert.Equal(t, 0, cnt)

			// Exactly at the boundary (air_date == now) counts as aired → a gap.
			seedListEntry(t, db, "main", 2, 2, "Ended", nil)
			seedEpisode(t, db, 2, 2, 1, 1, &listNow)
			seedEpisodeState(t, db, "main", 2, true, false, nil)

			ended, err = repo.EndedIncomplete(ctx, "main", listNow, listLimit)
			require.NoError(t, err)
			require.Len(t, ended, 1)
			assert.Equal(t, domain.SeriesID(2), ended[0].SeriesID)
		})
	}
}

// TestSmartListsRepository_InstanceScoping — a shelf hit in instance "a" must
// never leak into instance "b".
func TestSmartListsRepository_InstanceScoping(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			repo := NewSmartListsRepository(db)
			ctx := context.Background()

			seedSonarrInstance(t, db, "a")
			seedSonarrInstance(t, db, "b")
			aired := listNow.Add(-time.Hour)

			// Ended-with-gap series lives only in instance "a".
			seedListEntry(t, db, "a", 1, 1, "Ended", nil)
			seedEpisode(t, db, 1, 1, 1, 1, &aired)
			seedEpisodeState(t, db, "a", 1, true, false, nil)

			// Instance "b" has its own series, fully filed → no gap.
			seedListEntry(t, db, "b", 2, 2, "Ended", nil)
			seedEpisode(t, db, 2, 2, 1, 1, &aired)
			seedEpisodeState(t, db, "b", 2, true, true, nil)

			names, err := repo.DistinctInstances(ctx)
			require.NoError(t, err)
			assert.Equal(t, []string{"a", "b"}, names)

			aEnded, err := repo.EndedIncomplete(ctx, "a", listNow, listLimit)
			require.NoError(t, err)
			require.Len(t, aEnded, 1)
			assert.Equal(t, domain.SeriesID(1), aEnded[0].SeriesID)

			bEnded, err := repo.EndedIncomplete(ctx, "b", listNow, listLimit)
			require.NoError(t, err)
			assert.Empty(t, bEnded, "instance b has the file — no gap leaks from a")
			bCount, err := repo.EndedIncompleteCount(ctx, "b", listNow)
			require.NoError(t, err)
			assert.Equal(t, 0, bCount)
		})
	}
}

// TestSmartListsRepository_Empty — an empty DB yields empty shelves / zero
// counts / no instances.
func TestSmartListsRepository_Empty(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			repo := NewSmartListsRepository(db)
			ctx := context.Background()

			names, err := repo.DistinctInstances(ctx)
			require.NoError(t, err)
			assert.Empty(t, names)

			until := listNow.Add(35 * 24 * time.Hour)
			cutoff := listNow.Add(-90 * 24 * time.Hour)

			ended, err := repo.EndedIncomplete(ctx, "main", listNow, listLimit)
			require.NoError(t, err)
			assert.Empty(t, ended)
			returning, err := repo.ReturningSoon(ctx, "main", listNow, until, listLimit)
			require.NoError(t, err)
			assert.Empty(t, returning)
			hiatus, err := repo.Hiatus(ctx, "main", listNow, cutoff, listLimit)
			require.NoError(t, err)
			assert.Empty(t, hiatus)

			ec, err := repo.EndedIncompleteCount(ctx, "main", listNow)
			require.NoError(t, err)
			assert.Equal(t, 0, ec)
			rc, err := repo.ReturningSoonCount(ctx, "main", listNow, until)
			require.NoError(t, err)
			assert.Equal(t, 0, rc)
			hc, err := repo.HiatusCount(ctx, "main", listNow, cutoff)
			require.NoError(t, err)
			assert.Equal(t, 0, hc)
		})
	}
}
