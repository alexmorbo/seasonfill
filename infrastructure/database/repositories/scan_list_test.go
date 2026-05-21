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
)

// seedScans inserts n scan rows for (instance, status) and forces
// created_at to a deterministic series (base, base+1s, ...). The override
// is required because ScanRunModel.CreatedAt is GORM-managed (auto-set
// on Create) and we want the cursor pagination to walk a known order.
func seedScans(t *testing.T, db *gorm.DB, n int, instance, status string, base time.Time) []ports.ScanRecord {
	t.Helper()
	repo := NewScanRepository(db)
	ctx := context.Background()
	recs := make([]ports.ScanRecord, 0, n)
	for i := 0; i < n; i++ {
		rec := ports.ScanRecord{
			ID:           uuid.New(),
			InstanceName: instance,
			Trigger:      "manual",
			StartedAt:    base.Add(time.Duration(i) * time.Second),
			Status:       status,
			DryRun:       true,
		}
		require.NoError(t, repo.Create(ctx, rec))
		ts := base.Add(time.Duration(i) * time.Second)
		require.NoError(t, db.Table("scan_runs").Where("id = ?", rec.ID.String()).Update("created_at", ts).Error)
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
	want := "secondary"
	got, _, err := repo.List(context.Background(), ports.ScanFilter{Instance: &want}, ports.Pagination{Limit: 10})
	require.NoError(t, err)
	require.Len(t, got, 2)
	for _, r := range got {
		assert.Equal(t, "secondary", r.InstanceName)
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
