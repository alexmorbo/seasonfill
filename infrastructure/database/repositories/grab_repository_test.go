package repositories

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/application/ports"
	"github.com/alexmorbo/seasonfill/domain/grab"
	"github.com/alexmorbo/seasonfill/infrastructure/database"
)

func newGrabRecord(t *testing.T) grab.Record {
	t.Helper()
	now := time.Now().UTC().Truncate(time.Second)
	return grab.Record{
		ID:           uuid.New(),
		InstanceName: "main",
		SeriesID:     122,
		SeriesTitle:  "Hijack",
		SeasonNumber: 2,
		ReleaseGUID:  "g1",
		ReleaseTitle: "Hijack.S02.PACK",
		DownloadID:   "ABC123",
		IndexerID:    3,
		IndexerName:  "RT",
		Status:       grab.StatusGrabbed,
		ScanRunID:    uuid.New(),
		Attempts:     1,
		CreatedAt:    now,
		UpdatedAt:    now,
	}
}

func TestGrabRepository_Create_Success(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
	repo := NewGrabRepository(db)
	rec := newGrabRecord(t)
	require.NoError(t, repo.Create(context.Background(), rec))

	var m database.GrabRecordModel
	require.NoError(t, db.First(&m, "id = ?", rec.ID.String()).Error)
	assert.Equal(t, "grabbed", m.Status)
	assert.Equal(t, "ABC123", m.DownloadID)
}

func TestGrabRepository_Create_ClosedDB_ReturnsError(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
	repo := NewGrabRepository(db)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	require.NoError(t, sqlDB.Close())

	err = repo.Create(context.Background(), grab.Record{
		ID: uuid.New(), Status: grab.StatusGrabbed,
		CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
		ScanRunID: uuid.New(),
	})
	require.Error(t, err)
}

func TestGrabRepository_MatchLatest_ByDownloadID_Found(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
	repo := NewGrabRepository(db)
	ctx := context.Background()
	rec := newGrabRecord(t)
	require.NoError(t, repo.Create(ctx, rec))

	got, err := repo.MatchLatest(ctx, ports.MatchKey{
		DownloadID: rec.DownloadID, InstanceName: rec.InstanceName,
	})
	require.NoError(t, err)
	assert.Equal(t, rec.ID, got.ID)
}

func TestGrabRepository_MatchLatest_TerminalRowExcluded(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
	repo := NewGrabRepository(db)
	ctx := context.Background()
	rec := newGrabRecord(t)
	rec.Status = grab.StatusImported
	require.NoError(t, repo.Create(ctx, rec))

	_, err := repo.MatchLatest(ctx, ports.MatchKey{
		DownloadID: rec.DownloadID, InstanceName: rec.InstanceName,
	})
	require.ErrorIs(t, err, ports.ErrNotFound)
}

func TestGrabRepository_MatchLatest_FallbackByTuple_Found(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
	repo := NewGrabRepository(db)
	ctx := context.Background()
	rec := newGrabRecord(t)
	rec.DownloadID = ""
	require.NoError(t, repo.Create(ctx, rec))

	got, err := repo.MatchLatest(ctx, ports.MatchKey{
		ReleaseTitle: rec.ReleaseTitle,
		SeriesID:     rec.SeriesID,
		SeasonNumber: rec.SeasonNumber,
		InstanceName: rec.InstanceName,
	})
	require.NoError(t, err)
	assert.Equal(t, rec.ID, got.ID)
}

func TestGrabRepository_MatchLatest_DownloadIDMisses_FallsThroughToTuple(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
	repo := NewGrabRepository(db)
	ctx := context.Background()
	rec := newGrabRecord(t)
	rec.DownloadID = ""
	require.NoError(t, repo.Create(ctx, rec))

	got, err := repo.MatchLatest(ctx, ports.MatchKey{
		DownloadID:   "UNKNOWN",
		ReleaseTitle: rec.ReleaseTitle,
		SeriesID:     rec.SeriesID,
		SeasonNumber: rec.SeasonNumber,
		InstanceName: rec.InstanceName,
	})
	require.NoError(t, err)
	assert.Equal(t, rec.ID, got.ID)
}

func TestGrabRepository_MatchLatest_NoMatch_ReturnsErrNotFound(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
	repo := NewGrabRepository(db)
	_, err := repo.MatchLatest(context.Background(), ports.MatchKey{
		DownloadID: "missing", InstanceName: "main",
	})
	require.ErrorIs(t, err, ports.ErrNotFound)
}

func TestGrabRepository_UpdateStatus_Success_WithMessage(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
	repo := NewGrabRepository(db)
	ctx := context.Background()
	rec := newGrabRecord(t)
	require.NoError(t, repo.Create(ctx, rec))

	require.NoError(t, repo.UpdateStatus(ctx, rec.ID, grab.StatusImportFailed, "bad file"))

	var got database.GrabRecordModel
	require.NoError(t, db.First(&got, "id = ?", rec.ID.String()).Error)
	assert.Equal(t, "import_failed", got.Status)
	assert.Equal(t, "bad file", got.ErrorMessage)
}

func TestGrabRepository_UpdateStatus_UnknownID_ErrNotFound(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
	repo := NewGrabRepository(db)
	err := repo.UpdateStatus(context.Background(), uuid.New(), grab.StatusImported, "")
	require.ErrorIs(t, err, ports.ErrNotFound)
}

func TestGrabRepository_UpdateStatus_TerminalSource_Rejects(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
	repo := NewGrabRepository(db)
	ctx := context.Background()
	rec := newGrabRecord(t)
	rec.Status = grab.StatusImported
	require.NoError(t, repo.Create(ctx, rec))

	err := repo.UpdateStatus(ctx, rec.ID, grab.StatusImportFailed, "")
	require.Error(t, err)
	assert.True(t, errors.Is(err, grab.ErrInvalidStatusTransition))
}
