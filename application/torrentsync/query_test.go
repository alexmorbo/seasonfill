package torrentsync

import (
	"context"
	"errors"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/infrastructure/qbit"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
)

// fakeLookup is the test stub for LookupRepo.
type fakeLookup struct {
	mu   sync.Mutex
	rows map[string][]string // key = "instance|seriesID"
	err  error
}

func (f *fakeLookup) HashesForSeries(_ context.Context, instance domain.InstanceName, seriesID int) ([]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return nil, f.err
	}
	return f.rows[lookupKey(instance, seriesID)], nil
}

func lookupKey(instance domain.InstanceName, seriesID int) string {
	return string(instance) + "|" + strconv.Itoa(seriesID)
}

// fakeTorrentsRepoWithFind extends fakeTorrentsRepo (from
// persist_test.go) with FindByHashes — kept inline rather than
// muddying the persist suite.
type fakeTorrentsRepoWithFind struct {
	fakeTorrentsRepo
	byHash map[string]Entry
}

func (f *fakeTorrentsRepoWithFind) FindByHashes(_ context.Context, _ domain.InstanceName, hashes []string) ([]Entry, error) {
	out := make([]Entry, 0, len(hashes))
	for _, h := range hashes {
		if e, ok := f.byHash[h]; ok {
			out = append(out, e)
		}
	}
	return out, nil
}

func liveInfo(hash, name string, addedOn time.Time) qbit.TorrentInfo {
	return qbit.TorrentInfo{
		Hash:       hash,
		Name:       name,
		StateRaw:   "uploading",
		StateGroup: qbit.StateGroupSeeding,
		Size:       1 << 30,
		DlSpeed:    100,
		UpSpeed:    200,
		Progress:   1.0,
		AddedOn:    addedOn,
	}
}

func TestQuery_BySeriesID_LiveAndDeadMerged(t *testing.T) {
	t.Parallel()
	store := NewStore()
	store.EnsureInstance("alpha")

	// Two torrents mapped to series 42; only one is live.
	addedNewer := time.Date(2026, 6, 13, 12, 0, 0, 0, time.UTC)
	addedOlder := time.Date(2026, 6, 10, 9, 0, 0, 0, time.UTC)

	liveEntry := Entry{
		Info:       liveInfo("aaaa", "live show", addedNewer),
		StateGroup: qbit.StateGroupSeeding,
		SyncedAt:   addedNewer.Add(time.Hour),
	}
	store.Put("alpha", liveEntry)
	store.SetSeriesMapping("alpha", "aaaa", 42)

	// "bbbb" is in torrent_series_map but NOT in the store
	// (qBit unreachable / deleted) — DB fallback path.
	repo := &fakeTorrentsRepoWithFind{
		byHash: map[string]Entry{
			"bbbb": {
				Info: qbit.TorrentInfo{
					Hash:       "bbbb",
					Name:       "dead show",
					StateRaw:   "stoppedUP",
					StateGroup: qbit.StateGroupPaused,
					AddedOn:    addedOlder,
					DlSpeed:    9999, // MUST be zeroed by the query
					UpSpeed:    9999,
				},
				StateGroup: qbit.StateGroupPaused,
				SyncedAt:   addedOlder.Add(time.Hour),
			},
		},
	}
	lookup := &fakeLookup{
		rows: map[string][]string{
			lookupKey("alpha", 42): {"aaaa", "bbbb"},
		},
	}
	q := NewQuery(store, repo, lookup).
		WithClock(func() time.Time { return time.Date(2026, 6, 13, 13, 0, 0, 0, time.UTC) })

	result, err := q.BySeriesID(context.Background(), "alpha", 42)
	require.NoError(t, err)
	require.Len(t, result.Rows, 2)
	// Sort order — newer first.
	assert.Equal(t, "aaaa", result.Rows[0].Entry.Info.Hash)
	assert.True(t, result.Rows[0].Live)
	assert.Equal(t, "bbbb", result.Rows[1].Entry.Info.Hash)
	assert.False(t, result.Rows[1].Live)
	// Live cells zeroed on dead row.
	assert.EqualValues(t, 0, result.Rows[1].Entry.Info.DlSpeed)
	assert.EqualValues(t, 0, result.Rows[1].Entry.Info.UpSpeed)
	// Counts.
	assert.Equal(t, 1, result.LiveCount)
}

func TestQuery_BySeriesID_EmptyWhenNoMapping(t *testing.T) {
	t.Parallel()
	store := NewStore()
	store.EnsureInstance("alpha")
	repo := &fakeTorrentsRepoWithFind{}
	lookup := &fakeLookup{}
	q := NewQuery(store, repo, lookup)

	result, err := q.BySeriesID(context.Background(), "alpha", 999)
	require.NoError(t, err)
	assert.Empty(t, result.Rows)
	assert.Equal(t, 0, result.LiveCount)
}

func TestQuery_BySeriesID_SortByAddedOnDesc(t *testing.T) {
	t.Parallel()
	store := NewStore()
	store.EnsureInstance("alpha")
	now := time.Date(2026, 6, 13, 12, 0, 0, 0, time.UTC)
	for i, h := range []string{"a", "b", "c"} {
		store.Put("alpha", Entry{
			Info:       liveInfo(h, h, now.Add(-time.Duration(i)*time.Hour)),
			StateGroup: qbit.StateGroupSeeding,
			SyncedAt:   now,
		})
		store.SetSeriesMapping("alpha", h, 42)
	}
	repo := &fakeTorrentsRepoWithFind{}
	lookup := &fakeLookup{rows: map[string][]string{
		lookupKey("alpha", 42): {"a", "b", "c"},
	}}
	q := NewQuery(store, repo, lookup)
	result, err := q.BySeriesID(context.Background(), "alpha", 42)
	require.NoError(t, err)
	require.Len(t, result.Rows, 3)
	assert.Equal(t, "a", result.Rows[0].Entry.Info.Hash)
	assert.Equal(t, "b", result.Rows[1].Entry.Info.Hash)
	assert.Equal(t, "c", result.Rows[2].Entry.Info.Hash)
}

func TestQuery_BySeriesID_LookupErrorBubbles(t *testing.T) {
	t.Parallel()
	store := NewStore()
	store.EnsureInstance("alpha")
	repo := &fakeTorrentsRepoWithFind{}
	lookup := &fakeLookup{err: errors.New("db dead")}
	q := NewQuery(store, repo, lookup)
	_, err := q.BySeriesID(context.Background(), "alpha", 42)
	require.Error(t, err)
}
