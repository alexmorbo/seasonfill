package enrichment

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/internal/shared/domain"
)

type fakeScanner struct {
	ids  []domain.SeriesID
	pass atomic.Int32
	err  error
}

func (f *fakeScanner) ListMissingSyncLog(_ context.Context, _ string, _ int) ([]domain.SeriesID, error) {
	if f.err != nil {
		return nil, f.err
	}
	if f.pass.Add(1) == 1 {
		return f.ids, nil
	}
	return nil, nil
}

// ListCanonImagesCorrupted — Story 319: cold_start_test cases never
// touch the recovery path (it lives in the enrichment_wiring closure,
// not in cold_start.go), so the fake returns an empty slice.
func (f *fakeScanner) ListCanonImagesCorrupted(_ context.Context, _ int) ([]domain.SeriesID, error) {
	return nil, nil
}

// CountCanonImagesBreakdown — Story 346: cold_start_test cases never
// touch the breakdown path (same justification as ListCanonImagesCorrupted),
// so the fake returns zeroes.
func (f *fakeScanner) CountCanonImagesBreakdown(_ context.Context) (int, int, error) {
	return 0, 0, nil
}

type recordedCall struct {
	Kind     EntityKind
	ID       int64
	Priority Priority
}

type recordingDispatcher struct {
	calls []recordedCall
}

func (r *recordingDispatcher) Enqueue(k EntityKind, id int64, p Priority) {
	r.calls = append(r.calls, recordedCall{Kind: k, ID: id, Priority: p})
}

func (r *recordingDispatcher) Close() {}

func TestBackfillSeries_IdempotentAfterFirstPass(t *testing.T) {
	t.Parallel()
	scanner := &fakeScanner{ids: []domain.SeriesID{10, 20, 30}}
	d := &recordingDispatcher{}
	ctx := context.Background()

	require.NoError(t, BackfillSeries(ctx, scanner, d, quietLogger()))
	require.Len(t, d.calls, 3)
	for i, c := range d.calls {
		assert.Equal(t, EntitySeries, c.Kind)
		assert.Equal(t, PriorityCold, c.Priority)
		assert.Equal(t, int64(scanner.ids[i]), c.ID)
	}

	// Second pass: scanner returns empty (every row now has a row).
	d2 := &recordingDispatcher{}
	require.NoError(t, BackfillSeries(ctx, scanner, d2, quietLogger()))
	assert.Empty(t, d2.calls)
}

func TestBackfillSeries_ScannerError_PropagatesWrapped(t *testing.T) {
	t.Parallel()
	scanner := &fakeScanner{err: errors.New("db down")}
	d := &recordingDispatcher{}
	err := BackfillSeries(context.Background(), scanner, d, quietLogger())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cold-start scan")
	assert.Empty(t, d.calls)
}

func TestBackfillSeries_EmptyIDs_NoEnqueue(t *testing.T) {
	t.Parallel()
	scanner := &fakeScanner{ids: nil}
	d := &recordingDispatcher{}
	require.NoError(t, BackfillSeries(context.Background(), scanner, d, quietLogger()))
	assert.Empty(t, d.calls)
}

// 306 — recordingHookableDispatcher captures Enqueue calls AND lets a
// test invoke the registered OnSeriesComplete to simulate the
// dispatcher's runHandler defer firing.
type recordingHookableDispatcher struct {
	calls   []recordedCall
	onDone  func(id int64)
	closeMu sync.Mutex
}

func (r *recordingHookableDispatcher) Enqueue(k EntityKind, id int64, p Priority) {
	r.calls = append(r.calls, recordedCall{Kind: k, ID: id, Priority: p})
}

func (r *recordingHookableDispatcher) Close() {}

func (r *recordingHookableDispatcher) SetOnSeriesComplete(fn func(id int64)) {
	r.closeMu.Lock()
	defer r.closeMu.Unlock()
	r.onDone = fn
}

func (r *recordingHookableDispatcher) fire(id int64) {
	r.closeMu.Lock()
	cb := r.onDone
	r.closeMu.Unlock()
	if cb != nil {
		cb(id)
	}
}

func TestBackfillSeries_ColdStartGauge_InitAndDecrement(t *testing.T) {
	// Override the gauge setter so this test is self-contained (we
	// inspect the captured value instead of /metrics — avoids races
	// with parallel tests mutating the global VictoriaMetrics set).
	var (
		mu       sync.Mutex
		captured []int
	)
	SetColdStartGaugeForTest(func(n int) {
		mu.Lock()
		captured = append(captured, n)
		mu.Unlock()
	})
	t.Cleanup(func() { SetColdStartGaugeForTest(nil) })

	scanner := &fakeScanner{ids: []domain.SeriesID{10, 20, 30}}
	d := &recordingHookableDispatcher{}
	require.NoError(t, BackfillSeries(context.Background(), scanner, d, quietLogger()))

	// Initial publish: gauge set to 3 BEFORE the enqueue loop.
	mu.Lock()
	require.NotEmpty(t, captured, "gauge must be set at least once")
	assert.Equal(t, 3, captured[0], "initial value = len(ids)")
	mu.Unlock()
	require.Len(t, d.calls, 3)

	// Simulate the three jobs completing — gauge ticks 2, 1, 0.
	d.fire(10)
	d.fire(20)
	d.fire(30)

	mu.Lock()
	defer mu.Unlock()
	// captured ⊇ [3, 2, 1, 0]; intermediate ticks may include the
	// initial publication so we check the last 4 entries.
	require.GreaterOrEqual(t, len(captured), 4)
	last := captured[len(captured)-4:]
	assert.Equal(t, []int{3, 2, 1, 0}, last)
}

func TestBackfillSeries_ColdStartGauge_EmptyIDs_PublishesZero(t *testing.T) {
	var captured []int
	SetColdStartGaugeForTest(func(n int) { captured = append(captured, n) })
	t.Cleanup(func() { SetColdStartGaugeForTest(nil) })

	scanner := &fakeScanner{ids: nil}
	d := &recordingHookableDispatcher{}
	require.NoError(t, BackfillSeries(context.Background(), scanner, d, quietLogger()))
	assert.Equal(t, []int{0}, captured)
	assert.Empty(t, d.calls)
}

func TestBackfillSeries_ColdStartGauge_UnknownIDFire_NoDecrement(t *testing.T) {
	var captured []int
	SetColdStartGaugeForTest(func(n int) { captured = append(captured, n) })
	t.Cleanup(func() { SetColdStartGaugeForTest(nil) })

	scanner := &fakeScanner{ids: []domain.SeriesID{1, 2}}
	d := &recordingHookableDispatcher{}
	require.NoError(t, BackfillSeries(context.Background(), scanner, d, quietLogger()))

	// Fire for an id NOT owned by cold-start. Must not push the gauge
	// below the current value (which is 2).
	d.fire(999)
	// captured: [2, ...] — the trailing entries must not include a -1.
	for _, v := range captured {
		assert.GreaterOrEqual(t, v, 0)
	}
	// Last captured value is still 2 (no decrement happened).
	assert.Equal(t, 2, captured[len(captured)-1])
}

func TestBackfillSeries_LegacyRecordingDispatcher_StillWorks(t *testing.T) {
	// Regression guard: the original Dispatcher-only fake still
	// satisfies the production BackfillSeries call. Hook is skipped
	// (no SetOnSeriesComplete on this fake) — the function returns
	// nil and the call list is correct.
	scanner := &fakeScanner{ids: []domain.SeriesID{1, 2}}
	d := &recordingDispatcher{}
	require.NoError(t, BackfillSeries(context.Background(), scanner, d, quietLogger()))
	require.Len(t, d.calls, 2)
}

// ----- Story 318 — periodic re-sweep ----------------------------------

// countingScanner returns a deterministic id list on each pass and
// records how many times ListMissingSyncLog was called.
type countingScanner struct {
	mu     sync.Mutex
	idsFn  func(pass int) []domain.SeriesID
	passes int
	err    error
}

func (c *countingScanner) ListMissingSyncLog(_ context.Context, _ string, _ int) ([]domain.SeriesID, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.passes++
	if c.err != nil {
		return nil, c.err
	}
	if c.idsFn == nil {
		return nil, nil
	}
	return c.idsFn(c.passes), nil
}

func (c *countingScanner) callCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.passes
}

// ListCanonImagesCorrupted — Story 319: countingScanner exercises the
// re-sweep loop, not the recovery path; return empty.
func (c *countingScanner) ListCanonImagesCorrupted(_ context.Context, _ int) ([]domain.SeriesID, error) {
	return nil, nil
}

// CountCanonImagesBreakdown — Story 346: same justification.
func (c *countingScanner) CountCanonImagesBreakdown(_ context.Context) (int, int, error) {
	return 0, 0, nil
}

func TestRunBackfillLoop_RunsImmediatelyThenOnTick(t *testing.T) {
	t.Parallel()
	// First pass returns 2 ids; subsequent passes return 0 (cold-start
	// is done). The loop must call the scanner AT LEAST twice within
	// 300ms when ticker interval is 50ms.
	scanner := &countingScanner{
		idsFn: func(pass int) []domain.SeriesID {
			if pass == 1 {
				return []domain.SeriesID{1, 2}
			}
			return nil
		},
	}
	d := &recordingHookableDispatcher{}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan struct{})
	go func() {
		RunBackfillLoop(ctx, scanner, d, 50*time.Millisecond, quietLogger())
		close(done)
	}()

	// Wait up to 500ms for the loop to make at least 3 passes
	// (initial + 2 tick-driven).
	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		if scanner.callCount() >= 3 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	require.GreaterOrEqual(t, scanner.callCount(), 3,
		"scanner must be polled by ticker after initial sweep")
	// The initial pass enqueued 2 ids; subsequent passes are no-ops.
	require.Len(t, d.calls, 2)

	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("RunBackfillLoop did not exit on ctx cancel")
	}
}

func TestRunBackfillLoop_ScannerErrorDoesNotKillLoop(t *testing.T) {
	t.Parallel()
	// Errors must be logged + the loop must keep going. We assert
	// "kept going" by checking the scanner is invoked more than once.
	scanner := &countingScanner{err: errors.New("db down")}
	d := &recordingHookableDispatcher{}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan struct{})
	go func() {
		RunBackfillLoop(ctx, scanner, d, 30*time.Millisecond, quietLogger())
		close(done)
	}()

	deadline := time.Now().Add(300 * time.Millisecond)
	for time.Now().Before(deadline) {
		if scanner.callCount() >= 2 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	require.GreaterOrEqual(t, scanner.callCount(), 2,
		"loop must survive scanner errors and keep ticking")

	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("RunBackfillLoop did not exit on ctx cancel")
	}
}

func TestRunBackfillLoop_ZeroIntervalUsesDefault(t *testing.T) {
	t.Parallel()
	// interval <= 0 collapses to 60s. We can't wait 60s in a unit
	// test, so we assert by canceling immediately after the initial
	// synchronous sweep — the goroutine should exit cleanly and the
	// scanner should have been called exactly once.
	scanner := &countingScanner{
		idsFn: func(int) []domain.SeriesID { return nil },
	}
	d := &recordingHookableDispatcher{}

	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan struct{})
	go func() {
		RunBackfillLoop(ctx, scanner, d, 0, quietLogger())
		close(done)
	}()
	// Give the initial synchronous sweep time to run.
	time.Sleep(30 * time.Millisecond)
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("RunBackfillLoop did not exit on ctx cancel")
	}
	require.Equal(t, 1, scanner.callCount(),
		"initial sweep runs once before the ticker")
}
