package repositories

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/domain/grab"
	"github.com/alexmorbo/seasonfill/infrastructure/database"
)

func TestGrabRepository_Create_Success(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
	repo := NewGrabRepository(db)
	rec := grab.Record{
		ID:           uuid.New(),
		InstanceName: "main",
		SeriesID:     122,
		SeriesTitle:  "Hijack",
		SeasonNumber: 2,
		ReleaseGUID:  "g1",
		ReleaseTitle: "Pack",
		IndexerID:    3,
		IndexerName:  "RT",
		Status:       grab.StatusGrabbed,
		ScanRunID:    uuid.New(),
		Attempts:     1,
		CreatedAt:    time.Now().UTC().Truncate(time.Second),
		UpdatedAt:    time.Now().UTC().Truncate(time.Second),
	}
	require.NoError(t, repo.Create(context.Background(), rec))

	var model database.GrabRecordModel
	require.NoError(t, db.First(&model, "id = ?", rec.ID.String()).Error)
	assert.Equal(t, "main", model.InstanceName)
	assert.Equal(t, "grabbed", model.Status)
	assert.Equal(t, 1, model.Attempts)
}

func TestGrabRepository_Create_ClosedDB_ReturnsError(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
	repo := NewGrabRepository(db)

	sqlDB, err := db.DB()
	require.NoError(t, err)
	require.NoError(t, sqlDB.Close())

	err = repo.Create(context.Background(), grab.Record{
		ID:        uuid.New(),
		Status:    grab.StatusGrabbed,
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
		ScanRunID: uuid.New(),
	})
	require.Error(t, err)
}
