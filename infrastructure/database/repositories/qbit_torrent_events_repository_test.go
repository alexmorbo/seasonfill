package repositories

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/application/torrentsync"
	"github.com/alexmorbo/seasonfill/infrastructure/database"
	"github.com/alexmorbo/seasonfill/infrastructure/qbit"
)

func TestQbitTorrentEventsRepository_InsertStateChange(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
	r := NewQbitTorrentEventsRepository(db)
	ctx := context.Background()

	row := torrentsync.EventRow{
		Instance: "alpha", Hash: "aaaa",
		Event: torrentsync.EventStateChange,
		From:  qbit.StateGroupDownloading,
		To:    qbit.StateGroupSeeding,
		At:    time.Date(2026, 6, 13, 12, 0, 0, 0, time.UTC),
	}
	require.NoError(t, r.Insert(ctx, row))

	var got database.QbitTorrentEventModel
	require.NoError(t, db.First(&got).Error)
	assert.Equal(t, "state_change", got.Event)
	require.NotNil(t, got.FromGroup)
	assert.Equal(t, "downloading", *got.FromGroup)
	require.NotNil(t, got.ToGroup)
	assert.Equal(t, "seeding", *got.ToGroup)
}

func TestQbitTorrentEventsRepository_InsertDeletedHasNilToGroup(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
	r := NewQbitTorrentEventsRepository(db)
	ctx := context.Background()

	require.NoError(t, r.Insert(ctx, torrentsync.EventRow{
		Instance: "alpha", Hash: "aaaa",
		Event: torrentsync.EventDeleted,
		At:    time.Now().UTC(),
	}))
	var got database.QbitTorrentEventModel
	require.NoError(t, db.First(&got).Error)
	assert.Equal(t, "deleted", got.Event)
	assert.Nil(t, got.ToGroup, "deleted events leave to_group null")
}
