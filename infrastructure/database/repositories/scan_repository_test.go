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

// TestScanRepository_TxRollback_OnForcedError is the 008a regression
// canary for D-3.A.3 applied to ScanRepository. It seeds a scan row
// (auto-committed outside the tx), then opens a Transactor.Transaction
// that calls scanRepo.Update inside it, then forces the function to
// return an error. After the tx rolls back, the row must be observable
// in its ORIGINAL pre-tx state — the Update must not have escaped the
// rollback envelope.
//
// With the pre-fix code (Update using r.db.WithContext bypassing
// dbFromContext) the Update would auto-commit on Postgres before the
// surrounding tx rolled back. On SQLite this test would also fail
// because the row's status would reflect the in-tx Update rather than
// the pre-tx state.
func TestScanRepository_TxRollback_OnForcedError(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
	repo := NewScanRepository(db)
	tx := NewGormTransactor(db)
	ctx := context.Background()

	// Seed: one scan in "running" state, persisted outside the tx.
	id := uuid.New()
	original := newScanRecord(id)
	original.Status = "running"
	require.NoError(t, repo.Create(ctx, original))

	// Compose a tx that updates the scan to "completed", then forces
	// the function to return an error so the tx rolls back.
	forced := errors.New("forced tx rollback")
	txErr := tx.Transaction(ctx, func(txCtx context.Context) error {
		modified := original
		modified.Status = "completed"
		modified.SeriesScanned = 99
		if err := repo.Update(txCtx, modified); err != nil {
			return err
		}
		return forced
	})
	require.Error(t, txErr, "transaction must propagate the forced error")
	assert.True(t, errors.Is(txErr, forced), "error must wrap the forced sentinel")

	// Assert the scan row is unchanged — Update was rolled back.
	got, err := repo.GetByID(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, "running", got.Status,
		"Update inside a rolled-back tx must NOT persist — dbFromContext must route the write through the tx session")
	assert.Equal(t, 0, got.SeriesScanned,
		"Update inside a rolled-back tx must NOT persist field changes")
}
