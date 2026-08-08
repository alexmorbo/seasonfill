// Package gaps_test's drill-down regression: the test the original I-2
// story missed. It reproduces the prod bug where the global-LIMIT-50
// GapEpisodes query dropped the biggest-gap series (highest series_id,
// outside the 50-episode window ordered by series_id ASC).
//
// WHY IT IS RED BEFORE THE FIX AND GREEN AFTER:
//
// Seed: 5 series ids 201..205 with gap counts 6,10,14,20,30 — the
// biggest-gap series (205 = 30 gaps) has the HIGHEST id. Total = 80 gap
// episodes (> 50).
//
//   - PRE-FIX: GapEpisodes selects gap EPISODES with a single global
//     LIMIT 50 ORDER BY series_id ASC. The first 50 rows are all of
//     201(6)+202(10)+203(14)+204(20) = 50 exactly, so 205 falls entirely
//     outside the window. The report shows 4 series in series_id order
//     [201,202,203,204]; the biggest-gap series 205 is INVISIBLE.
//     → assertions (a) len==5 and (c) order [205,204,203,202,201] FAIL.
//
//   - POST-FIX: GapSeriesRanked GROUP BYs per series and orders by
//     COUNT(*) DESC, returning all 5 series biggest-first with exact
//     per-series gap totals; GapEpisodesForSeries fills in detail for the
//     ranked ids under a 5000-row safety cap that cannot drop a series.
//     → all assertions pass.
//
// The instance aggregate MissingEpisodeCount (assertion d) is computed by
// a separate untouched query and is 80 both before and after — it pins
// that the aggregate path is not regressed.
package gaps_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/datatypes"
	"gorm.io/gorm"

	"github.com/alexmorbo/seasonfill/internal/catalog/app/gaps"
	"github.com/alexmorbo/seasonfill/internal/catalog/persistence"
	database "github.com/alexmorbo/seasonfill/internal/shared/db"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
	"github.com/alexmorbo/seasonfill/internal/shared/testhelpers"
)

// drillNow — deterministic aired/future boundary for the regression.
var drillNow = time.Date(2026, 6, 25, 12, 0, 0, 0, time.UTC)

// seedDrillSeries inserts a canonical series row (OriginCountries is a
// NOT NULL JSON column, so it must be a non-nil empty array).
func seedDrillSeries(t *testing.T, db *gorm.DB, id domain.SeriesID, title string) {
	t.Helper()
	row := database.SeriesModel{
		ID:              id,
		Hydration:       "stub",
		OriginalTitle:   &title,
		OriginCountries: datatypes.JSON([]byte("[]")),
		CreatedAt:       drillNow,
		UpdatedAt:       drillNow,
	}
	require.NoError(t, db.Create(&row).Error)
}

// seedDrillGap inserts one aired, monitored, fileless (season > 0) episode
// + its live episode_states row — i.e. a real gap.
func seedDrillGap(t *testing.T, db *gorm.DB, instance string, epID int64, seriesID domain.SeriesID, season, episode int, aired time.Time) {
	t.Helper()
	ep := database.EpisodeModel{
		ID:            epID,
		SeriesID:      seriesID,
		SeasonNumber:  season,
		EpisodeNumber: episode,
		AirDate:       &aired,
		CreatedAt:     drillNow,
		UpdatedAt:     drillNow,
	}
	require.NoError(t, db.Create(&ep).Error)
	st := database.EpisodeStateModel{
		InstanceName: domain.InstanceName(instance),
		EpisodeID:    domain.EpisodeID(epID),
		Monitored:    true,
		HasFile:      false,
		UpdatedAt:    drillNow,
	}
	require.NoError(t, db.Create(&st).Error)
}

func TestGapUseCase_Build_TopNSeriesRankedByGapCount(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			repo := persistence.NewGapRepository(db)
			uc := gaps.NewUseCase(repo).WithClock(func() time.Time { return drillNow })
			ctx := context.Background()

			aired := drillNow.Add(-time.Hour)
			specs := []struct {
				id    domain.SeriesID
				count int
			}{
				{201, 6}, {202, 10}, {203, 14}, {204, 20}, {205, 30},
			}
			for _, s := range specs {
				seedDrillSeries(t, db, s.id, fmt.Sprintf("Series %d", int64(s.id)))
				for ep := 1; ep <= s.count; ep++ {
					// Namespace episode ids so they never collide across series.
					epID := int64(s.id)*1000 + int64(ep)
					seedDrillGap(t, db, "homelab", epID, s.id, 1, ep, aired)
				}
			}

			rep, err := uc.Build(ctx, "homelab")
			require.NoError(t, err)
			require.Len(t, rep.Instances, 1)
			inst := rep.Instances[0]

			// (d) true instance total — untouched aggregate query.
			assert.Equal(t, 80, inst.MissingEpisodeCount, "6+10+14+20+30 = 80 aired monitored fileless")

			// (a) ALL 5 series present — pre-fix global LIMIT 50 dropped 205.
			require.Len(t, inst.Series, 5, "top-N ranking must surface all 5 series incl. the highest-id biggest-gap series")

			// (b) each series' MissingCount is EXACT.
			want := map[domain.SeriesID]int{201: 6, 202: 10, 203: 14, 204: 20, 205: 30}
			got := map[domain.SeriesID]int{}
			for _, s := range inst.Series {
				got[s.SeriesID] = s.MissingCount
			}
			assert.Equal(t, want, got, "MissingCount badge is the exact per-series gap total")

			// (c) ordered by MissingCount DESC.
			order := make([]domain.SeriesID, 0, len(inst.Series))
			for _, s := range inst.Series {
				order = append(order, s.SeriesID)
			}
			assert.Equal(t, []domain.SeriesID{205, 204, 203, 202, 201}, order, "biggest-gap-first")
		})
	}
}
