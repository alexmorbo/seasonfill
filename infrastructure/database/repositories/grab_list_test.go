package repositories

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"github.com/alexmorbo/seasonfill/application/ports"
	"github.com/alexmorbo/seasonfill/domain/grab"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
	"github.com/alexmorbo/seasonfill/internal/shared/testhelpers"
)

func seedGrab(t *testing.T, db *gorm.DB, instance domain.InstanceName, seriesID domain.SonarrSeriesID, season int, status grab.Status, createdAt time.Time) grab.Record {
	t.Helper()
	rec := grab.Record{
		ID:           uuid.New(),
		InstanceName: instance,
		SeriesID:     seriesID,
		SeriesTitle:  "Hijack",
		SeasonNumber: season,
		ReleaseGUID:  uuid.NewString(),
		ReleaseTitle: "S02 Pack",
		IndexerID:    3,
		IndexerName:  "RT",
		Status:       status,
		ScanRunID:    uuid.New(),
		Attempts:     1,
		CreatedAt:    createdAt,
		UpdatedAt:    createdAt,
	}
	require.NoError(t, NewGrabRepository(db).Create(context.Background(), rec))
	return rec
}

func TestGrabRepository_List_Empty(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			got, next, err := NewGrabRepository(db).List(context.Background(), ports.GrabFilter{}, ports.Pagination{Limit: 10})
			require.NoError(t, err)
			assert.Empty(t, got)
			assert.Nil(t, next)
		})
	}
}

func TestGrabRepository_List_FirstAndSecondPage(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			base := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
			for i := range 5 {
				seedGrab(t, db, "main", domain.SonarrSeriesID(100+i), 1, grab.StatusGrabbed, base.Add(time.Duration(i)*time.Second))
			}

			repo := NewGrabRepository(db)
			ctx := context.Background()
			first, next, err := repo.List(ctx, ports.GrabFilter{}, ports.Pagination{Limit: 3})
			require.NoError(t, err)
			require.Len(t, first, 3)
			require.NotNil(t, next)

			second, next2, err := repo.List(ctx, ports.GrabFilter{}, ports.Pagination{Limit: 3, Cursor: next})
			require.NoError(t, err)
			require.Len(t, second, 2)
			assert.Nil(t, next2)

			seen := map[string]bool{}
			for _, r := range append(first, second...) {
				assert.False(t, seen[r.ID.String()])
				seen[r.ID.String()] = true
			}
			assert.Len(t, seen, 5)
		})
	}
}

func TestGrabRepository_List_InstanceAndSeriesFilter(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			base := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
			seedGrab(t, db, "main", 100, 1, grab.StatusGrabbed, base)
			seedGrab(t, db, "main", 100, 2, grab.StatusGrabbed, base.Add(time.Second))
			seedGrab(t, db, "main", 200, 1, grab.StatusGrabbed, base.Add(2*time.Second))
			seedGrab(t, db, "secondary", 100, 1, grab.StatusGrabbed, base.Add(3*time.Second))

			inst := domain.InstanceName("main")
			sid := domain.SonarrSeriesID(100)
			got, _, err := NewGrabRepository(db).List(context.Background(),
				ports.GrabFilter{Instance: &inst, SeriesID: &sid}, ports.Pagination{Limit: 10})
			require.NoError(t, err)
			require.Len(t, got, 2)
			for _, r := range got {
				assert.Equal(t, domain.InstanceName("main"), r.InstanceName)
				assert.Equal(t, domain.SonarrSeriesID(100), r.SeriesID)
			}
		})
	}
}

func TestGrabRepository_List_StatusFilter(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			base := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
			seedGrab(t, db, "main", 100, 1, grab.StatusGrabbed, base)
			seedGrab(t, db, "main", 101, 1, grab.StatusGrabFailed, base.Add(time.Second))
			seedGrab(t, db, "main", 102, 1, grab.StatusGrabFailed, base.Add(2*time.Second))

			want := "grab_failed"
			got, _, err := NewGrabRepository(db).List(context.Background(),
				ports.GrabFilter{Status: &want}, ports.Pagination{Limit: 10})
			require.NoError(t, err)
			require.Len(t, got, 2)
			for _, r := range got {
				assert.Equal(t, grab.StatusGrabFailed, r.Status)
			}
		})
	}
}

func TestGrabRepository_List_TimeRange(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			base := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
			for i := range 6 {
				seedGrab(t, db, "main", domain.SonarrSeriesID(100+i), 1, grab.StatusGrabbed, base.Add(time.Duration(i)*time.Second))
			}

			from := base.Add(2 * time.Second)
			to := base.Add(5 * time.Second)
			got, _, err := NewGrabRepository(db).List(context.Background(),
				ports.GrabFilter{From: &from, To: &to}, ports.Pagination{Limit: 10})
			require.NoError(t, err)
			assert.Len(t, got, 3)
		})
	}
}

func TestGrabRepository_List_LimitDefensive(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			repo := NewGrabRepository(db)
			for _, lim := range []int{0, -1, ports.MaxListLimit + 1} {
				_, _, err := repo.List(context.Background(), ports.GrabFilter{}, ports.Pagination{Limit: lim})
				require.Error(t, err)
				assert.True(t, errors.Is(err, ports.ErrInvalidLimit))
			}
		})
	}
}
