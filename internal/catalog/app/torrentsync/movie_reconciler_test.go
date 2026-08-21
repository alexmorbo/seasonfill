package torrentsync

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/internal/shared/clients/radarr"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
)

type fakeMovieMapRepo struct {
	mu   sync.Mutex
	rows []MovieMapRow
	err  error
}

func (f *fakeMovieMapRepo) Upsert(_ context.Context, row MovieMapRow) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return f.err
	}
	f.rows = append(f.rows, row)
	return nil
}

func (f *fakeMovieMapRepo) UpsertTx(ctx context.Context, row MovieMapRow) error {
	return f.Upsert(ctx, row)
}

func (f *fakeMovieMapRepo) byHash(hash string) (MovieMapRow, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, r := range f.rows {
		if r.Hash == hash {
			return r, true
		}
	}
	return MovieMapRow{}, false
}

func (f *fakeMovieMapRepo) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.rows)
}

type fakeRadarr struct {
	mu          sync.Mutex
	queueResp   radarr.QueuePayload
	queueErr    error
	grabResp    []radarr.HistoryPage // indexed by page-1
	grabErr     error
	importResp  []radarr.HistoryPage // indexed by page-1
	importErr   error
	queueCalls  int
	grabPages   []int
	importPages []int
}

func (f *fakeRadarr) QueueAll(context.Context) (radarr.QueuePayload, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.queueCalls++
	return f.queueResp, f.queueErr
}

func page(pages []radarr.HistoryPage, n int) radarr.HistoryPage {
	idx := n - 1
	if idx < 0 || idx >= len(pages) {
		return radarr.HistoryPage{}
	}
	return pages[idx]
}

func (f *fakeRadarr) GrabHistoryPaged(_ context.Context, p, _ int) (radarr.HistoryPage, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.grabPages = append(f.grabPages, p)
	if f.grabErr != nil {
		return radarr.HistoryPage{}, f.grabErr
	}
	return page(f.grabResp, p), nil
}

func (f *fakeRadarr) ImportHistoryPaged(_ context.Context, p, _ int) (radarr.HistoryPage, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.importPages = append(f.importPages, p)
	if f.importErr != nil {
		return radarr.HistoryPage{}, f.importErr
	}
	return page(f.importResp, p), nil
}

// newMovieReconciler builds a reconciler with the movie pass enabled and
// the series pass inert (no sonarr client, empty grab lookup).
func newMovieReconciler(store *Store, movieMaps MovieMapRepo, rc RadarrReconciler) *Reconciler {
	radarrFor := func(domain.InstanceName) (RadarrReconciler, bool) { return rc, true }
	return NewReconciler(store, &fakeMapRepo{}, &fakeGrabHashLookup{}, nil, nil, newQuietLogger()).
		WithEveryN(1).
		WithMovieSources(movieMaps, radarrFor)
}

func TestMovieReconciler_QueueRow_GrabbedIsRadarrSearch(t *testing.T) {
	t.Parallel()
	store := NewStore()
	movieMaps := &fakeMovieMapRepo{}
	rc := &fakeRadarr{
		queueResp: radarr.QueuePayload{Records: []radarr.QueueRecord{
			{MovieID: 42, DownloadID: "AAAA"},
		}},
		grabResp: []radarr.HistoryPage{{RawCount: 1, Records: []radarr.HistoryRecord{
			{DownloadID: "aaaa", MovieID: 42, EventType: "grabbed"},
		}}},
	}
	r := newMovieReconciler(store, movieMaps, rc)
	putUnmappedHash(t, store, "radarr-main", "aaaa")

	require.NoError(t, r.MaybeRun(context.Background(), "radarr-main"))

	row, ok := movieMaps.byHash("aaaa")
	require.True(t, ok)
	assert.Equal(t, MovieMapSourceRadarrQueue, row.Source)
	assert.Equal(t, MovieProvenanceRadarrSearch, row.Provenance)
	assert.Equal(t, domain.RadarrMovieID(42), row.RadarrMovieID)
	assert.Equal(t, domain.InstanceName("radarr-main"), row.Instance)
	assert.False(t, row.CreatedAt.IsZero())
	// The store's movie index is updated so the next pass skips the hash.
	assert.Equal(t, domain.RadarrMovieID(42), store.MovieForHash("radarr-main", "aaaa"))
}

func TestMovieReconciler_QueueRow_NotGrabbedIsManualImport(t *testing.T) {
	t.Parallel()
	store := NewStore()
	movieMaps := &fakeMovieMapRepo{}
	rc := &fakeRadarr{
		queueResp: radarr.QueuePayload{Records: []radarr.QueueRecord{
			{MovieID: 7, DownloadID: "bbbb"},
		}},
		// grabbed-history knows nothing about bbbb.
		grabResp: []radarr.HistoryPage{{RawCount: 1, Records: []radarr.HistoryRecord{
			{DownloadID: "zzzz", MovieID: 99, EventType: "grabbed"},
		}}},
	}
	r := newMovieReconciler(store, movieMaps, rc)
	putUnmappedHash(t, store, "radarr-main", "bbbb")

	require.NoError(t, r.MaybeRun(context.Background(), "radarr-main"))

	row, ok := movieMaps.byHash("bbbb")
	require.True(t, ok)
	assert.Equal(t, MovieMapSourceRadarrQueue, row.Source)
	assert.Equal(t, MovieProvenanceManualImport, row.Provenance)
	assert.Equal(t, domain.RadarrMovieID(7), row.RadarrMovieID)
}

// The LIVE acceptance case: a hand-added torrent Radarr already imported.
// Absent from /queue, absent from grabbed-history, present in
// downloadFolderImported history => manual_import.
func TestMovieReconciler_ImportHistoryOnly_IsManualImport(t *testing.T) {
	t.Parallel()
	store := NewStore()
	movieMaps := &fakeMovieMapRepo{}
	rc := &fakeRadarr{
		queueResp: radarr.QueuePayload{},
		grabResp:  []radarr.HistoryPage{{RawCount: 0}},
		importResp: []radarr.HistoryPage{{RawCount: 1, Records: []radarr.HistoryRecord{
			{DownloadID: "cccc", MovieID: 55, EventType: "downloadFolderImported"},
		}}},
	}
	r := newMovieReconciler(store, movieMaps, rc)
	putUnmappedHash(t, store, "radarr-main", "cccc")

	require.NoError(t, r.MaybeRun(context.Background(), "radarr-main"))

	row, ok := movieMaps.byHash("cccc")
	require.True(t, ok)
	assert.Equal(t, MovieMapSourceRadarrHistory, row.Source)
	assert.Equal(t, MovieProvenanceManualImport, row.Provenance)
	assert.Equal(t, domain.RadarrMovieID(55), row.RadarrMovieID)
}

// Grabbed-history-only hash (already left the queue): radarr_history +
// radarr_search.
func TestMovieReconciler_GrabHistoryOnly_IsRadarrSearch(t *testing.T) {
	t.Parallel()
	store := NewStore()
	movieMaps := &fakeMovieMapRepo{}
	rc := &fakeRadarr{
		grabResp: []radarr.HistoryPage{{RawCount: 1, Records: []radarr.HistoryRecord{
			{DownloadID: "dddd", MovieID: 8, EventType: "grabbed"},
		}}},
		importResp: []radarr.HistoryPage{{RawCount: 1, Records: []radarr.HistoryRecord{
			{DownloadID: "dddd", MovieID: 8, EventType: "downloadFolderImported"},
		}}},
	}
	r := newMovieReconciler(store, movieMaps, rc)
	putUnmappedHash(t, store, "radarr-main", "dddd")

	require.NoError(t, r.MaybeRun(context.Background(), "radarr-main"))

	assert.Equal(t, 1, movieMaps.count(), "the import stream must not double-write")
	row, ok := movieMaps.byHash("dddd")
	require.True(t, ok)
	assert.Equal(t, MovieMapSourceRadarrHistory, row.Source)
	assert.Equal(t, MovieProvenanceRadarrSearch, row.Provenance)
}

// First-source-wins inside one pass: /queue outranks /history.
func TestMovieReconciler_QueueOutranksHistory(t *testing.T) {
	t.Parallel()
	store := NewStore()
	movieMaps := &fakeMovieMapRepo{}
	rc := &fakeRadarr{
		queueResp: radarr.QueuePayload{Records: []radarr.QueueRecord{{MovieID: 3, DownloadID: "eeee"}}},
		grabResp: []radarr.HistoryPage{{RawCount: 1, Records: []radarr.HistoryRecord{
			{DownloadID: "eeee", MovieID: 3, EventType: "grabbed"},
		}}},
	}
	r := newMovieReconciler(store, movieMaps, rc)
	putUnmappedHash(t, store, "radarr-main", "eeee")

	require.NoError(t, r.MaybeRun(context.Background(), "radarr-main"))

	assert.Equal(t, 1, movieMaps.count())
	row, _ := movieMaps.byHash("eeee")
	assert.Equal(t, MovieMapSourceRadarrQueue, row.Source)
	assert.Equal(t, MovieProvenanceRadarrSearch, row.Provenance)
}

// A hash already carrying a webhook mapping in the store is never
// re-offered — the pass writes nothing for it and no upstream call can
// change that.
func TestMovieReconciler_PreExistingMapping_NotRewritten(t *testing.T) {
	t.Parallel()
	store := NewStore()
	movieMaps := &fakeMovieMapRepo{}
	rc := &fakeRadarr{
		queueResp: radarr.QueuePayload{Records: []radarr.QueueRecord{{MovieID: 999, DownloadID: "ffff"}}},
	}
	r := newMovieReconciler(store, movieMaps, rc)
	putUnmappedHash(t, store, "radarr-main", "ffff")
	// Simulate B1.2's webhook row having already been bridged.
	store.SetMovieMapping("radarr-main", "ffff", 111)

	require.NoError(t, r.MaybeRun(context.Background(), "radarr-main"))

	assert.Equal(t, 0, movieMaps.count(), "a mapped hash is not unmapped")
	assert.Equal(t, domain.RadarrMovieID(111), store.MovieForHash("radarr-main", "ffff"))
}

func TestMovieReconciler_SkipsZeroMovieIDAndEmptyHash(t *testing.T) {
	t.Parallel()
	store := NewStore()
	movieMaps := &fakeMovieMapRepo{}
	rc := &fakeRadarr{
		queueResp: radarr.QueuePayload{Records: []radarr.QueueRecord{
			{MovieID: 0, DownloadID: "gggg"}, // unknown item
			{MovieID: 5, DownloadID: ""},     // usenet
		}},
	}
	r := newMovieReconciler(store, movieMaps, rc)
	putUnmappedHash(t, store, "radarr-main", "gggg")

	require.NoError(t, r.MaybeRun(context.Background(), "radarr-main"))
	assert.Equal(t, 0, movieMaps.count())
}

// A full page (RawCount == MovieHistoryPageSize) keeps the walk going; the
// short page ends it. Cap is never exceeded.
func TestMovieReconciler_HistoryWindow_WalksUntilShortPageAndCaps(t *testing.T) {
	t.Parallel()
	full := radarr.HistoryPage{RawCount: MovieHistoryPageSize}
	store := NewStore()
	movieMaps := &fakeMovieMapRepo{}
	rc := &fakeRadarr{
		grabResp: []radarr.HistoryPage{
			full, full,
			{RawCount: 1, Records: []radarr.HistoryRecord{{DownloadID: "hhhh", MovieID: 2, EventType: "grabbed"}}},
		},
		importResp: []radarr.HistoryPage{full, full, full, full, full, full, full},
	}
	r := newMovieReconciler(store, movieMaps, rc)
	putUnmappedHash(t, store, "radarr-main", "hhhh")

	require.NoError(t, r.MaybeRun(context.Background(), "radarr-main"))

	rc.mu.Lock()
	defer rc.mu.Unlock()
	assert.Equal(t, []int{1, 2, 3}, rc.grabPages, "stops on the short page")
	assert.Len(t, rc.importPages, MovieHistoryPageCap, "never exceeds the cap")
}

// A page made entirely of usenet records (RawCount>0, Records empty) must
// NOT be read as end-of-data.
func TestMovieReconciler_HistoryWindow_AllUsenetPageIsNotEndOfData(t *testing.T) {
	t.Parallel()
	store := NewStore()
	movieMaps := &fakeMovieMapRepo{}
	rc := &fakeRadarr{
		grabResp: []radarr.HistoryPage{
			{RawCount: MovieHistoryPageSize}, // 50 usenet grabs, 0 torrent records
			{RawCount: 1, Records: []radarr.HistoryRecord{{DownloadID: "iiii", MovieID: 4, EventType: "grabbed"}}},
		},
	}
	r := newMovieReconciler(store, movieMaps, rc)
	putUnmappedHash(t, store, "radarr-main", "iiii")

	require.NoError(t, r.MaybeRun(context.Background(), "radarr-main"))

	row, ok := movieMaps.byHash("iiii")
	require.True(t, ok)
	assert.Equal(t, MovieProvenanceRadarrSearch, row.Provenance)
}

// A server that clamps our requested pageSize reports the clamp in
// HistoryPage.PageSize; a full clamped page must NOT read as end-of-data.
func TestMovieReconciler_HistoryWindow_HonoursServerPageSize(t *testing.T) {
	t.Parallel()
	store := NewStore()
	movieMaps := &fakeMovieMapRepo{}
	rc := &fakeRadarr{
		grabResp: []radarr.HistoryPage{
			{PageSize: 20, RawCount: 20}, // full page at the server's clamp
			{PageSize: 20, RawCount: 1, Records: []radarr.HistoryRecord{
				{DownloadID: "oooo", MovieID: 9, EventType: "grabbed"},
			}},
		},
	}
	r := newMovieReconciler(store, movieMaps, rc)
	putUnmappedHash(t, store, "radarr-main", "oooo")

	require.NoError(t, r.MaybeRun(context.Background(), "radarr-main"))

	row, ok := movieMaps.byHash("oooo")
	require.True(t, ok, "the walk must continue past a full clamped page")
	assert.Equal(t, MovieProvenanceRadarrSearch, row.Provenance)
}

// An upstream failure is reported but never aborts the pass: the sources
// that DID answer still write.
func TestMovieReconciler_QueueError_HistoryStillWrites(t *testing.T) {
	t.Parallel()
	store := NewStore()
	movieMaps := &fakeMovieMapRepo{}
	rc := &fakeRadarr{
		queueErr: errors.New("radarr down"),
		grabResp: []radarr.HistoryPage{{RawCount: 1, Records: []radarr.HistoryRecord{
			{DownloadID: "jjjj", MovieID: 6, EventType: "grabbed"},
		}}},
	}
	r := newMovieReconciler(store, movieMaps, rc)
	putUnmappedHash(t, store, "radarr-main", "jjjj")

	// MaybeRun never propagates — the loop must not stall on a bad pass.
	require.NoError(t, r.MaybeRun(context.Background(), "radarr-main"))

	row, ok := movieMaps.byHash("jjjj")
	require.True(t, ok)
	assert.Equal(t, MovieMapSourceRadarrHistory, row.Source)
}

// B-1 regression: the grabbed-history fetch is the provenance oracle, so a
// failure there must abort the pass BEFORE any write. Writing on a partial
// oracle would stamp manual_import permanently (the repo never rewrites
// provenance and SetMovieMapping stops the hash being re-offered), turning
// one transient Radarr 502 into a permanent misclassification.
func TestMovieReconciler_GrabHistoryError_WritesNothing(t *testing.T) {
	t.Parallel()
	store := NewStore()
	movieMaps := &fakeMovieMapRepo{}
	rc := &fakeRadarr{
		grabErr: errors.New("radarr 502"),
		// Both other sources know the hash — and would have written
		// manual_import if the pass had been allowed to continue.
		queueResp: radarr.QueuePayload{Records: []radarr.QueueRecord{{MovieID: 21, DownloadID: "nnnn"}}},
		importResp: []radarr.HistoryPage{{RawCount: 1, Records: []radarr.HistoryRecord{
			{DownloadID: "nnnn", MovieID: 21, EventType: "downloadFolderImported"},
		}}},
	}
	r := newMovieReconciler(store, movieMaps, rc)
	putUnmappedHash(t, store, "radarr-main", "nnnn")

	require.NoError(t, r.MaybeRun(context.Background(), "radarr-main"))

	assert.Equal(t, 0, movieMaps.count(), "no row may be written without a complete grabbed window")
	_, ok := movieMaps.byHash("nnnn")
	assert.False(t, ok)
	// The store index stays clean, so the next pass re-offers the hash.
	assert.Equal(t, domain.RadarrMovieID(0), store.MovieForHash("radarr-main", "nnnn"))

	// A later pass with a healthy oracle recovers the correct provenance.
	rc.mu.Lock()
	rc.grabErr = nil
	rc.grabResp = []radarr.HistoryPage{{RawCount: 1, Records: []radarr.HistoryRecord{
		{DownloadID: "nnnn", MovieID: 21, EventType: "grabbed"},
	}}}
	rc.mu.Unlock()

	require.NoError(t, r.MaybeRun(context.Background(), "radarr-main"))

	row, ok := movieMaps.byHash("nnnn")
	require.True(t, ok)
	assert.Equal(t, MovieProvenanceRadarrSearch, row.Provenance)
	assert.Equal(t, MovieMapSourceRadarrQueue, row.Source)
}

// A repo write failure leaves the hash unmapped (retried next pass) and
// does NOT poison the store index.
func TestMovieReconciler_WriteError_LeavesHashUnmapped(t *testing.T) {
	t.Parallel()
	store := NewStore()
	movieMaps := &fakeMovieMapRepo{err: errors.New("db down")}
	rc := &fakeRadarr{
		queueResp: radarr.QueuePayload{Records: []radarr.QueueRecord{{MovieID: 1, DownloadID: "kkkk"}}},
	}
	r := newMovieReconciler(store, movieMaps, rc)
	putUnmappedHash(t, store, "radarr-main", "kkkk")

	require.NoError(t, r.MaybeRun(context.Background(), "radarr-main"))
	assert.Equal(t, domain.RadarrMovieID(0), store.MovieForHash("radarr-main", "kkkk"))
}

// No WithMovieSources => no radarr call at all. Guards the "fleet without
// Radarr is byte-identical to pre-B1.3" invariant.
func TestMovieReconciler_Disabled_NoRadarrCalls(t *testing.T) {
	t.Parallel()
	store := NewStore()
	rc := &fakeRadarr{}
	r := NewReconciler(store, &fakeMapRepo{}, &fakeGrabHashLookup{}, nil, nil, newQuietLogger()).
		WithEveryN(1).
		WithMovieSources(nil, func(domain.InstanceName) (RadarrReconciler, bool) { return rc, true })
	putUnmappedHash(t, store, "radarr-main", "llll")

	require.NoError(t, r.MaybeRun(context.Background(), "radarr-main"))

	rc.mu.Lock()
	defer rc.mu.Unlock()
	assert.Equal(t, 0, rc.queueCalls)
	assert.Empty(t, rc.grabPages)
}

// On a cheap-only tick (not the boot tick, not an Nth tick) the Radarr
// /queue source still runs AND its provenance stays sound: the grabbed
// oracle rides with the cheap phase, so a queued hash Radarr grabbed is
// stamped radarr_search even though the import-history walk is skipped.
func TestMovieReconciler_CheapTick_QueueMapsWithSoundProvenance(t *testing.T) {
	t.Parallel()
	store := NewStore()
	movieMaps := &fakeMovieMapRepo{}
	rc := &fakeRadarr{
		queueResp: radarr.QueuePayload{Records: []radarr.QueueRecord{{MovieID: 42, DownloadID: "aaaa"}}},
		grabResp: []radarr.HistoryPage{{RawCount: 1, Records: []radarr.HistoryRecord{
			{DownloadID: "aaaa", MovieID: 42, EventType: "grabbed"},
		}}},
		importResp: []radarr.HistoryPage{{RawCount: 1, Records: []radarr.HistoryRecord{
			{DownloadID: "aaaa", MovieID: 42, EventType: "downloadFolderImported"},
		}}},
	}
	radarrFor := func(domain.InstanceName) (RadarrReconciler, bool) { return rc, true }
	r := NewReconciler(store, &fakeMapRepo{}, &fakeGrabHashLookup{}, nil, nil, newQuietLogger()).
		WithEveryN(5).
		WithMovieSources(movieMaps, radarrFor)

	// Tick 1 (boot) with an empty store short-circuits before any Radarr
	// call but still advances the tick counter.
	require.NoError(t, r.MaybeRun(context.Background(), "radarr-main"))
	rc.mu.Lock()
	assert.Equal(t, 0, rc.queueCalls, "empty store short-circuits the boot tick")
	rc.mu.Unlock()

	// Tick 2 is cheap-only (2 != 1 and 2%5 != 0).
	putUnmappedHash(t, store, "radarr-main", "aaaa")
	require.NoError(t, r.MaybeRun(context.Background(), "radarr-main"))

	row, ok := movieMaps.byHash("aaaa")
	require.True(t, ok, "queue source maps the hash on a cheap-only tick")
	assert.Equal(t, MovieMapSourceRadarrQueue, row.Source)
	assert.Equal(t, MovieProvenanceRadarrSearch, row.Provenance, "grabbed oracle rides with the cheap phase")
	rc.mu.Lock()
	assert.Empty(t, rc.importPages, "import-history walk is throttled off on a cheap tick")
	assert.NotEmpty(t, rc.grabPages, "grabbed-history oracle still runs (queue provenance depends on it)")
	rc.mu.Unlock()
}

// An import-ONLY hash (gone from /queue, absent from grabbed history) does
// NOT map on a cheap-only tick — the import walk is throttled — but DOES map
// on the next history tick.
func TestMovieReconciler_ImportOnlyHash_WaitsForHistoryTick(t *testing.T) {
	t.Parallel()
	store := NewStore()
	movieMaps := &fakeMovieMapRepo{}
	rc := &fakeRadarr{
		queueResp: radarr.QueuePayload{},
		grabResp:  []radarr.HistoryPage{{RawCount: 0}},
		importResp: []radarr.HistoryPage{{RawCount: 1, Records: []radarr.HistoryRecord{
			{DownloadID: "cccc", MovieID: 55, EventType: "downloadFolderImported"},
		}}},
	}
	radarrFor := func(domain.InstanceName) (RadarrReconciler, bool) { return rc, true }
	r := NewReconciler(store, &fakeMapRepo{}, &fakeGrabHashLookup{}, nil, nil, newQuietLogger()).
		WithEveryN(2).
		WithMovieSources(movieMaps, radarrFor)

	// Tick 1 (boot) short-circuits on the empty store.
	require.NoError(t, r.MaybeRun(context.Background(), "radarr-main"))
	putUnmappedHash(t, store, "radarr-main", "cccc")

	// Tick 2 is a history tick (2%2==0) — the import walk runs and maps it.
	require.NoError(t, r.MaybeRun(context.Background(), "radarr-main"))
	_, ok := movieMaps.byHash("cccc")
	require.True(t, ok, "import-only hash maps on a history tick")

	// Sanity: a fresh reconciler on a cheap-only tick leaves it unmapped.
	store2 := NewStore()
	movieMaps2 := &fakeMovieMapRepo{}
	rc2 := &fakeRadarr{
		queueResp: radarr.QueuePayload{},
		grabResp:  []radarr.HistoryPage{{RawCount: 0}},
		importResp: []radarr.HistoryPage{{RawCount: 1, Records: []radarr.HistoryRecord{
			{DownloadID: "cccc", MovieID: 55, EventType: "downloadFolderImported"},
		}}},
	}
	radarrFor2 := func(domain.InstanceName) (RadarrReconciler, bool) { return rc2, true }
	r2 := NewReconciler(store2, &fakeMapRepo{}, &fakeGrabHashLookup{}, nil, nil, newQuietLogger()).
		WithEveryN(5).
		WithMovieSources(movieMaps2, radarrFor2)
	require.NoError(t, r2.MaybeRun(context.Background(), "radarr-main")) // tick 1 boot, empty store
	putUnmappedHash(t, store2, "radarr-main", "cccc")
	require.NoError(t, r2.MaybeRun(context.Background(), "radarr-main")) // tick 2 cheap-only
	_, ok = movieMaps2.byHash("cccc")
	assert.False(t, ok, "import-only hash stays unmapped on a cheap-only tick")
	rc2.mu.Lock()
	assert.Empty(t, rc2.importPages, "import walk not attempted on a cheap-only tick")
	rc2.mu.Unlock()
}

// The unmapped gauge must fall once a hash is movie-bridged.
func TestMovieReconciler_GaugeCountsMovieMappedAsMapped(t *testing.T) {
	t.Parallel()
	store := NewStore()
	movieMaps := &fakeMovieMapRepo{}
	gauge := &fakeGauge{}
	rc := &fakeRadarr{
		queueResp: radarr.QueuePayload{Records: []radarr.QueueRecord{{MovieID: 12, DownloadID: "mmmm"}}},
	}
	radarrFor := func(domain.InstanceName) (RadarrReconciler, bool) { return rc, true }
	r := NewReconciler(store, &fakeMapRepo{}, &fakeGrabHashLookup{}, nil, gauge, newQuietLogger()).
		WithEveryN(1).
		WithMovieSources(movieMaps, radarrFor)
	putUnmappedHash(t, store, "radarr-main", "mmmm")

	require.NoError(t, r.MaybeRun(context.Background(), "radarr-main"))
	gauge.mu.Lock()
	assert.Equal(t, 0, gauge.last["radarr-main"])
	gauge.mu.Unlock()

	// Second pass: the hash is now indexed, so it is not even offered.
	require.NoError(t, r.MaybeRun(context.Background(), "radarr-main"))
	assert.Equal(t, 1, movieMaps.count(), "no re-upsert on the second pass")
}
