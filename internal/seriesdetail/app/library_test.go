package seriesdetail

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/internal/catalog/domain/series"
	"github.com/alexmorbo/seasonfill/internal/shared/clients/sonarr"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
)

type fakeLibCacheLookup struct {
	entries []series.CacheEntry
	err     error
}

func (f *fakeLibCacheLookup) ListBySeriesID(_ context.Context, _ domain.SeriesID) ([]series.CacheEntry, error) {
	return f.entries, f.err
}

func (f *fakeLibCacheLookup) ListBySeriesIDs(_ context.Context, ids []domain.SeriesID) (map[domain.SeriesID][]series.CacheEntry, error) {
	out := make(map[domain.SeriesID][]series.CacheEntry, len(ids))
	for _, id := range ids {
		out[id] = f.entries
	}
	return out, f.err
}

type fakeLibEpisodes struct {
	rows []series.CanonEpisode
	err  error
}

func (f *fakeLibEpisodes) ListBySeries(_ context.Context, _ domain.SeriesID) ([]series.CanonEpisode, error) {
	return f.rows, f.err
}

type fakeLibEpisodeStates struct {
	rows []series.EpisodeState
	err  error
}

func (f *fakeLibEpisodeStates) ListBySeries(_ context.Context, _ domain.InstanceName, _ domain.SeriesID) ([]series.EpisodeState, error) {
	return f.rows, f.err
}

type fakeLibGrabHistory struct {
	rows []GrabEvent
	err  error
}

func (f *fakeLibGrabHistory) RecentBySeries(_ context.Context, _ domain.InstanceName, _ domain.SonarrSeriesID, _ int) ([]GrabEvent, error) {
	return f.rows, f.err
}

type fakeQueueLister struct {
	payload sonarr.QueuePayload
	err     error
}

func (f *fakeQueueLister) Queue(_ context.Context, _ domain.SonarrSeriesID) (sonarr.QueuePayload, error) {
	return f.payload, f.err
}

type recordingSyncTrigger struct {
	calls int
	last  domain.SonarrSeriesID
}

func (r *recordingSyncTrigger) TriggerSeriesSync(_ domain.InstanceName, id domain.SonarrSeriesID) {
	r.calls++
	r.last = id
}

func fixedNow() time.Time { return time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC) }

// sonarrForOK returns a SonarrFor closure resolving to the supplied lister.
func sonarrForOK(l SonarrQueueLister) func(domain.InstanceName) (SonarrQueueLister, bool) {
	return func(domain.InstanceName) (SonarrQueueLister, bool) { return l, true }
}

func TestLibraryCompose_HappyPath(t *testing.T) {
	t.Parallel()
	now := fixedNow()
	entry := series.CacheEntry{
		InstanceName:      "homelab",
		SonarrSeriesID:    7,
		Monitored:         true,
		EpisodeFileCount:  8,
		AiredEpisodeCount: 10,
		MissingCount:      2,
		SizeOnDiskBytes:   1 << 30,
		UpdatedAt:         now,
	}
	episodes := []series.CanonEpisode{
		{ID: 1, SeasonNumber: 1, EpisodeNumber: 1},
		{ID: 2, SeasonNumber: 1, EpisodeNumber: 2},
		{ID: 3, SeasonNumber: 1, EpisodeNumber: 3},
	}
	states := []series.EpisodeState{
		{EpisodeID: 1, HasFile: true, Quality: new("WEB-DL 1080p"), UpdatedAt: now},
		{EpisodeID: 2, HasFile: true, Quality: new("WEB-DL 1080p"), UpdatedAt: now},
		{EpisodeID: 3, HasFile: false},
	}
	grabs := []GrabEvent{
		{Status: "imported", SeasonNumber: 1, ReleaseTitle: "Show.S01E02", Quality: "WEB-DL 1080p", CreatedAt: now.Add(-1 * time.Hour), UpdatedAt: now.Add(-30 * time.Minute)},
		{Status: "grabbed", SeasonNumber: 1, ReleaseTitle: "Show.S01E03", Quality: "WEB-DL 1080p", CreatedAt: now.Add(-2 * time.Hour), UpdatedAt: now.Add(-2 * time.Hour)},
	}
	queue := sonarr.QueuePayload{Records: []sonarr.QueueRecord{
		{ID: 11, EpisodeID: 3, SeasonNumber: 1, EpisodeNumber: 3, Title: "Ep 3", Status: "downloading", Size: 100, SizeLeft: 40},
	}}

	lc := NewLibraryComposer(LibraryDeps{
		CacheLookup:   &fakeLibCacheLookup{entries: []series.CacheEntry{entry}},
		Episodes:      &fakeLibEpisodes{rows: episodes},
		EpisodeStates: &fakeLibEpisodeStates{rows: states},
		GrabHistory:   &fakeLibGrabHistory{rows: grabs},
		SonarrFor:     sonarrForOK(&fakeQueueLister{payload: queue}),
		Now:           fixedNow,
	})

	view, err := lc.Compose(context.Background(), 42, "homelab")
	require.NoError(t, err)
	assert.Equal(t, domain.SonarrSeriesID(7), view.SonarrSeriesID)
	assert.True(t, view.Monitored)
	assert.Equal(t, 3, view.Strip.EpisodesTotal)
	assert.Equal(t, 8, view.Strip.EpisodesOnDisk)
	assert.Equal(t, 10, view.Strip.EpisodesAired)
	assert.Equal(t, 2, view.Strip.MissingCount)
	assert.Equal(t, int64(1<<30), view.Strip.SizeOnDiskBytes)
	assert.Equal(t, "WEB-DL 1080p", view.Strip.DominantQuality)
	assert.Len(t, view.Recent, 2)
	require.NotNil(t, view.LastGrabAt)
	assert.Equal(t, grabs[0].CreatedAt, *view.LastGrabAt)
	require.NotNil(t, view.LastImportedAt)
	assert.Equal(t, grabs[0].UpdatedAt, *view.LastImportedAt)
	require.NotNil(t, view.InProgress)
	assert.Equal(t, 60, view.InProgress.Percent)
	// Story 971 — hero download chip = first queue record (raw index 0).
	require.NotNil(t, view.Download)
	assert.Equal(t, 11, view.Download.QueueID)
	assert.Equal(t, domain.SonarrEpisodeID(3), view.Download.SonarrEpisodeID)
	assert.Equal(t, 1, view.Download.SeasonNumber)
	assert.Equal(t, "Ep 3", view.Download.Title)
	assert.Equal(t, "downloading", view.Download.Status)
	assert.False(t, view.StaleEnqueued)
	assert.Equal(t, []LibrarySeasonCountView{
		{SeasonNumber: 1, EpisodesOnDisk: 2, Downloading: 1},
	}, view.SeasonCounts)
}

// TestLibraryCompose_SeasonCounts_FromSeasonStats — bug #974. episode_states
// are ALL HasFile=false (stale, exactly like series 58 after files landed
// post-add), but season_stats.EpisodeFileCount=8 (refreshed by the scan). The
// per-season counter must read 8 (from season_stats), matching the library-strip
// aggregate — NOT 0 (the stale episode_states walk).
func TestLibraryCompose_SeasonCounts_FromSeasonStats(t *testing.T) {
	t.Parallel()
	now := fixedNow()
	entry := series.CacheEntry{
		InstanceName:      "homelab",
		SonarrSeriesID:    58,
		Monitored:         true,
		EpisodeFileCount:  8,
		AiredEpisodeCount: 8,
		SizeOnDiskBytes:   43 << 30,
		UpdatedAt:         now,
	}
	episodes := make([]series.CanonEpisode, 0, 8)
	states := make([]series.EpisodeState, 0, 8)
	for i := 1; i <= 8; i++ {
		episodes = append(episodes, series.CanonEpisode{ID: int64(i), SeasonNumber: 1, EpisodeNumber: i})
		states = append(states, series.EpisodeState{EpisodeID: domain.EpisodeID(i), HasFile: false, Monitored: false})
	}

	lc := NewLibraryComposer(LibraryDeps{
		CacheLookup:   &fakeLibCacheLookup{entries: []series.CacheEntry{entry}},
		Episodes:      &fakeLibEpisodes{rows: episodes},
		EpisodeStates: &fakeLibEpisodeStates{rows: states},
		SeasonStats:   &fakeSeasonStatsPort{rows: []series.SeasonStat{{SeasonNumber: 1, EpisodeFileCount: 8}}},
		Now:           fixedNow,
	})

	view, err := lc.Compose(context.Background(), 58, "homelab")
	require.NoError(t, err)
	// Collapsed per-season counter now matches the hero/library-strip aggregate.
	assert.Equal(t, []LibrarySeasonCountView{
		{SeasonNumber: 1, EpisodesOnDisk: 8, Downloading: 0},
	}, view.SeasonCounts)
	assert.Equal(t, 8, view.Strip.EpisodesOnDisk)
}

func TestLibraryCompose_NotInInstance(t *testing.T) {
	t.Parallel()
	lc := NewLibraryComposer(LibraryDeps{
		CacheLookup: &fakeLibCacheLookup{entries: []series.CacheEntry{
			{InstanceName: "beta", SonarrSeriesID: 5, UpdatedAt: fixedNow()},
		}},
		Episodes:      &fakeLibEpisodes{},
		EpisodeStates: &fakeLibEpisodeStates{},
		Now:           fixedNow,
	})
	view, err := lc.Compose(context.Background(), 42, "homelab")
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrSeriesNotInInstance))
	assert.Equal(t, LibraryView{}, view)
}

func TestLibraryCompose_StaleCache_Enqueues(t *testing.T) {
	t.Parallel()
	now := fixedNow()
	trigger := &recordingSyncTrigger{}
	lc := NewLibraryComposer(LibraryDeps{
		CacheLookup: &fakeLibCacheLookup{entries: []series.CacheEntry{
			{InstanceName: "homelab", SonarrSeriesID: 7, UpdatedAt: now.Add(-7 * time.Hour)},
		}},
		Episodes:      &fakeLibEpisodes{},
		EpisodeStates: &fakeLibEpisodeStates{},
		SyncTrigger:   trigger,
		Now:           fixedNow,
	})
	view, err := lc.Compose(context.Background(), 42, "homelab")
	require.NoError(t, err)
	assert.True(t, view.StaleEnqueued)
	assert.Equal(t, 1, trigger.calls)
	assert.Equal(t, domain.SonarrSeriesID(7), trigger.last)
	assert.Equal(t, domain.SonarrSeriesID(7), view.SonarrSeriesID)
}

func TestLibraryCompose_FreshCache_NoEnqueue(t *testing.T) {
	t.Parallel()
	now := fixedNow()
	trigger := &recordingSyncTrigger{}
	lc := NewLibraryComposer(LibraryDeps{
		CacheLookup: &fakeLibCacheLookup{entries: []series.CacheEntry{
			{InstanceName: "homelab", SonarrSeriesID: 7, UpdatedAt: now.Add(-1 * time.Hour)},
		}},
		Episodes:      &fakeLibEpisodes{},
		EpisodeStates: &fakeLibEpisodeStates{},
		SyncTrigger:   trigger,
		Now:           fixedNow,
	})
	view, err := lc.Compose(context.Background(), 42, "homelab")
	require.NoError(t, err)
	assert.False(t, view.StaleEnqueued)
	assert.Equal(t, 0, trigger.calls)
}

func TestLibraryCompose_NilSonarr_InProgressNil(t *testing.T) {
	t.Parallel()
	lc := NewLibraryComposer(LibraryDeps{
		CacheLookup: &fakeLibCacheLookup{entries: []series.CacheEntry{
			{InstanceName: "homelab", SonarrSeriesID: 7, UpdatedAt: fixedNow()},
		}},
		Episodes:      &fakeLibEpisodes{},
		EpisodeStates: &fakeLibEpisodeStates{},
		SonarrFor:     func(domain.InstanceName) (SonarrQueueLister, bool) { return nil, false },
		Now:           fixedNow,
	})
	view, err := lc.Compose(context.Background(), 42, "homelab")
	require.NoError(t, err)
	assert.Nil(t, view.InProgress)
	assert.Nil(t, view.Download)
}

func TestLibraryCompose_SonarrError_Degrades(t *testing.T) {
	t.Parallel()
	lc := NewLibraryComposer(LibraryDeps{
		CacheLookup: &fakeLibCacheLookup{entries: []series.CacheEntry{
			{InstanceName: "homelab", SonarrSeriesID: 7, UpdatedAt: fixedNow()},
		}},
		Episodes:      &fakeLibEpisodes{},
		EpisodeStates: &fakeLibEpisodeStates{},
		SonarrFor:     sonarrForOK(&fakeQueueLister{err: errors.New("sonarr down")}), //nolint:err113
		Now:           fixedNow,
	})
	view, err := lc.Compose(context.Background(), 42, "homelab")
	require.NoError(t, err)
	assert.Nil(t, view.InProgress)
	assert.Nil(t, view.Download)
}

func TestLibraryCompose_DownloadChip_FirstRecordWins(t *testing.T) {
	t.Parallel()
	queue := sonarr.QueuePayload{Records: []sonarr.QueueRecord{
		{ID: 21, EpisodeID: 8, SeasonNumber: 2, EpisodeNumber: 1, Title: "S2 queued", Status: "queued", Protocol: "torrent", DownloadID: "abc"},
		{ID: 22, EpisodeID: 9, SeasonNumber: 2, EpisodeNumber: 2, Title: "S2 dl", Status: "downloading", Size: 100, SizeLeft: 10},
	}}
	lc := NewLibraryComposer(LibraryDeps{
		CacheLookup: &fakeLibCacheLookup{entries: []series.CacheEntry{
			{InstanceName: "homelab", SonarrSeriesID: 7, UpdatedAt: fixedNow()},
		}},
		Episodes:      &fakeLibEpisodes{},
		EpisodeStates: &fakeLibEpisodeStates{},
		SonarrFor:     sonarrForOK(&fakeQueueLister{payload: queue}),
		Now:           fixedNow,
	})
	view, err := lc.Compose(context.Background(), 42, "homelab")
	require.NoError(t, err)

	// Raw-first pick — record[0] even though its status is "queued".
	require.NotNil(t, view.Download)
	assert.Equal(t, 21, view.Download.QueueID)
	assert.Equal(t, "queued", view.Download.Status)
	assert.Equal(t, "torrent", view.Download.Protocol)
	assert.Equal(t, "abc", view.Download.DownloadID)
	assert.Equal(t, domain.SonarrEpisodeID(8), view.Download.SonarrEpisodeID)

	// InProgress still filters to the downloading record — independent selection.
	require.NotNil(t, view.InProgress)
	assert.Equal(t, 2, view.InProgress.SeasonNumber)
	assert.Equal(t, 2, view.InProgress.EpisodeNumber)
}

func TestLibraryCompose_NextToAir_PrefersMonitored(t *testing.T) {
	t.Parallel()
	now := fixedNow()
	earlier := now.Add(24 * time.Hour)
	later := now.Add(48 * time.Hour)
	// earlier unmonitored (ID 1), later monitored (ID 2).
	episodes := []series.CanonEpisode{
		{ID: 1, SeasonNumber: 2, EpisodeNumber: 1, AirDate: &earlier},
		{ID: 2, SeasonNumber: 2, EpisodeNumber: 2, AirDate: &later},
	}
	states := []series.EpisodeState{
		{EpisodeID: 2, Monitored: true, UpdatedAt: now},
	}
	lc := NewLibraryComposer(LibraryDeps{
		CacheLookup: &fakeLibCacheLookup{entries: []series.CacheEntry{
			{InstanceName: "homelab", SonarrSeriesID: 7, UpdatedAt: now},
		}},
		Episodes:      &fakeLibEpisodes{rows: episodes},
		EpisodeStates: &fakeLibEpisodeStates{rows: states},
		Now:           fixedNow,
	})
	view, err := lc.Compose(context.Background(), 42, "homelab")
	require.NoError(t, err)
	require.NotNil(t, view.NextEpisodeToAir)
	assert.Equal(t, 2, view.NextEpisodeToAir.SeasonNumber)
	assert.Equal(t, 2, view.NextEpisodeToAir.EpisodeNumber)
	assert.Nil(t, view.NextEpisodeToAir.Title)

	// Now flip: no monitored future → returns bestAny (the earlier one).
	lc2 := NewLibraryComposer(LibraryDeps{
		CacheLookup: &fakeLibCacheLookup{entries: []series.CacheEntry{
			{InstanceName: "homelab", SonarrSeriesID: 7, UpdatedAt: now},
		}},
		Episodes:      &fakeLibEpisodes{rows: episodes},
		EpisodeStates: &fakeLibEpisodeStates{rows: nil},
		Now:           fixedNow,
	})
	view2, err := lc2.Compose(context.Background(), 42, "homelab")
	require.NoError(t, err)
	require.NotNil(t, view2.NextEpisodeToAir)
	assert.Equal(t, 1, view2.NextEpisodeToAir.EpisodeNumber)
	assert.Nil(t, view2.NextEpisodeToAir.Title)
}

func TestLibraryCompose_NoGrabHistory_EmptyRecent(t *testing.T) {
	t.Parallel()
	lc := NewLibraryComposer(LibraryDeps{
		CacheLookup: &fakeLibCacheLookup{entries: []series.CacheEntry{
			{InstanceName: "homelab", SonarrSeriesID: 7, UpdatedAt: fixedNow()},
		}},
		Episodes:      &fakeLibEpisodes{},
		EpisodeStates: &fakeLibEpisodeStates{},
		GrabHistory:   &fakeLibGrabHistory{rows: nil},
		Now:           fixedNow,
	})
	view, err := lc.Compose(context.Background(), 42, "homelab")
	require.NoError(t, err)
	assert.Empty(t, view.Recent)
	assert.Nil(t, view.LastGrabAt)
	assert.Nil(t, view.LastImportedAt)
}

func TestLibraryCompose_GrabHistoryError_Degrades(t *testing.T) {
	t.Parallel()
	lc := NewLibraryComposer(LibraryDeps{
		CacheLookup: &fakeLibCacheLookup{entries: []series.CacheEntry{
			{InstanceName: "homelab", SonarrSeriesID: 7, UpdatedAt: fixedNow()},
		}},
		Episodes:      &fakeLibEpisodes{},
		EpisodeStates: &fakeLibEpisodeStates{},
		GrabHistory:   &fakeLibGrabHistory{err: errors.New("db down")}, //nolint:err113
		Now:           fixedNow,
	})
	view, err := lc.Compose(context.Background(), 42, "homelab")
	require.NoError(t, err)
	assert.Empty(t, view.Recent)
}

func TestLibraryCompose_CacheError_Fails(t *testing.T) {
	t.Parallel()
	lc := NewLibraryComposer(LibraryDeps{
		CacheLookup:   &fakeLibCacheLookup{err: errors.New("db down")}, //nolint:err113
		Episodes:      &fakeLibEpisodes{},
		EpisodeStates: &fakeLibEpisodeStates{},
		Now:           fixedNow,
	})
	_, err := lc.Compose(context.Background(), 42, "homelab")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "list cache")
}

func TestLibraryCompose_EpisodesError_Fails(t *testing.T) {
	t.Parallel()
	lc := NewLibraryComposer(LibraryDeps{
		CacheLookup: &fakeLibCacheLookup{entries: []series.CacheEntry{
			{InstanceName: "homelab", SonarrSeriesID: 7, UpdatedAt: fixedNow()},
		}},
		Episodes:      &fakeLibEpisodes{err: errors.New("db down")}, //nolint:err113
		EpisodeStates: &fakeLibEpisodeStates{},
		Now:           fixedNow,
	})
	_, err := lc.Compose(context.Background(), 42, "homelab")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "episodes")
}

func TestLibraryCompose_SyncedAt_MaxOfCacheAndStates(t *testing.T) {
	t.Parallel()
	now := fixedNow()
	cacheUpdated := now.Add(-3 * time.Hour)
	stateUpdated := now.Add(-1 * time.Hour)
	lc := NewLibraryComposer(LibraryDeps{
		CacheLookup: &fakeLibCacheLookup{entries: []series.CacheEntry{
			{InstanceName: "homelab", SonarrSeriesID: 7, UpdatedAt: cacheUpdated},
		}},
		Episodes: &fakeLibEpisodes{},
		EpisodeStates: &fakeLibEpisodeStates{rows: []series.EpisodeState{
			{EpisodeID: 1, UpdatedAt: stateUpdated},
		}},
		Now: fixedNow,
	})
	view, err := lc.Compose(context.Background(), 42, "homelab")
	require.NoError(t, err)
	assert.Equal(t, stateUpdated, view.SyncedAt)
}

func TestBuildSeasonCounts(t *testing.T) {
	t.Parallel()

	// S1: 3 canon eps (2 episode_states HasFile), S2: 2 canon eps (1 HasFile).
	twoSeasonEpisodes := []series.CanonEpisode{
		{ID: 1, SeasonNumber: 1, EpisodeNumber: 1},
		{ID: 2, SeasonNumber: 1, EpisodeNumber: 2},
		{ID: 3, SeasonNumber: 1, EpisodeNumber: 3},
		{ID: 4, SeasonNumber: 2, EpisodeNumber: 1},
		{ID: 5, SeasonNumber: 2, EpisodeNumber: 2},
	}
	twoSeasonStates := []series.EpisodeState{
		{EpisodeID: 1, HasFile: true},
		{EpisodeID: 2, HasFile: true},
		{EpisodeID: 3, HasFile: false},
		{EpisodeID: 4, HasFile: true},
		{EpisodeID: 5, HasFile: false},
	}
	twoSeasonQueue := []QueueRecordDetail{
		{SeasonNumber: 2, Status: "downloading"},
		{SeasonNumber: 1, Status: "queued"},      // not downloading — ignored
		{SeasonNumber: 9, Status: "downloading"}, // no canon episode — skipped
	}

	tests := []struct {
		name     string
		episodes []series.CanonEpisode
		states   []series.EpisodeState
		stats    map[int]series.SeasonStat
		queue    []QueueRecordDetail
		want     []LibrarySeasonCountView
	}{
		{
			name: "bug#974: files on disk but episode_states stale → season_stats wins",
			episodes: []series.CanonEpisode{
				{ID: 1, SeasonNumber: 1, EpisodeNumber: 1},
				{ID: 2, SeasonNumber: 1, EpisodeNumber: 2},
				{ID: 3, SeasonNumber: 1, EpisodeNumber: 3},
				{ID: 4, SeasonNumber: 1, EpisodeNumber: 4},
				{ID: 5, SeasonNumber: 1, EpisodeNumber: 5},
				{ID: 6, SeasonNumber: 1, EpisodeNumber: 6},
				{ID: 7, SeasonNumber: 1, EpisodeNumber: 7},
				{ID: 8, SeasonNumber: 1, EpisodeNumber: 8},
			},
			states: []series.EpisodeState{
				{EpisodeID: 1, HasFile: false, Monitored: false},
				{EpisodeID: 2, HasFile: false, Monitored: false},
				{EpisodeID: 3, HasFile: false, Monitored: false},
				{EpisodeID: 4, HasFile: false, Monitored: false},
				{EpisodeID: 5, HasFile: false, Monitored: false},
				{EpisodeID: 6, HasFile: false, Monitored: false},
				{EpisodeID: 7, HasFile: false, Monitored: false},
				{EpisodeID: 8, HasFile: false, Monitored: false},
			},
			stats: map[int]series.SeasonStat{
				1: {SeasonNumber: 1, EpisodeFileCount: 8},
			},
			queue: nil,
			want: []LibrarySeasonCountView{
				{SeasonNumber: 1, EpisodesOnDisk: 8, Downloading: 0},
			},
		},
		{
			name:     "two seasons: season_stats authoritative per season (overrides episode_states)",
			episodes: twoSeasonEpisodes,
			states:   twoSeasonStates, // S1: 2 HasFile, S2: 1 HasFile
			stats: map[int]series.SeasonStat{
				1: {SeasonNumber: 1, EpisodeFileCount: 3}, // stat=3 beats the 2 HasFile rows
				2: {SeasonNumber: 2, EpisodeFileCount: 1},
			},
			queue: twoSeasonQueue,
			want: []LibrarySeasonCountView{
				{SeasonNumber: 1, EpisodesOnDisk: 3, Downloading: 0},
				{SeasonNumber: 2, EpisodesOnDisk: 1, Downloading: 1},
			},
		},
		{
			name:     "partial season_stats: present season uses stat, absent season falls back to episode_states",
			episodes: twoSeasonEpisodes,
			states:   twoSeasonStates, // S1: 2 HasFile, S2: 1 HasFile
			stats: map[int]series.SeasonStat{
				1: {SeasonNumber: 1, EpisodeFileCount: 3}, // stat wins → 3
			},
			queue: nil,
			want: []LibrarySeasonCountView{
				{SeasonNumber: 1, EpisodesOnDisk: 3, Downloading: 0},
				{SeasonNumber: 2, EpisodesOnDisk: 1, Downloading: 0}, // fallback → 1
			},
		},
		{
			name:     "nil season_stats → episode_states fallback (legacy behaviour preserved)",
			episodes: twoSeasonEpisodes,
			states:   twoSeasonStates,
			stats:    nil,
			queue:    twoSeasonQueue,
			want: []LibrarySeasonCountView{
				{SeasonNumber: 1, EpisodesOnDisk: 2, Downloading: 0},
				{SeasonNumber: 2, EpisodesOnDisk: 1, Downloading: 1},
			},
		},
		{
			name:     "no queue (Sonarr unreachable) → downloading all zero",
			episodes: twoSeasonEpisodes,
			states:   twoSeasonStates,
			stats:    nil,
			queue:    nil,
			want: []LibrarySeasonCountView{
				{SeasonNumber: 1, EpisodesOnDisk: 2, Downloading: 0},
				{SeasonNumber: 2, EpisodesOnDisk: 1, Downloading: 0},
			},
		},
		{
			name: "season present with zero on disk (stat=0)",
			episodes: []series.CanonEpisode{
				{ID: 10, SeasonNumber: 3, EpisodeNumber: 1},
			},
			states: []series.EpisodeState{
				{EpisodeID: 10, HasFile: false},
			},
			stats: map[int]series.SeasonStat{3: {SeasonNumber: 3, EpisodeFileCount: 0}},
			queue: nil,
			want: []LibrarySeasonCountView{
				{SeasonNumber: 3, EpisodesOnDisk: 0, Downloading: 0},
			},
		},
		{
			name: "specials (season 0) included, from season_stats",
			episodes: []series.CanonEpisode{
				{ID: 20, SeasonNumber: 0, EpisodeNumber: 1},
				{ID: 21, SeasonNumber: 1, EpisodeNumber: 1},
			},
			states: []series.EpisodeState{
				{EpisodeID: 20, HasFile: false}, // stale — stat must win
				{EpisodeID: 21, HasFile: false},
			},
			stats: map[int]series.SeasonStat{
				0: {SeasonNumber: 0, EpisodeFileCount: 1},
				1: {SeasonNumber: 1, EpisodeFileCount: 1},
			},
			queue: nil,
			want: []LibrarySeasonCountView{
				{SeasonNumber: 0, EpisodesOnDisk: 1, Downloading: 0},
				{SeasonNumber: 1, EpisodesOnDisk: 1, Downloading: 0},
			},
		},
		{
			name:     "empty / TMDB-only (no episodes) → nil, no panic",
			episodes: nil,
			states:   nil,
			stats:    nil,
			queue:    nil,
			want:     nil,
		},
		{
			name: "fallback path skips episode_states row not in canon, no panic",
			episodes: []series.CanonEpisode{
				{ID: 30, SeasonNumber: 1, EpisodeNumber: 1},
			},
			states: []series.EpisodeState{
				{EpisodeID: 30, HasFile: true},
				{EpisodeID: 999, HasFile: true}, // orphan state — skipped
			},
			stats: nil, // force the fallback path
			queue: nil,
			want: []LibrarySeasonCountView{
				{SeasonNumber: 1, EpisodesOnDisk: 1, Downloading: 0},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := buildSeasonCounts(tt.episodes, tt.states, tt.stats, tt.queue)
			assert.Equal(t, tt.want, got)
		})
	}
}
