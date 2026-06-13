package torrentsync

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/infrastructure/qbit"
	"github.com/alexmorbo/seasonfill/infrastructure/sonarr"
)

type fakeMapRepo struct {
	mu   sync.Mutex
	rows []MapRow
	err  error
}

func (f *fakeMapRepo) Upsert(_ context.Context, row MapRow) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return f.err
	}
	f.rows = append(f.rows, row)
	return nil
}

func (f *fakeMapRepo) UpsertTx(ctx context.Context, row MapRow) error {
	return f.Upsert(ctx, row)
}

func (f *fakeMapRepo) snapshot() []MapRow {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]MapRow, len(f.rows))
	copy(out, f.rows)
	return out
}

type fakeGrabHashLookup struct {
	mu      sync.Mutex
	resp    []GrabHashRow
	err     error
	calls   int
	gotHash []string
	gotInst string
}

func (f *fakeGrabHashLookup) FindSeriesByTorrentHashes(_ context.Context, instance string, hashes []string) ([]GrabHashRow, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	f.gotInst = instance
	f.gotHash = append([]string(nil), hashes...)
	if f.err != nil {
		return nil, f.err
	}
	return f.resp, nil
}

type fakeSonarr struct {
	mu           sync.Mutex
	queueResp    sonarr.QueuePayload
	queueErr     error
	historyResp  []sonarr.HistoryPage // indexed by page-1
	historyErr   error
	queueCalls   int
	historyCalls int
	historyPages []int
}

func (f *fakeSonarr) QueueAll(_ context.Context) (sonarr.QueuePayload, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.queueCalls++
	return f.queueResp, f.queueErr
}

func (f *fakeSonarr) GrabHistoryPaged(_ context.Context, page, _ int) (sonarr.HistoryPage, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.historyCalls++
	f.historyPages = append(f.historyPages, page)
	if f.historyErr != nil {
		return sonarr.HistoryPage{}, f.historyErr
	}
	idx := page - 1
	if idx < 0 || idx >= len(f.historyResp) {
		return sonarr.HistoryPage{}, nil
	}
	return f.historyResp[idx], nil
}

type fakeGauge struct {
	mu   sync.Mutex
	last map[string]int
	hits int
}

func (f *fakeGauge) SetTorrentsyncUnmapped(instance string, count int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.last == nil {
		f.last = make(map[string]int)
	}
	f.last[instance] = count
	f.hits++
}

func newQuietLogger() *slog.Logger { return slog.New(slog.NewJSONHandler(io.Discard, nil)) }

func putUnmappedHash(t *testing.T, store *Store, instance, hash string) {
	t.Helper()
	store.EnsureInstance(instance)
	store.Put(instance, Entry{
		Info: qbit.TorrentInfo{
			Hash: hash, Name: "x", StateGroup: qbit.StateGroupSeeding,
		},
		StateGroup: qbit.StateGroupSeeding,
		SyncedAt:   time.Now().UTC(),
	})
}

func TestReconciler_MaybeRun_RespectsEveryNthTick(t *testing.T) {
	t.Parallel()
	store := NewStore()
	maps := &fakeMapRepo{}
	grabs := &fakeGrabHashLookup{}
	r := NewReconciler(store, maps, grabs, nil, nil, newQuietLogger()).WithEveryN(3)

	for i := 0; i < 7; i++ {
		require.NoError(t, r.MaybeRun(context.Background(), "alpha"))
	}
	// Ticks 3 and 6 trigger. Each pass calls the grab lookup once when
	// there are unmapped hashes — but with no hashes in store, grabs
	// short-circuits before the lookup. Tick counter however must
	// reflect 7 ticks.
	assert.Equal(t, 7, r.TickIndexFor("alpha"))
	// grabs.calls is 0 because store is empty (early return).
	assert.Equal(t, 0, grabs.calls)

	// Now add a hash and run 3 more ticks (8, 9, 10) — tick 9 fires.
	putUnmappedHash(t, store, "alpha", "aaaa")
	for i := 0; i < 3; i++ {
		require.NoError(t, r.MaybeRun(context.Background(), "alpha"))
	}
	// 9 - 6 = 3 more, tick 9 is %3==0, so one more grabs call.
	assert.Equal(t, 1, grabs.calls)
	assert.Equal(t, 10, r.TickIndexFor("alpha"))
}

func TestReconciler_FourSources_GrabRecord(t *testing.T) {
	t.Parallel()
	store := NewStore()
	maps := &fakeMapRepo{}
	grabs := &fakeGrabHashLookup{resp: []GrabHashRow{
		{Hash: "aaaa", SeriesID: 42, SeasonNumber: 3},
	}}
	r := NewReconciler(store, maps, grabs, nil, nil, newQuietLogger()).WithEveryN(1)
	putUnmappedHash(t, store, "alpha", "aaaa")

	require.NoError(t, r.MaybeRun(context.Background(), "alpha"))
	rows := maps.snapshot()
	require.Len(t, rows, 1)
	assert.Equal(t, MapSourceGrabRecord, rows[0].Source)
	assert.Equal(t, 42, rows[0].SeriesID)
	assert.Equal(t, 3, rows[0].SeasonNumber)
	assert.Equal(t, 42, store.SeriesForHash("alpha", "aaaa"))
}

func TestReconciler_FourSources_SonarrQueue(t *testing.T) {
	t.Parallel()
	store := NewStore()
	maps := &fakeMapRepo{}
	grabs := &fakeGrabHashLookup{} // no grab record match
	sn := &fakeSonarr{queueResp: sonarr.QueuePayload{
		Records: []sonarr.QueueRecord{
			{SeriesID: 77, SeasonNumber: 2, DownloadID: "BBBB"},
		},
	}}
	sonarrFor := func(_ string) (SonarrReconciler, bool) { return sn, true }
	r := NewReconciler(store, maps, grabs, sonarrFor, nil, newQuietLogger()).WithEveryN(1)
	putUnmappedHash(t, store, "alpha", "bbbb")

	require.NoError(t, r.MaybeRun(context.Background(), "alpha"))
	rows := maps.snapshot()
	require.Len(t, rows, 1)
	assert.Equal(t, MapSourceQueue, rows[0].Source)
	assert.Equal(t, 77, rows[0].SeriesID)
	assert.Equal(t, "bbbb", rows[0].Hash)
	assert.Equal(t, 77, store.SeriesForHash("alpha", "bbbb"))
	assert.Equal(t, 1, sn.queueCalls)
}

func TestReconciler_FourSources_SonarrHistory(t *testing.T) {
	t.Parallel()
	store := NewStore()
	maps := &fakeMapRepo{}
	grabs := &fakeGrabHashLookup{} // miss
	sn := &fakeSonarr{
		queueResp: sonarr.QueuePayload{}, // miss
		historyResp: []sonarr.HistoryPage{
			// Page 1: short (1 record < 50), triggers end-of-data reset.
			{
				Records: []sonarr.HistoryGrab{
					{DownloadID: "cccc", SeriesID: 99, SeasonNumber: 1},
				},
			},
		},
	}
	sonarrFor := func(_ string) (SonarrReconciler, bool) { return sn, true }
	r := NewReconciler(store, maps, grabs, sonarrFor, nil, newQuietLogger()).WithEveryN(1)
	putUnmappedHash(t, store, "alpha", "cccc")

	require.NoError(t, r.MaybeRun(context.Background(), "alpha"))
	rows := maps.snapshot()
	require.Len(t, rows, 1)
	assert.Equal(t, MapSourceHistory, rows[0].Source)
	assert.Equal(t, 99, rows[0].SeriesID)
	assert.Equal(t, 99, store.SeriesForHash("alpha", "cccc"))
	assert.Equal(t, 1, sn.historyCalls)
}

func TestReconciler_HistoryPriorityIsLast(t *testing.T) {
	t.Parallel()
	store := NewStore()
	maps := &fakeMapRepo{}
	// grab_records resolves first.
	grabs := &fakeGrabHashLookup{resp: []GrabHashRow{
		{Hash: "dddd", SeriesID: 50, SeasonNumber: 4},
	}}
	sn := &fakeSonarr{
		queueResp: sonarr.QueuePayload{Records: []sonarr.QueueRecord{
			{SeriesID: 51, DownloadID: "dddd"},
		}},
		historyResp: []sonarr.HistoryPage{{Records: []sonarr.HistoryGrab{
			{DownloadID: "dddd", SeriesID: 52, SeasonNumber: 1},
		}}},
	}
	sonarrFor := func(_ string) (SonarrReconciler, bool) { return sn, true }
	r := NewReconciler(store, maps, grabs, sonarrFor, nil, newQuietLogger()).WithEveryN(1)
	putUnmappedHash(t, store, "alpha", "dddd")

	require.NoError(t, r.MaybeRun(context.Background(), "alpha"))
	rows := maps.snapshot()
	require.Len(t, rows, 1, "first-source-wins")
	assert.Equal(t, MapSourceGrabRecord, rows[0].Source)
	assert.Equal(t, 50, rows[0].SeriesID)
	// Sonarr is hit (queue+history) because the closure runs unconditionally
	// after grabs. The point is the second/third-source rows are NOT written.
	// In our current impl applyQueue only writes rows for `wanted` hashes;
	// after grabs the wanted set is empty so queue does no writes and
	// history is skipped because `len(unmapped) == 0` short-circuit.
	assert.Equal(t, 0, sn.queueCalls, "queue should not be called when no unmapped hashes remain")
	assert.Equal(t, 0, sn.historyCalls)
}

func TestReconciler_HistoryCursorAdvancesAndCaps(t *testing.T) {
	t.Parallel()
	store := NewStore()
	maps := &fakeMapRepo{}
	grabs := &fakeGrabHashLookup{}
	// Build 25 full pages — each with HistoryPageSize records that
	// don't match the wanted hash. Reconciler walks 10 pages per
	// pass; two passes consume 20 pages total without hitting EOD.
	pages := make([]sonarr.HistoryPage, 25)
	for i := range pages {
		recs := make([]sonarr.HistoryGrab, HistoryPageSize)
		for j := range recs {
			recs[j] = sonarr.HistoryGrab{DownloadID: "filler", SeriesID: 1}
		}
		pages[i] = sonarr.HistoryPage{Records: recs}
	}
	sn := &fakeSonarr{historyResp: pages}
	sonarrFor := func(_ string) (SonarrReconciler, bool) { return sn, true }
	r := NewReconciler(store, maps, grabs, sonarrFor, nil, newQuietLogger()).WithEveryN(1)
	putUnmappedHash(t, store, "alpha", "eeee")

	require.NoError(t, r.MaybeRun(context.Background(), "alpha"))
	assert.Equal(t, HistoryPageCap, sn.historyCalls, "must cap at HistoryPageCap")
	// Cursor advanced to 11 (next page to fetch).
	assert.Equal(t, HistoryPageCap+1, r.CursorPageFor("alpha"))

	// Second pass continues from page 11.
	sn.mu.Lock()
	sn.historyPages = nil
	sn.historyCalls = 0
	sn.mu.Unlock()
	require.NoError(t, r.MaybeRun(context.Background(), "alpha"))
	assert.Equal(t, HistoryPageCap, sn.historyCalls)
	sn.mu.Lock()
	firstPage := sn.historyPages[0]
	sn.mu.Unlock()
	assert.Equal(t, HistoryPageCap+1, firstPage, "second pass starts at cursor")
}

func TestReconciler_HistoryCursorResetsAtEnd(t *testing.T) {
	t.Parallel()
	store := NewStore()
	maps := &fakeMapRepo{}
	grabs := &fakeGrabHashLookup{}
	// Page 1+2 full; page 3 short.
	full := make([]sonarr.HistoryGrab, HistoryPageSize)
	for j := range full {
		full[j] = sonarr.HistoryGrab{DownloadID: "filler", SeriesID: 1}
	}
	short := []sonarr.HistoryGrab{{DownloadID: "filler", SeriesID: 1}}
	sn := &fakeSonarr{historyResp: []sonarr.HistoryPage{
		{Records: full},
		{Records: full},
		{Records: short},
	}}
	sonarrFor := func(_ string) (SonarrReconciler, bool) { return sn, true }
	r := NewReconciler(store, maps, grabs, sonarrFor, nil, newQuietLogger()).WithEveryN(1)
	putUnmappedHash(t, store, "alpha", "ffff")

	require.NoError(t, r.MaybeRun(context.Background(), "alpha"))
	assert.Equal(t, 3, sn.historyCalls)
	assert.Equal(t, 1, r.CursorPageFor("alpha"), "end-of-data resets cursor to 1")
}

func TestReconciler_UnmappedGaugeReflectsLeftover(t *testing.T) {
	t.Parallel()
	store := NewStore()
	maps := &fakeMapRepo{}
	// resolve 3 of 5
	grabs := &fakeGrabHashLookup{resp: []GrabHashRow{
		{Hash: "h1", SeriesID: 1},
		{Hash: "h2", SeriesID: 1},
		{Hash: "h3", SeriesID: 1},
	}}
	gauge := &fakeGauge{}
	r := NewReconciler(store, maps, grabs, nil, gauge, newQuietLogger()).WithEveryN(1)
	for _, h := range []string{"h1", "h2", "h3", "h4", "h5"} {
		putUnmappedHash(t, store, "alpha", h)
	}

	require.NoError(t, r.MaybeRun(context.Background(), "alpha"))
	assert.Equal(t, 2, gauge.last["alpha"], "remaining unmapped after sources run")
	assert.Equal(t, 1, gauge.hits)
}

func TestReconciler_StoreSecondaryIndexUpdated(t *testing.T) {
	t.Parallel()
	store := NewStore()
	maps := &fakeMapRepo{}
	grabs := &fakeGrabHashLookup{resp: []GrabHashRow{
		{Hash: "ZZZZ", SeriesID: 33, SeasonNumber: 1},
	}}
	r := NewReconciler(store, maps, grabs, nil, nil, newQuietLogger()).WithEveryN(1)
	putUnmappedHash(t, store, "alpha", "zzzz")

	require.NoError(t, r.MaybeRun(context.Background(), "alpha"))
	hashes := store.HashesFor("alpha", 33)
	require.Len(t, hashes, 1)
	assert.Equal(t, "zzzz", hashes[0])
}

func TestReconciler_NoUnmappedShortCircuits(t *testing.T) {
	t.Parallel()
	store := NewStore()
	maps := &fakeMapRepo{}
	grabs := &fakeGrabHashLookup{}
	gauge := &fakeGauge{}
	r := NewReconciler(store, maps, grabs, nil, gauge, newQuietLogger()).WithEveryN(1)
	// No torrents in the store.
	require.NoError(t, r.MaybeRun(context.Background(), "alpha"))
	assert.Equal(t, 0, grabs.calls)
	assert.Equal(t, 0, gauge.last["alpha"])
	assert.Equal(t, 1, gauge.hits, "gauge still emits 0 on empty unmapped")
}

func TestReconciler_GrabLookupErrorDoesNotStallOtherSources(t *testing.T) {
	t.Parallel()
	store := NewStore()
	maps := &fakeMapRepo{}
	grabs := &fakeGrabHashLookup{err: errors.New("db down")}
	sn := &fakeSonarr{queueResp: sonarr.QueuePayload{Records: []sonarr.QueueRecord{
		{SeriesID: 7, DownloadID: "GGGG"},
	}}}
	sonarrFor := func(_ string) (SonarrReconciler, bool) { return sn, true }
	r := NewReconciler(store, maps, grabs, sonarrFor, nil, newQuietLogger()).WithEveryN(1)
	putUnmappedHash(t, store, "alpha", "gggg")

	require.NoError(t, r.MaybeRun(context.Background(), "alpha"))
	rows := maps.snapshot()
	require.Len(t, rows, 1)
	assert.Equal(t, MapSourceQueue, rows[0].Source)
}

func TestReconciler_NewReconciler_PanicsOnNilMaps(t *testing.T) {
	t.Parallel()
	assert.Panics(t, func() {
		_ = NewReconciler(NewStore(), nil, nil, nil, nil, newQuietLogger())
	})
}
