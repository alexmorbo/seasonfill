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
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
)

// seedScans inserts n scan rows for (instance, status) with a deterministic
// started_at series (base, base+1s, ...). ScanRepository.List orders,
// filters and paginates by started_at (created_at is unreliable — it was
// historically zeroed by the completion Save), so distinct StartedAt values
// are all that is needed to drive a known order.
func seedScans(t *testing.T, db *gorm.DB, n int, instance domain.InstanceName, status string, base time.Time) []ports.ScanRecord {
	t.Helper()
	repo := NewScanRepository(db)
	ctx := context.Background()
	recs := make([]ports.ScanRecord, 0, n)
	for i := range n {
		rec := ports.ScanRecord{
			ID:           uuid.New(),
			InstanceName: instance,
			Trigger:      "manual",
			StartedAt:    base.Add(time.Duration(i) * time.Second),
			Status:       status,
			DryRun:       true,
		}
		require.NoError(t, repo.Create(ctx, rec))
		recs = append(recs, rec)
	}
	return recs
}

func TestScanRepository_List_Empty(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
	repo := NewScanRepository(db)
	got, next, err := repo.List(context.Background(), ports.ScanFilter{}, ports.Pagination{Limit: 10})
	require.NoError(t, err)
	assert.Empty(t, got)
	assert.Nil(t, next)
}

// TestScanRepository_List_OrdersByStartedAt_ZeroCreatedAt is the exact
// production regression: every scan_runs.created_at is the Go zero value
// (0001-01-01) because the completion Save clobbered it, yet started_at is
// populated and distinct. The list must come back strictly started_at-DESC
// regardless of created_at. We force created_at to zero on every row (and
// shuffle which row gets the newest started_at vs the smallest id) so the
// only signal that can produce a correct order is started_at.
func TestScanRepository_List_OrdersByStartedAt_ZeroCreatedAt(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
	repo := NewScanRepository(db)
	ctx := context.Background()

	base := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	// Insert rows whose started_at order is intentionally NOT the id order.
	offsets := []int{40, 10, 30, 0, 20}
	for _, off := range offsets {
		rec := ports.ScanRecord{
			ID:           uuid.New(),
			InstanceName: "main",
			Trigger:      "manual",
			StartedAt:    base.Add(time.Duration(off) * time.Second),
			Status:       "completed",
			DryRun:       true,
		}
		require.NoError(t, repo.Create(ctx, rec))
	}
	// Zero out created_at for every row — reproduces the prod corruption.
	require.NoError(t, db.Table("scan_runs").
		Where("1 = 1").Update("created_at", time.Time{}).Error)

	got, _, err := repo.List(ctx, ports.ScanFilter{}, ports.Pagination{Limit: 10})
	require.NoError(t, err)
	require.Len(t, got, 5)

	for i := 0; i < len(got)-1; i++ {
		assert.True(t, got[i].StartedAt.After(got[i+1].StartedAt),
			"row %d started_at %s must be after row %d started_at %s",
			i, got[i].StartedAt, i+1, got[i+1].StartedAt)
	}
	// Sanity: every created_at really is zero, so the order can only have
	// come from started_at.
	for _, r := range got {
		assert.True(t, r.CreatedAt.IsZero(), "created_at must be zero in this scenario")
	}
}

func TestScanRepository_List_FirstPage(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
	base := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	seedScans(t, db, 5, "main", "completed", base)

	repo := NewScanRepository(db)
	got, next, err := repo.List(context.Background(), ports.ScanFilter{}, ports.Pagination{Limit: 3})
	require.NoError(t, err)
	require.Len(t, got, 3)
	require.NotNil(t, next)
	// DESC ordering — newer first.
	assert.True(t, got[0].StartedAt.After(got[1].StartedAt))
	assert.True(t, got[1].StartedAt.After(got[2].StartedAt))
}

func TestScanRepository_List_SecondPageViaCursor(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
	base := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	seedScans(t, db, 5, "main", "completed", base)

	repo := NewScanRepository(db)
	ctx := context.Background()
	first, next, err := repo.List(ctx, ports.ScanFilter{}, ports.Pagination{Limit: 3})
	require.NoError(t, err)
	require.Len(t, first, 3)
	require.NotNil(t, next)

	second, next2, err := repo.List(ctx, ports.ScanFilter{}, ports.Pagination{Limit: 3, Cursor: next})
	require.NoError(t, err)
	require.Len(t, second, 2)
	assert.Nil(t, next2)

	seen := map[string]bool{}
	for _, r := range append(first, second...) {
		assert.False(t, seen[r.ID.String()], "dup %s", r.ID)
		seen[r.ID.String()] = true
	}
	assert.Len(t, seen, 5)
}

func TestScanRepository_List_InstanceFilter(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
	base := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	seedScans(t, db, 3, "main", "completed", base)
	seedScans(t, db, 2, "secondary", "completed", base.Add(time.Hour))

	repo := NewScanRepository(db)
	want := domain.InstanceName("secondary")
	got, _, err := repo.List(context.Background(), ports.ScanFilter{Instance: &want}, ports.Pagination{Limit: 10})
	require.NoError(t, err)
	require.Len(t, got, 2)
	for _, r := range got {
		assert.Equal(t, domain.InstanceName("secondary"), r.InstanceName)
	}
}

func TestScanRepository_List_StatusFilter(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
	base := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	seedScans(t, db, 2, "main", "completed", base)
	seedScans(t, db, 3, "main", "failed", base.Add(time.Hour))

	repo := NewScanRepository(db)
	want := "failed"
	got, _, err := repo.List(context.Background(), ports.ScanFilter{Status: &want}, ports.Pagination{Limit: 10})
	require.NoError(t, err)
	require.Len(t, got, 3)
	for _, r := range got {
		assert.Equal(t, "failed", r.Status)
	}
}

func TestScanRepository_List_TimeRange(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
	base := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	seedScans(t, db, 6, "main", "completed", base)

	repo := NewScanRepository(db)
	from := base.Add(2 * time.Second)
	to := base.Add(5 * time.Second) // exclusive
	got, _, err := repo.List(context.Background(), ports.ScanFilter{From: &from, To: &to}, ports.Pagination{Limit: 10})
	require.NoError(t, err)
	// Rows at +2s, +3s, +4s match.
	assert.Len(t, got, 3)
}

func TestScanRepository_List_LimitDefensive(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
	repo := NewScanRepository(db)
	ctx := context.Background()

	for _, lim := range []int{0, -1, ports.MaxListLimit + 1} {
		_, _, err := repo.List(ctx, ports.ScanFilter{}, ports.Pagination{Limit: lim})
		require.Error(t, err, "limit=%d", lim)
		assert.True(t, errors.Is(err, ports.ErrInvalidLimit), "limit=%d", lim)
	}

	// Ceiling itself is accepted.
	_, _, err := repo.List(ctx, ports.ScanFilter{}, ports.Pagination{Limit: ports.MaxListLimit})
	require.NoError(t, err)
}
