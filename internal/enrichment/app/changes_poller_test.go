package enrichment

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	enrichdomain "github.com/alexmorbo/seasonfill/internal/enrichment/domain/enrichment"
	"github.com/alexmorbo/seasonfill/internal/shared/clients/tmdb"
	ports "github.com/alexmorbo/seasonfill/internal/shared/dataports"
)

// ---- fakes -----------------------------------------------------------------

// utcMid mirrors the domain's unexported utcMidnight for assertions (the domain
// helper is not importable across packages).
func utcMid(t time.Time) time.Time {
	u := t.UTC()
	return time.Date(u.Year(), u.Month(), u.Day(), 0, 0, 0, 0, time.UTC)
}

type recPage struct {
	page tmdb.ChangedIDsPage
	err  error
}

type listerCall struct {
	start time.Time
	end   time.Time
	page  int
}

// fakeLister returns a page script. byWindow (keyed by start date YYYY-MM-DD)
// wins when present; otherwise dflt is used for every window.
type fakeLister struct {
	mu       sync.Mutex
	calls    []listerCall
	dflt     []recPage
	byWindow map[string][]recPage
}

func (f *fakeLister) GetTVChangesPage(_ context.Context, start, end time.Time, page int) (tmdb.ChangedIDsPage, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, listerCall{start: start, end: end, page: page})
	script := f.dflt
	if f.byWindow != nil {
		if s, ok := f.byWindow[start.Format("2006-01-02")]; ok {
			script = s
		}
	}
	idx := page - 1
	if idx < 0 || idx >= len(script) {
		return tmdb.ChangedIDsPage{}, errors.New("fakeLister: page out of script range")
	}
	r := script[idx]
	return r.page, r.err
}

func (f *fakeLister) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

type markCall struct {
	ids           []int64
	markedAt      time.Time
	dedupBoundary time.Time
}

// fakeMarker records every call and returns rows (or rowsFn(ids)). errOnCall is
// a 1-based index that returns an error on that call (0 = never).
type fakeMarker struct {
	mu        sync.Mutex
	calls     []markCall
	rows      int64
	rowsFn    func(ids []int64) int64
	errOnCall int
}

func (f *fakeMarker) MarkChangedByTMDBIDs(_ context.Context, ids []int64, markedAt, dedupBoundary time.Time) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	cp := append([]int64(nil), ids...)
	f.calls = append(f.calls, markCall{ids: cp, markedAt: markedAt, dedupBoundary: dedupBoundary})
	if f.errOnCall != 0 && len(f.calls) == f.errOnCall {
		return 0, errors.New("mark boom")
	}
	if f.rowsFn != nil {
		return f.rowsFn(cp), nil
	}
	return f.rows, nil
}

// fakeCursorStore is an in-memory cursor that records every Save.
type fakeCursorStore struct {
	mu      sync.Mutex
	cur     enrichdomain.ChangeCursor
	getErr  error
	saveErr error
	saves   []enrichdomain.ChangeCursor
}

func (f *fakeCursorStore) Get(context.Context) (enrichdomain.ChangeCursor, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.getErr != nil {
		return enrichdomain.ChangeCursor{}, f.getErr
	}
	return f.cur, nil
}

func (f *fakeCursorStore) Save(_ context.Context, c enrichdomain.ChangeCursor) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.saveErr != nil {
		return f.saveErr
	}
	f.saves = append(f.saves, c)
	f.cur = c
	return nil
}

type recordingMetrics struct {
	mu        sync.Mutex
	polls     []string
	pages     int
	firehose  int
	matched   int64
	durations []time.Duration
	lags      []time.Duration
}

func (m *recordingMetrics) IncPoll(result string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.polls = append(m.polls, result)
}
func (m *recordingMetrics) AddPages(n int)       { m.mu.Lock(); defer m.mu.Unlock(); m.pages += n }
func (m *recordingMetrics) AddFirehoseIDs(n int) { m.mu.Lock(); defer m.mu.Unlock(); m.firehose += n }
func (m *recordingMetrics) AddMatched(n int64)   { m.mu.Lock(); defer m.mu.Unlock(); m.matched += n }
func (m *recordingMetrics) ObservePollDuration(d time.Duration) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.durations = append(m.durations, d)
}
func (m *recordingMetrics) SetCursorLag(d time.Duration) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.lags = append(m.lags, d)
}
func (m *recordingMetrics) lastPoll() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.polls) == 0 {
		return ""
	}
	return m.polls[len(m.polls)-1]
}

// fixedClock returns a Clock dep pinned to now.
func fixedClock(now time.Time) func() time.Time { return func() time.Time { return now } }

func newTestPoller(t *testing.T, deps ChangesPollerDeps) *ChangesPoller {
	t.Helper()
	p, err := NewChangesPoller(deps)
	require.NoError(t, err)
	return p
}

// ---- tests -----------------------------------------------------------------

// happy_path: one window (gap 3d), 3 overlapping pages → dedup to 6 distinct
// firehose ids, chunked at MarkBatch=4, cursor Saved once at window End.
func TestChangesPoller_HappyPath(t *testing.T) {
	now := time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC)
	origLWE := time.Date(2026, 7, 13, 0, 0, 0, 0, time.UTC)
	lister := &fakeLister{dflt: []recPage{
		{page: tmdb.ChangedIDsPage{IDs: []int64{100, 200, 300}, Page: 1, TotalPages: 3}},
		{page: tmdb.ChangedIDsPage{IDs: []int64{300, 400, 500}, Page: 2, TotalPages: 3}}, // 300 dup
		{page: tmdb.ChangedIDsPage{IDs: []int64{500, 600}, Page: 3, TotalPages: 3}},      // 500 dup
	}}
	marker := &fakeMarker{rowsFn: func(ids []int64) int64 { return int64(len(ids)) }}
	store := &fakeCursorStore{cur: enrichdomain.ChangeCursor{SchemaVersion: 1, LastWindowEnd: origLWE}}
	rec := &recordingMetrics{}
	p := newTestPoller(t, ChangesPollerDeps{
		Lister: lister, Marker: marker, CursorStore: store, Metrics: rec,
		Clock: fixedClock(now), MarkBatch: 4,
	})

	res, err := p.poll(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "ok", res.Result)

	// distinct firehose = {100,200,300,400,500,600} = 6.
	assert.Equal(t, 6, res.Firehose)
	assert.Equal(t, 6, rec.firehose)
	assert.Equal(t, 3, res.Pages)
	assert.Equal(t, 3, rec.pages)

	// chunked at MarkBatch=4 → [100,200,300,400] then [500,600] (sorted).
	require.Len(t, marker.calls, 2)
	assert.Equal(t, []int64{100, 200, 300, 400}, marker.calls[0].ids)
	assert.Equal(t, []int64{500, 600}, marker.calls[1].ids)
	assert.Equal(t, int64(6), res.Matched)
	assert.Equal(t, int64(6), rec.matched)

	// every mark call: markedAt == now, dedupBoundary == original cursor LWE.
	for _, c := range marker.calls {
		assert.Equal(t, now, c.markedAt)
		assert.Equal(t, origLWE, c.dedupBoundary)
	}

	// cursor Saved once, LastWindowEnd == today.
	require.Len(t, store.saves, 1)
	assert.Equal(t, utcMid(now), store.saves[0].LastWindowEnd)
	assert.Equal(t, 6, store.saves[0].LastFirehose)
	assert.Equal(t, 6, store.saves[0].LastMatched)

	assert.Equal(t, "ok", rec.lastPoll())
	require.Len(t, rec.lags, 1)
	require.Len(t, rec.durations, 1)
}

// F3: 2-window scenario; window 1 succeeds+Saves, window 2 page 1 errors →
// exactly ONE Save (window 1's End), cursor NOT advanced past it, error result.
func TestChangesPoller_F3_AbortWindow_NoCursorAdvance(t *testing.T) {
	now := time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC)
	origLWE := time.Date(2026, 7, 3, 6, 30, 0, 0, time.UTC) // gap 13d → windows [07-02..07-15],[07-15..07-16]
	lister := &fakeLister{byWindow: map[string][]recPage{
		"2026-07-02": {{page: tmdb.ChangedIDsPage{IDs: []int64{11, 22}, Page: 1, TotalPages: 1}}},
		"2026-07-15": {{err: errors.New("tmdb 500")}}, // window 2 page 1 fails
	}}
	marker := &fakeMarker{rows: 2}
	store := &fakeCursorStore{cur: enrichdomain.ChangeCursor{SchemaVersion: 1, LastWindowEnd: origLWE}}
	rec := &recordingMetrics{}
	p := newTestPoller(t, ChangesPollerDeps{
		Lister: lister, Marker: marker, CursorStore: store, Metrics: rec, Clock: fixedClock(now),
	})

	res, err := p.poll(context.Background())
	require.Error(t, err)
	assert.Equal(t, "error", res.Result)
	assert.Equal(t, "error", rec.lastPoll())

	// window 1 marked (prior work persisted / idempotent), window 2 not.
	require.Len(t, marker.calls, 1)
	assert.Equal(t, origLWE, marker.calls[0].dedupBoundary)

	// cursor advanced ONLY through window 1's End (07-15).
	require.Len(t, store.saves, 1)
	assert.Equal(t, time.Date(2026, 7, 15, 0, 0, 0, 0, time.UTC), store.saves[0].LastWindowEnd)
}

// F4: replay the same store; second run's Marker returns 0 rows (simulated
// downstream dedup) → mark still invoked, matched=0, idempotent, no panic.
func TestChangesPoller_F4_Replay_Idempotent(t *testing.T) {
	now := time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC)
	lister := &fakeLister{dflt: []recPage{
		{page: tmdb.ChangedIDsPage{IDs: []int64{7, 8, 9}, Page: 1, TotalPages: 1}},
	}}
	marker := &fakeMarker{rows: 3}
	store := &fakeCursorStore{cur: enrichdomain.ChangeCursor{SchemaVersion: 1, LastWindowEnd: time.Date(2026, 7, 13, 0, 0, 0, 0, time.UTC)}}
	rec := &recordingMetrics{}
	p := newTestPoller(t, ChangesPollerDeps{
		Lister: lister, Marker: marker, CursorStore: store, Metrics: rec, Clock: fixedClock(now),
	})

	res1, err := p.poll(context.Background())
	require.NoError(t, err)
	assert.Equal(t, int64(3), res1.Matched)
	firstCalls := len(marker.calls)

	marker.rows = 0 // second run: everything already marked → 0 rows
	res2, err := p.poll(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "ok", res2.Result)
	assert.Greater(t, len(marker.calls), firstCalls) // mark still invoked
	assert.Equal(t, int64(0), res2.Matched)          // nothing newly matched
}

// F18: a poll already in flight → second call returns skipped_inflight without
// touching the Lister. White-box: fill the channel directly.
func TestChangesPoller_F18_InFlightSkip(t *testing.T) {
	now := time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC)
	lister := &fakeLister{dflt: []recPage{{page: tmdb.ChangedIDsPage{IDs: []int64{1}, Page: 1, TotalPages: 1}}}}
	store := &fakeCursorStore{cur: enrichdomain.ChangeCursor{LastWindowEnd: time.Date(2026, 7, 13, 0, 0, 0, 0, time.UTC)}}
	rec := &recordingMetrics{}
	p := newTestPoller(t, ChangesPollerDeps{
		Lister: lister, Marker: &fakeMarker{}, CursorStore: store, Metrics: rec, Clock: fixedClock(now),
	})

	p.inFlight <- struct{}{} // simulate a concurrent running poll
	res, err := p.poll(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "skipped_inflight", res.Result)
	assert.Equal(t, 0, lister.callCount())
	assert.Equal(t, "skipped_inflight", rec.lastPoll())
	<-p.inFlight // drain
}

// cursor_reset_future: LastWindowEnd = now+48h → reset (zeroed LWE Saved),
// Lister untouched, nil err.
func TestChangesPoller_CursorResetFuture(t *testing.T) {
	now := time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC)
	lister := &fakeLister{}
	store := &fakeCursorStore{cur: enrichdomain.ChangeCursor{SchemaVersion: 2, LastWindowEnd: now.Add(48 * time.Hour)}}
	rec := &recordingMetrics{}
	p := newTestPoller(t, ChangesPollerDeps{
		Lister: lister, Marker: &fakeMarker{}, CursorStore: store, Metrics: rec, Clock: fixedClock(now),
	})

	res, err := p.poll(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "cursor_reset", res.Result)
	assert.Equal(t, "cursor_reset", rec.lastPoll())
	assert.Equal(t, 0, lister.callCount())

	require.Len(t, store.saves, 1)
	assert.True(t, store.saves[0].LastWindowEnd.IsZero())
	assert.Equal(t, 2, store.saves[0].SchemaVersion) // preserved
	assert.Equal(t, now, store.saves[0].LastPollAt)
}

// cursor_reset_stale: LastWindowEnd = now-20d (>14) → same reset path.
func TestChangesPoller_CursorResetStale(t *testing.T) {
	now := time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC)
	lister := &fakeLister{}
	store := &fakeCursorStore{cur: enrichdomain.ChangeCursor{SchemaVersion: 1, LastWindowEnd: now.Add(-20 * 24 * time.Hour)}}
	rec := &recordingMetrics{}
	p := newTestPoller(t, ChangesPollerDeps{
		Lister: lister, Marker: &fakeMarker{}, CursorStore: store, Metrics: rec, Clock: fixedClock(now),
	})

	res, err := p.poll(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "cursor_reset", res.Result)
	assert.Equal(t, 0, lister.callCount())
	require.Len(t, store.saves, 1)
	assert.True(t, store.saves[0].LastWindowEnd.IsZero())
}

// dedupBoundary_constant (R-A01): 2-window poll → every Mark call across BOTH
// windows uses dedupBoundary == the pre-advance cursor.LastWindowEnd, never any
// w.Start (midnights).
func TestChangesPoller_DedupBoundaryConstantAcrossWindows(t *testing.T) {
	now := time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC)
	origLWE := time.Date(2026, 7, 3, 6, 30, 0, 0, time.UTC) // non-midnight → differs from any window Start
	lister := &fakeLister{byWindow: map[string][]recPage{
		"2026-07-02": {{page: tmdb.ChangedIDsPage{IDs: []int64{1, 2}, Page: 1, TotalPages: 1}}},
		"2026-07-15": {{page: tmdb.ChangedIDsPage{IDs: []int64{3, 4}, Page: 1, TotalPages: 1}}},
	}}
	marker := &fakeMarker{rows: 2}
	store := &fakeCursorStore{cur: enrichdomain.ChangeCursor{SchemaVersion: 1, LastWindowEnd: origLWE}}
	rec := &recordingMetrics{}
	p := newTestPoller(t, ChangesPollerDeps{
		Lister: lister, Marker: marker, CursorStore: store, Metrics: rec, Clock: fixedClock(now),
	})

	res, err := p.poll(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "ok", res.Result)

	// per-window advance → 2 Saves.
	require.Len(t, store.saves, 2)
	assert.Equal(t, time.Date(2026, 7, 15, 0, 0, 0, 0, time.UTC), store.saves[0].LastWindowEnd)
	assert.Equal(t, time.Date(2026, 7, 16, 0, 0, 0, 0, time.UTC), store.saves[1].LastWindowEnd)

	w1Start := time.Date(2026, 7, 2, 0, 0, 0, 0, time.UTC)
	w2Start := time.Date(2026, 7, 15, 0, 0, 0, 0, time.UTC)
	require.GreaterOrEqual(t, len(marker.calls), 2)
	for i, c := range marker.calls {
		assert.Equalf(t, origLWE, c.dedupBoundary, "mark %d: dedupBoundary must equal pre-advance cursor.LastWindowEnd (R-A01)", i)
		assert.Falsef(t, c.dedupBoundary.Equal(w1Start), "mark %d: dedupBoundary must NOT be window 1 Start", i)
		assert.Falsef(t, c.dedupBoundary.Equal(w2Start), "mark %d: dedupBoundary must NOT be window 2 Start", i)
	}
}

// cold_start: Get → ErrNotFound → single [today-1, today] window, dedupBoundary
// zero, cursor Saved at today.
func TestChangesPoller_ColdStart(t *testing.T) {
	now := time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC)
	lister := &fakeLister{dflt: []recPage{{page: tmdb.ChangedIDsPage{IDs: []int64{55}, Page: 1, TotalPages: 1}}}}
	marker := &fakeMarker{rows: 1}
	store := &fakeCursorStore{getErr: ports.ErrNotFound}
	rec := &recordingMetrics{}
	p := newTestPoller(t, ChangesPollerDeps{
		Lister: lister, Marker: marker, CursorStore: store, Metrics: rec, Clock: fixedClock(now),
	})

	res, err := p.poll(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "ok", res.Result)

	require.GreaterOrEqual(t, lister.callCount(), 1)
	assert.Equal(t, time.Date(2026, 7, 15, 0, 0, 0, 0, time.UTC), lister.calls[0].start)
	assert.Equal(t, time.Date(2026, 7, 16, 0, 0, 0, 0, time.UTC), lister.calls[0].end)

	require.Len(t, marker.calls, 1)
	assert.True(t, marker.calls[0].dedupBoundary.IsZero()) // empty cursor → zero boundary

	require.Len(t, store.saves, 1)
	assert.Equal(t, time.Date(2026, 7, 16, 0, 0, 0, 0, time.UTC), store.saves[0].LastWindowEnd)
}

// no_client skip (F15): ClientReady=false → skipped_no_client, Lister untouched.
func TestChangesPoller_NoClientSkip(t *testing.T) {
	now := time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC)
	lister := &fakeLister{}
	rec := &recordingMetrics{}
	p := newTestPoller(t, ChangesPollerDeps{
		Lister: lister, Marker: &fakeMarker{}, CursorStore: &fakeCursorStore{}, Metrics: rec,
		Clock: fixedClock(now), ClientReady: func() bool { return false },
	})

	res, err := p.poll(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "skipped_no_client", res.Result)
	assert.Equal(t, 0, lister.callCount())
	assert.Equal(t, "skipped_no_client", rec.lastPoll())
}

// page_cap: TotalPages=5 but PageCap=2 → only 2 pages fetched, window SUCCEEDS.
func TestChangesPoller_PageCapStopsPagination(t *testing.T) {
	now := time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC)
	lister := &fakeLister{dflt: []recPage{
		{page: tmdb.ChangedIDsPage{IDs: []int64{1}, Page: 1, TotalPages: 5}},
		{page: tmdb.ChangedIDsPage{IDs: []int64{2}, Page: 2, TotalPages: 5}},
		{page: tmdb.ChangedIDsPage{IDs: []int64{3}, Page: 3, TotalPages: 5}},
	}}
	store := &fakeCursorStore{cur: enrichdomain.ChangeCursor{SchemaVersion: 1, LastWindowEnd: time.Date(2026, 7, 13, 0, 0, 0, 0, time.UTC)}}
	rec := &recordingMetrics{}
	p := newTestPoller(t, ChangesPollerDeps{
		Lister: lister, Marker: &fakeMarker{rows: 1}, CursorStore: store, Metrics: rec,
		Clock: fixedClock(now), PageCap: 2,
	})

	res, err := p.poll(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "ok", res.Result)
	assert.Equal(t, 2, res.Pages)
	assert.Equal(t, 2, lister.callCount())
	require.Len(t, store.saves, 1) // window still advanced
}

// cursor Get non-NotFound error → error result, no fetch.
func TestChangesPoller_CursorGetError(t *testing.T) {
	now := time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC)
	lister := &fakeLister{}
	store := &fakeCursorStore{getErr: errors.New("db down")}
	rec := &recordingMetrics{}
	p := newTestPoller(t, ChangesPollerDeps{
		Lister: lister, Marker: &fakeMarker{}, CursorStore: store, Metrics: rec, Clock: fixedClock(now),
	})

	res, err := p.poll(context.Background())
	require.Error(t, err)
	assert.Equal(t, "error", res.Result)
	assert.Equal(t, "error", rec.lastPoll())
	assert.Equal(t, 0, lister.callCount())
}

// cursor Save failure inside the window loop → error result.
func TestChangesPoller_CursorSaveError(t *testing.T) {
	now := time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC)
	lister := &fakeLister{dflt: []recPage{{page: tmdb.ChangedIDsPage{IDs: []int64{1}, Page: 1, TotalPages: 1}}}}
	store := &fakeCursorStore{
		cur:     enrichdomain.ChangeCursor{SchemaVersion: 1, LastWindowEnd: time.Date(2026, 7, 13, 0, 0, 0, 0, time.UTC)},
		saveErr: errors.New("db down"),
	}
	rec := &recordingMetrics{}
	p := newTestPoller(t, ChangesPollerDeps{
		Lister: lister, Marker: &fakeMarker{rows: 1}, CursorStore: store, Metrics: rec, Clock: fixedClock(now),
	})

	res, err := p.poll(context.Background())
	require.Error(t, err)
	assert.Equal(t, "error", res.Result)
	assert.Equal(t, "error", rec.lastPoll())
}

// mark error aborts the window (single-window variant of F3).
func TestChangesPoller_MarkErrorAborts(t *testing.T) {
	now := time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC)
	lister := &fakeLister{dflt: []recPage{{page: tmdb.ChangedIDsPage{IDs: []int64{1, 2, 3}, Page: 1, TotalPages: 1}}}}
	marker := &fakeMarker{errOnCall: 1}
	store := &fakeCursorStore{cur: enrichdomain.ChangeCursor{SchemaVersion: 1, LastWindowEnd: time.Date(2026, 7, 13, 0, 0, 0, 0, time.UTC)}}
	rec := &recordingMetrics{}
	p := newTestPoller(t, ChangesPollerDeps{
		Lister: lister, Marker: marker, CursorStore: store, Metrics: rec, Clock: fixedClock(now),
	})

	res, err := p.poll(context.Background())
	require.Error(t, err)
	assert.Equal(t, "error", res.Result)
	assert.Empty(t, store.saves) // cursor NOT advanced
}

// constructor rejects missing required ports.
func TestNewChangesPoller_RequiresPorts(t *testing.T) {
	_, err := NewChangesPoller(ChangesPollerDeps{Marker: &fakeMarker{}, CursorStore: &fakeCursorStore{}})
	require.Error(t, err)
	_, err = NewChangesPoller(ChangesPollerDeps{Lister: &fakeLister{}, CursorStore: &fakeCursorStore{}})
	require.Error(t, err)
	_, err = NewChangesPoller(ChangesPollerDeps{Lister: &fakeLister{}, Marker: &fakeMarker{}})
	require.Error(t, err)
}

// exported Poll wrapper returns only the error.
func TestChangesPoller_PollWrapper(t *testing.T) {
	now := time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC)
	lister := &fakeLister{dflt: []recPage{{page: tmdb.ChangedIDsPage{IDs: []int64{1}, Page: 1, TotalPages: 1}}}}
	store := &fakeCursorStore{cur: enrichdomain.ChangeCursor{SchemaVersion: 1, LastWindowEnd: time.Date(2026, 7, 13, 0, 0, 0, 0, time.UTC)}}
	p := newTestPoller(t, ChangesPollerDeps{
		Lister: lister, Marker: &fakeMarker{rows: 1}, CursorStore: store, Clock: fixedClock(now),
	})
	require.NoError(t, p.Poll(context.Background()))
}
