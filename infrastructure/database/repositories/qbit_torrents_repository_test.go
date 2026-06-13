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

func TestQbitTorrentsRepository_FindByHashes(t *testing.T) {
	db := setupTestDB(t)
	r := NewQbitTorrentsRepository(db)
	ctx := context.Background()

	// Seed three rows — two stay present, one marked absent.
	for _, e := range []torrentsync.Entry{
		mkEntry("aaaa", "a", qbit.StateGroupSeeding),
		mkEntry("bbbb", "b", qbit.StateGroupSeeding),
		mkEntry("cccc", "c", qbit.StateGroupSeeding),
	} {
		require.NoError(t, r.Upsert(ctx, "alpha", e))
	}
	require.NoError(t, r.MarkAbsent(ctx, "alpha", "cccc", time.Now().UTC()))

	// FindByHashes must surface present=false rows too — that is
	// the point of the story 222 read fallback.
	got, err := r.FindByHashes(ctx, "alpha", []string{"aaaa", "bbbb", "cccc", "zzzz"})
	require.NoError(t, err)
	require.Len(t, got, 3, "absent row must still be returned by FindByHashes")
}

func TestQbitTorrentsRepository_FindByHashes_EmptyInput(t *testing.T) {
	db := setupTestDB(t)
	r := NewQbitTorrentsRepository(db)
	ctx := context.Background()
	got, err := r.FindByHashes(ctx, "alpha", nil)
	require.NoError(t, err)
	assert.Empty(t, got)
}
