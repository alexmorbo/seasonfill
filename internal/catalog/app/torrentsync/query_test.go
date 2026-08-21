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

	"github.com/alexmorbo/seasonfill/internal/shared/clients/qbit"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
)

// fakeLookup is the test stub for LookupRepo.
type fakeLookup struct {
	mu   sync.Mutex
	rows map[string][]string // key = "instance|seriesID"
	err  error
}

func (f *fakeLookup) HashesForSeries(_ context.Context, instance domain.InstanceName, seriesID domain.SonarrSeriesID) ([]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return nil, f.err
	}
	return f.rows[lookupKey(instance, seriesID)], nil
}

func lookupKey(instance domain.InstanceName, seriesID domain.SonarrSeriesID) string {
	return string(instance) + "|" + strconv.Itoa(int(seriesID))
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

// fakeMovieLookup is the test stub for MovieLookupRepo (ADR-0023 B1.4).
type fakeMovieLookup struct {
	mu      sync.Mutex
	entries map[string][]MovieMapEntry // key = "instance|radarrMovieID"
	err     error
}

func (f *fakeMovieLookup) HashesForMovie(_ context.Context, instance domain.InstanceName, movieID domain.RadarrMovieID) ([]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return nil, f.err
	}
	out := make([]string, 0)
	for _, e := range f.entries[movieLookupKey(instance, movieID)] {
		out = append(out, e.Hash)
	}
	return out, nil
}

func (f *fakeMovieLookup) EntriesForMovie(_ context.Context, instance domain.InstanceName, movieID domain.RadarrMovieID) ([]MovieMapEntry, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return nil, f.err
	}
	return f.entries[movieLookupKey(instance, movieID)], nil
}

func movieLookupKey(instance domain.InstanceName, movieID domain.RadarrMovieID) string {
	return string(instance) + "|" + strconv.Itoa(int(movieID))
}

func TestQuery_ByMovieID(t *testing.T) {
	t.Parallel()

	addedNewer := time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC)
	addedOlder := time.Date(2026, 8, 17, 9, 0, 0, 0, time.UTC)

	t.Run("live rows carry provenance from the bridge", func(t *testing.T) {
		t.Parallel()
		store := NewStore()
		store.EnsureInstance("radarr-main")
		store.Put("radarr-main", Entry{
			Info:       liveInfo("aaaa", "live movie", addedNewer),
			StateGroup: qbit.StateGroupSeeding,
			SyncedAt:   addedNewer.Add(time.Hour),
		})
		store.SetMovieMapping("radarr-main", "aaaa", 42)

		lookup := &fakeMovieLookup{entries: map[string][]MovieMapEntry{
			movieLookupKey("radarr-main", 42): {
				{Hash: "aaaa", Source: MovieMapSourceWebhook, Provenance: MovieProvenanceManualImport},
			},
		}}
		q := NewQuery(store, &fakeTorrentsRepoWithFind{}, nil).WithMovieLookup(lookup)

		result, err := q.ByMovieID(context.Background(), "radarr-main", 42)
		require.NoError(t, err)
		require.Len(t, result.Rows, 1)
		assert.Equal(t, "aaaa", result.Rows[0].Entry.Info.Hash)
		assert.True(t, result.Rows[0].Live)
		assert.True(t, result.Rows[0].Present)
		assert.Equal(t, string(MovieProvenanceManualImport), result.Rows[0].Provenance)
		assert.Equal(t, 1, result.LiveCount)
	})

	t.Run("db fallback fills bridge hash absent from the store with zeroed live cells", func(t *testing.T) {
		t.Parallel()
		store := NewStore()
		store.EnsureInstance("radarr-main")
		store.Put("radarr-main", Entry{
			Info:       liveInfo("aaaa", "live movie", addedNewer),
			StateGroup: qbit.StateGroupSeeding,
			SyncedAt:   addedNewer.Add(time.Hour),
		})
		store.SetMovieMapping("radarr-main", "aaaa", 42)

		repo := &fakeTorrentsRepoWithFind{byHash: map[string]Entry{
			"bbbb": {
				Info: qbit.TorrentInfo{
					Hash:       "bbbb",
					Name:       "dead movie",
					StateRaw:   "stoppedUP",
					StateGroup: qbit.StateGroupPaused,
					AddedOn:    addedOlder,
					DlSpeed:    9999, // MUST be zeroed by the query
					UpSpeed:    9999,
					ETA:        9999,
					NumSeeds:   9999,
					NumLeechs:  9999,
					Progress:   0.5,
				},
				StateGroup: qbit.StateGroupPaused,
				SyncedAt:   addedOlder.Add(time.Hour),
			},
		}}
		lookup := &fakeMovieLookup{entries: map[string][]MovieMapEntry{
			movieLookupKey("radarr-main", 42): {
				{Hash: "aaaa", Source: MovieMapSourceWebhook, Provenance: MovieProvenanceRadarrSearch},
				{Hash: "bbbb", Source: MovieMapSourceRadarrHistory, Provenance: MovieProvenanceManualImport},
			},
		}}
		q := NewQuery(store, repo, nil).
			WithMovieLookup(lookup).
			WithClock(func() time.Time { return addedNewer.Add(2 * time.Hour) })

		result, err := q.ByMovieID(context.Background(), "radarr-main", 42)
		require.NoError(t, err)
		require.Len(t, result.Rows, 2)
		// added_on DESC — the live (newer) row first.
		assert.Equal(t, "aaaa", result.Rows[0].Entry.Info.Hash)
		assert.True(t, result.Rows[0].Live)
		assert.Equal(t, string(MovieProvenanceRadarrSearch), result.Rows[0].Provenance)

		assert.Equal(t, "bbbb", result.Rows[1].Entry.Info.Hash)
		assert.False(t, result.Rows[1].Live)
		assert.True(t, result.Rows[1].Present)
		assert.Equal(t, string(MovieProvenanceManualImport), result.Rows[1].Provenance)
		assert.EqualValues(t, 0, result.Rows[1].Entry.Info.DlSpeed)
		assert.EqualValues(t, 0, result.Rows[1].Entry.Info.UpSpeed)
		assert.EqualValues(t, 0, result.Rows[1].Entry.Info.ETA)
		assert.EqualValues(t, 0, result.Rows[1].Entry.Info.NumSeeds)
		assert.EqualValues(t, 0, result.Rows[1].Entry.Info.NumLeechs)
		assert.EqualValues(t, 0, result.Rows[1].Entry.Info.Progress)

		assert.Equal(t, 1, result.LiveCount)
		assert.Equal(t, addedNewer.Add(2*time.Hour), result.SyncedAt)
	})

	t.Run("movieLookup nil degrades to store-only rows with empty provenance", func(t *testing.T) {
		t.Parallel()
		store := NewStore()
		store.EnsureInstance("radarr-main")
		store.Put("radarr-main", Entry{
			Info:       liveInfo("aaaa", "live movie", addedNewer),
			StateGroup: qbit.StateGroupSeeding,
			SyncedAt:   addedNewer,
		})
		store.SetMovieMapping("radarr-main", "aaaa", 42)

		// WithMovieLookup(nil) is a no-op — movieLookup stays unset.
		q := NewQuery(store, &fakeTorrentsRepoWithFind{}, nil).WithMovieLookup(nil)

		result, err := q.ByMovieID(context.Background(), "radarr-main", 42)
		require.NoError(t, err)
		require.Len(t, result.Rows, 1)
		assert.Equal(t, "aaaa", result.Rows[0].Entry.Info.Hash)
		assert.True(t, result.Rows[0].Live)
		assert.Empty(t, result.Rows[0].Provenance)
	})

	t.Run("EntriesForMovie error wraps and returns an empty result", func(t *testing.T) {
		t.Parallel()
		store := NewStore()
		store.EnsureInstance("radarr-main")
		lookup := &fakeMovieLookup{err: errors.New("db dead")}
		q := NewQuery(store, &fakeTorrentsRepoWithFind{}, nil).WithMovieLookup(lookup)

		result, err := q.ByMovieID(context.Background(), "radarr-main", 42)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "lookup entries for movie")
		assert.Contains(t, err.Error(), "db dead")
		assert.Empty(t, result.Rows)
		assert.Equal(t, 0, result.LiveCount)
	})
}
