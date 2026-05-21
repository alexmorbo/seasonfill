package repositories

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"github.com/alexmorbo/seasonfill/application/ports"
	"github.com/alexmorbo/seasonfill/infrastructure/database"
)

func setupTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, database.Migrate(db))
	return db
}

func newScanRecord(id uuid.UUID) ports.ScanRecord {
	now := time.Now().UTC().Truncate(time.Second)
	return ports.ScanRecord{
		ID:           id,
		InstanceName: "main",
		Trigger:      "manual",
		StartedAt:    now,
		Status:       "running",
		DryRun:       true,
	}
}

func TestScanRepository_Create_Then_GetByID(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
	repo := NewScanRepository(db)
	ctx := context.Background()

	id := uuid.New()
	rec := newScanRecord(id)
	require.NoError(t, repo.Create(ctx, rec))

	got, err := repo.GetByID(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, id, got.ID)
	assert.Equal(t, "main", got.InstanceName)
	assert.Equal(t, "running", got.Status)
	assert.True(t, got.DryRun)
}

func TestScanRepository_GetByID_NotFound(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
	repo := NewScanRepository(db)

	_, err := repo.GetByID(context.Background(), uuid.New())
	require.Error(t, err)
	assert.True(t, errors.Is(err, ports.ErrNotFound))
}

func TestScanRepository_Update(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
	repo := NewScanRepository(db)
	ctx := context.Background()

	id := uuid.New()
	rec := newScanRecord(id)
	require.NoError(t, repo.Create(ctx, rec))

	finished := time.Now().UTC().Truncate(time.Second)
	rec.Status = "completed"
	rec.SeriesScanned = 12
	rec.CandidatesFound = 5
	rec.FinishedAt = &finished

	require.NoError(t, repo.Update(ctx, rec))

	got, err := repo.GetByID(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, "completed", got.Status)
	assert.Equal(t, 12, got.SeriesScanned)
	assert.Equal(t, 5, got.CandidatesFound)
	require.NotNil(t, got.FinishedAt)
}

func TestScanRepository_MarkAborted(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
	repo := NewScanRepository(db)
	ctx := context.Background()

	id := uuid.New()
	rec := newScanRecord(id)
	require.NoError(t, repo.Create(ctx, rec))

	require.NoError(t, repo.MarkAborted(ctx, id, "shutdown timeout"))

	got, err := repo.GetByID(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, "aborted", got.Status)
	assert.Equal(t, "shutdown timeout", got.ErrorMessage)
}

func TestScanRepository_MarkAborted_UnknownID_Succeeds(t *testing.T) {
	t.Parallel()
	// GORM Updates with no matching row returns no error and 0 rows affected.
	db := setupTestDB(t)
	repo := NewScanRepository(db)

	err := repo.MarkAborted(context.Background(), uuid.New(), "noop")
	assert.NoError(t, err)
}

func TestScanRepository_Create_ClosedDB_ReturnsError(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	require.NoError(t, sqlDB.Close())

	repo := NewScanRepository(db)
	err = repo.Create(context.Background(), newScanRecord(uuid.New()))
	require.Error(t, err)
}

func TestScanRepository_Update_ClosedDB_ReturnsError(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
	repo := NewScanRepository(db)
	require.NoError(t, repo.Create(context.Background(), newScanRecord(uuid.New())))

	sqlDB, err := db.DB()
	require.NoError(t, err)
	require.NoError(t, sqlDB.Close())

	err = repo.Update(context.Background(), newScanRecord(uuid.New()))
	require.Error(t, err)
}
