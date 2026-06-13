package repositories

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/application/torrentsync"
	"github.com/alexmorbo/seasonfill/infrastructure/qbit"
)

func mkEntry(hash, name string, group qbit.StateGroup) torrentsync.Entry {
	return torrentsync.Entry{
		Info: qbit.TorrentInfo{
			Hash: hash, Name: name,
			StateRaw: "downloading", StateGroup: group,
			Size: 1 << 20, Uploaded: 0,
		},
		StateGroup: group,
		SyncedAt:   time.Date(2026, 6, 13, 12, 0, 0, 0, time.UTC),
	}
}

func TestQbitTorrentsRepository_Upsert(t *testing.T) {
	db := setupTestDB(t)
	r := NewQbitTorrentsRepository(db)
	ctx := context.Background()

	require.NoError(t, r.Upsert(ctx, "alpha", mkEntry("aaaa", "show", qbit.StateGroupDownloading)))
	rows, err := r.List(ctx, "alpha")
	require.NoError(t, err)
	require.Len(t, rows, 1)
	assert.Equal(t, "show", rows[0].Info.Name)
}

func TestQbitTorrentsRepository_MarkAbsent(t *testing.T) {
	db := setupTestDB(t)
	r := NewQbitTorrentsRepository(db)
	ctx := context.Background()

	require.NoError(t, r.Upsert(ctx, "alpha", mkEntry("aaaa", "show", qbit.StateGroupSeeding)))
	require.NoError(t, r.MarkAbsent(ctx, "alpha", "aaaa", time.Now().UTC()))
	rows, err := r.List(ctx, "alpha")
	require.NoError(t, err)
	assert.Empty(t, rows, "present=false rows must NOT appear in List")
}

func TestQbitTorrentsRepository_BatchUpsertSingleTx(t *testing.T) {
	db := setupTestDB(t)
	r := NewQbitTorrentsRepository(db)
	ctx := context.Background()

	entries := []torrentsync.Entry{
		mkEntry("a", "x", qbit.StateGroupSeeding),
		mkEntry("b", "y", qbit.StateGroupSeeding),
		mkEntry("c", "z", qbit.StateGroupSeeding),
	}
	require.NoError(t, r.BatchUpsert(ctx, "alpha", entries, time.Now().UTC()))
	rows, err := r.List(ctx, "alpha")
	require.NoError(t, err)
	assert.Len(t, rows, 3)
}
