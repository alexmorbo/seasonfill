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
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
	db := setupTestDB(t)
	r := NewQbitTorrentsRepository(db)
	ctx := context.Background()
	got, err := r.FindByHashes(ctx, "alpha", nil)
	require.NoError(t, err)
	assert.Empty(t, got)
}

// TestQbitTorrentsRepository_SeasonNumber_RoundTrip covers Story 308:
// migration 000039 added qbit_torrents.season_number; modelFromEntry
// carries it from torrentsync.Entry → model; entryFromModel restores
// it; the DoUpdate column list includes it so Upsert overwrites the
// stored value on every refresh.
func TestQbitTorrentsRepository_SeasonNumber_RoundTrip(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
	r := NewQbitTorrentsRepository(db)
	ctx := context.Background()

	// Insert a row with SeasonNumber=ptrInt(5).
	five := 5
	e := mkEntry("aaaa", "Show.S05E07.1080p.WEB-DL", qbit.StateGroupDownloading)
	e.Info.SeasonNumber = &five
	require.NoError(t, r.Upsert(ctx, "alpha", e))

	got, err := r.List(ctx, "alpha")
	require.NoError(t, err)
	require.Len(t, got, 1)
	require.NotNil(t, got[0].Info.SeasonNumber)
	assert.Equal(t, 5, *got[0].Info.SeasonNumber)

	// Re-upsert with SeasonNumber=nil — the DoUpdate column list
	// includes "season_number" so the row stays nil after this.
	e2 := mkEntry("aaaa", "Show.Complete.Series.PACK.1080p", qbit.StateGroupDownloading)
	e2.Info.SeasonNumber = nil
	require.NoError(t, r.Upsert(ctx, "alpha", e2))

	got, err = r.List(ctx, "alpha")
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Nil(t, got[0].Info.SeasonNumber, "nil overrides previous non-nil via DoUpdate column list")
}

// TestQbitTorrentsRepository_SeasonNumber_BatchUpsertSurvivesAcrossRows
// asserts BatchUpsert correctly persists distinct season values
// across rows in the same transaction — the column is in the
// BatchUpsert DoUpdate list, not just Upsert.
func TestQbitTorrentsRepository_SeasonNumber_BatchUpsertSurvivesAcrossRows(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
	r := NewQbitTorrentsRepository(db)
	ctx := context.Background()

	two, three := 2, 3
	a := mkEntry("aaaa", "Show.S02E01", qbit.StateGroupSeeding)
	a.Info.SeasonNumber = &two
	b := mkEntry("bbbb", "Show.S03E01", qbit.StateGroupSeeding)
	b.Info.SeasonNumber = &three
	c := mkEntry("cccc", "Show.PACK", qbit.StateGroupSeeding) // nil season

	require.NoError(t, r.BatchUpsert(ctx, "alpha", []torrentsync.Entry{a, b, c}, time.Now().UTC()))
	rows, err := r.List(ctx, "alpha")
	require.NoError(t, err)
	require.Len(t, rows, 3)

	got := map[string]*int{}
	for _, row := range rows {
		got[row.Info.Hash] = row.Info.SeasonNumber
	}
	require.NotNil(t, got["aaaa"])
	assert.Equal(t, 2, *got["aaaa"])
	require.NotNil(t, got["bbbb"])
	assert.Equal(t, 3, *got["bbbb"])
	assert.Nil(t, got["cccc"])
}
