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

// seedSeriesForMark inserts one series row with a fixed tmdb_id and an optional
// tmdb_changed_at, returning the autoincrement id. Direct model Create keeps full
// control over both columns (series.Canon has no TMDBChangedAt field).
func seedSeriesForMark(t *testing.T, db *gorm.DB, tmdbID int64, changedAt *time.Time) domain.SeriesID {
	t.Helper()
	seed := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC) // distinct fixed created/updated stamp
	tid := domain.TMDBID(tmdbID)
	m := database.SeriesModel{
		TMDBID:          &tid,
		Hydration:       "stub",
		OriginCountries: []byte("[]"),
		TMDBChangedAt:   changedAt,
		CreatedAt:       seed,
		UpdatedAt:       seed,
	}
	require.NoError(t, db.Create(&m).Error)
	return m.ID
}

func readSeriesModel(t *testing.T, db *gorm.DB, id domain.SeriesID) database.SeriesModel {
	t.Helper()
	var m database.SeriesModel
	require.NoError(t, db.Where("id = ?", id).First(&m).Error)
	return m
}

func TestMarkChangedByTMDBIDs_Predicate(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			repo := NewSeriesRepository(db)
			ctx := context.Background()

			boundary := time.Date(2026, 6, 20, 0, 0, 0, 0, time.UTC)
			marked := time.Date(2026, 6, 25, 12, 0, 0, 0, time.UTC)

			// NULL row → marked.
			idNull := seedSeriesForMark(t, db, 1001, nil)
			// changed_at < boundary → re-marked.
			idOld := seedSeriesForMark(t, db, 1002, new(boundary.Add(-2*time.Hour)))
			// changed_at == marked (>= boundary; same-window PENDING) → NOT re-marked.
			idSameWindow := seedSeriesForMark(t, db, 1003, new(marked))
			// changed_at exactly == boundary (>= boundary, not <) → NOT re-marked.
			idAtBoundary := seedSeriesForMark(t, db, 1004, new(boundary))
			// not in ids → untouched.
			idOther := seedSeriesForMark(t, db, 1005, nil)

			n, err := repo.MarkChangedByTMDBIDs(ctx,
				[]int64{1001, 1002, 1003, 1004}, marked, boundary)
			require.NoError(t, err)
			assert.EqualValues(t, 2, n, "only NULL + <boundary rows marked")

			assert.WithinDuration(t, marked, *readSeriesModel(t, db, idNull).TMDBChangedAt, time.Second)
			assert.WithinDuration(t, marked, *readSeriesModel(t, db, idOld).TMDBChangedAt, time.Second)
			// same-window + at-boundary PENDING rows keep their prior value.
			assert.WithinDuration(t, marked, *readSeriesModel(t, db, idSameWindow).TMDBChangedAt, time.Second)
			assert.WithinDuration(t, boundary, *readSeriesModel(t, db, idAtBoundary).TMDBChangedAt, time.Second)
			// untouched row stays NULL.
			assert.Nil(t, readSeriesModel(t, db, idOther).TMDBChangedAt)
		})
	}
}

// Same-window replay must NOT re-mark a still-PENDING series; a row changed
// earlier than the boundary CAN legitimately re-mark (B-04 reformulated — no
// assertion on synced_at, that clause was removed).
func TestMarkChangedByTMDBIDs_ReplaySameWindow(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			repo := NewSeriesRepository(db)
			ctx := context.Background()

			boundary := time.Date(2026, 6, 20, 0, 0, 0, 0, time.UTC)
			marked := time.Date(2026, 6, 25, 12, 0, 0, 0, time.UTC)

			idPending := seedSeriesForMark(t, db, 2001, nil)
			idStale := seedSeriesForMark(t, db, 2002, new(boundary.Add(-3*time.Hour)))

			// First poll marks both.
			n1, err := repo.MarkChangedByTMDBIDs(ctx, []int64{2001, 2002}, marked, boundary)
			require.NoError(t, err)
			assert.EqualValues(t, 2, n1)

			// Replay of the SAME window (same boundary) → both now PENDING at `marked`
			// (>= boundary) → 0 re-marks.
			n2, err := repo.MarkChangedByTMDBIDs(ctx, []int64{2001, 2002}, marked, boundary)
			require.NoError(t, err)
			assert.EqualValues(t, 0, n2, "same-window replay must not re-mark PENDING rows")

			_ = idPending
			_ = idStale
		})
	}
}

func TestMarkChangedByTMDBIDs_EdgeCases(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			repo := NewSeriesRepository(db)
			ctx := context.Background()

			boundary := time.Date(2026, 6, 20, 0, 0, 0, 0, time.UTC)
			marked := time.Date(2026, 6, 25, 12, 0, 0, 0, time.UTC)

			// Empty ids → 0, nil, no query.
			n, err := repo.MarkChangedByTMDBIDs(ctx, nil, marked, boundary)
			require.NoError(t, err)
			assert.EqualValues(t, 0, n)

			// Non-existent ids → 0 rows, no error.
			n, err = repo.MarkChangedByTMDBIDs(ctx, []int64{999999}, marked, boundary)
			require.NoError(t, err)
			assert.EqualValues(t, 0, n)

			// In-page duplicate ids → deduped, single update.
			idDup := seedSeriesForMark(t, db, 3001, nil)
			n, err = repo.MarkChangedByTMDBIDs(ctx, []int64{3001, 3001, 3001}, marked, boundary)
			require.NoError(t, err)
			assert.EqualValues(t, 1, n, "duplicate ids collapse to one update")
			assert.WithinDuration(t, marked, *readSeriesModel(t, db, idDup).TMDBChangedAt, time.Second)
		})
	}
}

// updated_at must NOT be bumped by the mark (L-06).
func TestMarkChangedByTMDBIDs_DoesNotBumpUpdatedAt(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			repo := NewSeriesRepository(db)
			ctx := context.Background()

			id := seedSeriesForMark(t, db, 4001, nil)
			before := readSeriesModel(t, db, id).UpdatedAt

			n, err := repo.MarkChangedByTMDBIDs(ctx, []int64{4001},
				time.Date(2026, 6, 25, 12, 0, 0, 0, time.UTC),
				time.Date(2026, 6, 20, 0, 0, 0, 0, time.UTC))
			require.NoError(t, err)
			require.EqualValues(t, 1, n)

			after := readSeriesModel(t, db, id).UpdatedAt
			assert.WithinDuration(t, before, after, time.Second, "updated_at must be unchanged")
		})
	}
}

// >markChangedBatchSize (500) ids exercise multiple chunks; matched rows in
// different chunks both count toward the returned sum.
func TestMarkChangedByTMDBIDs_ChunkBoundary(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			repo := NewSeriesRepository(db)
			ctx := context.Background()

			boundary := time.Date(2026, 6, 20, 0, 0, 0, 0, time.UTC)
			marked := time.Date(2026, 6, 25, 12, 0, 0, 0, time.UTC)

			// Two real rows: tmdb_id 1 lands in chunk 1 (ids[0:500]),
			// tmdb_id 600 lands in chunk 2 (ids[500:600]).
			idA := seedSeriesForMark(t, db, 1, nil)
			idB := seedSeriesForMark(t, db, 600, nil)

			ids := make([]int64, 0, 600)
			for i := int64(1); i <= 600; i++ {
				ids = append(ids, i)
			}

			n, err := repo.MarkChangedByTMDBIDs(ctx, ids, marked, boundary)
			require.NoError(t, err)
			assert.EqualValues(t, 2, n, "both chunks contribute to the sum")
			assert.WithinDuration(t, marked, *readSeriesModel(t, db, idA).TMDBChangedAt, time.Second)
			assert.WithinDuration(t, marked, *readSeriesModel(t, db, idB).TMDBChangedAt, time.Second)
		})
	}
}

// Cancelled context → error surfaced (error pair for the D-0 quality bar).
func TestMarkChangedByTMDBIDs_CancelledContext(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			repo := NewSeriesRepository(db)

			seedSeriesForMark(t, db, 5001, nil)

			ctx, cancel := context.WithCancel(context.Background())
			cancel()

			_, err := repo.MarkChangedByTMDBIDs(ctx, []int64{5001},
				time.Now().UTC(), time.Now().UTC().Add(-24*time.Hour))
			assert.Error(t, err, "cancelled context must surface an error")
		})
	}
}
