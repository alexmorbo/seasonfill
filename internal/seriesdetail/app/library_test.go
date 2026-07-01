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
	assert.False(t, view.StaleEnqueued)
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
