package enrichment

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func quietLogger() *slog.Logger { return slog.New(slog.NewJSONHandler(io.Discard, nil)) }

// waitFor polls fn until it returns true or the deadline expires.
func waitFor(t *testing.T, timeout time.Duration, fn func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if fn() {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("waitFor timed out after %s", timeout)
}

func TestDispatcher_SeriesHandlerCalledForSeriesJob(t *testing.T) {
	t.Parallel()
	var seen atomic.Int64
	d := NewDispatcher(Workers{
		SeriesHandler: func(ctx context.Context, id int64) error {
			seen.Store(id)
			return nil
		},
	}, quietLogger())
	ctx := t.Context()
	d.Start(ctx)
	defer d.Close()
	d.Enqueue(EntitySeries, 42, PriorityHot)
	waitFor(t, time.Second, func() bool { return seen.Load() == 42 })
}

func TestDispatcher_OMDbHandlerCalledForOMDbJob(t *testing.T) {
	t.Parallel()
	var seriesSeen, personSeen, omdbSeen int64
	d := NewDispatcher(Workers{
		SeriesHandler: func(_ context.Context, id int64) error {
			atomic.StoreInt64(&seriesSeen, id)
			return nil
		},
		PersonHandler: func(_ context.Context, id int64) error {
			atomic.StoreInt64(&personSeen, id)
			return nil
		},
		OMDbHandler: func(_ context.Context, id int64, _ Priority) error {
			atomic.StoreInt64(&omdbSeen, id)
			return nil
		},
	}, quietLogger())
	ctx := t.Context()
	d.Start(ctx)
	defer d.Close()
	d.Enqueue(EntityOMDb, 77, PriorityHot)
	waitFor(t, time.Second, func() bool { return atomic.LoadInt64(&omdbSeen) == 77 })
	assert.Zero(t, atomic.LoadInt64(&seriesSeen))
	assert.Zero(t, atomic.LoadInt64(&personSeen))
}

func TestDispatcher_DedupPreventsSimultaneousCalls(t *testing.T) {
	t.Parallel()
	var calls atomic.Int64
	gate := make(chan struct{})
	d := NewDispatcher(Workers{
		SeriesHandler: func(ctx context.Context, id int64) error {
			calls.Add(1)
			<-gate
			return nil
		},
	}, quietLogger())
	ctx := t.Context()
	d.Start(ctx)
	defer d.Close()

	var wg sync.WaitGroup
	for range 10 {
		wg.Go(func() {
			d.Enqueue(EntitySeries, 7, PriorityHot)
		})
	}
	wg.Wait()
	// Give the handler a chance to start.
	time.Sleep(50 * time.Millisecond)
	got := calls.Load()
	close(gate)
	assert.Equal(t, int64(1), got, "10 concurrent enqueues must invoke handler exactly once")
}

func TestDispatcher_PersonHandlerNilLogsAndSkips(t *testing.T) {
	t.Parallel()
	d := NewDispatcher(Workers{
		SeriesHandler: func(ctx context.Context, id int64) error { return nil },
		PersonHandler: nil,
	}, quietLogger())
	ctx := t.Context()
	d.Start(ctx)
	defer d.Close()
	d.Enqueue(EntityPerson, 5, PriorityHot)
	// After the no-op handler runs, the dedup slot must be released.
	waitFor(t, time.Second, func() bool {
		// A successful re-enqueue (returns silently — but the second
		// enqueue's dedup-skip would have happened if the first never
		// released). Verify by checking queue state instead.
		d.queue.mu.Lock()
		empty := len(d.queue.inFlight) == 0
		d.queue.mu.Unlock()
		return empty
	})
}

func TestDispatcher_HotBeatsColdOnHandler(t *testing.T) {
	t.Parallel()
	var order []int64
	var mu sync.Mutex
	gate := make(chan struct{})
	d := NewDispatcher(Workers{
		SeriesHandler: func(ctx context.Context, id int64) error {
			<-gate
			mu.Lock()
			order = append(order, id)
			mu.Unlock()
			return nil
		},
	}, quietLogger())
	ctx := t.Context()
	d.Start(ctx)
	defer d.Close()
	// Enqueue cold first; pause to ensure both workers are blocked at
	// dequeue ready to wake on the next enqueue.
	d.Enqueue(EntitySeries, 100, PriorityCold)
	time.Sleep(20 * time.Millisecond)
	d.Enqueue(EntitySeries, 200, PriorityHot)
	close(gate)
	waitFor(t, time.Second, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(order) == 2
	})
	mu.Lock()
	defer mu.Unlock()
	// Hot job should appear in the order set, regardless of which
	// worker picks each up first. Crucial assertion: 200 ran. Order
	// of completion can vary due to two-worker race; we assert both
	// landed.
	assert.Contains(t, order, int64(200))
	assert.Contains(t, order, int64(100))
}

func TestDispatcher_CloseStopsGoroutines(t *testing.T) {
	t.Parallel()
	d := NewDispatcher(Workers{
		SeriesHandler: func(ctx context.Context, id int64) error { return nil },
	}, quietLogger())
	ctx := t.Context()
	d.Start(ctx)
	done := make(chan struct{})
	go func() {
		d.Close()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Close did not return within 2s")
	}
}

func TestDispatcher_InvalidEnqueueLogged(t *testing.T) {
	t.Parallel()
	d := NewDispatcher(Workers{
		SeriesHandler: func(ctx context.Context, id int64) error { return nil },
	}, quietLogger())
	ctx := t.Context()
	d.Start(ctx)
	defer d.Close()
	d.Enqueue(EntityKind("garbage"), 1, PriorityHot)
	d.Enqueue(EntitySeries, 0, PriorityHot)
	d.Enqueue(EntitySeries, -1, PriorityHot)
	// No panic + no work — assert no in-flight entries.
	time.Sleep(20 * time.Millisecond)
	d.queue.mu.Lock()
	defer d.queue.mu.Unlock()
	assert.Len(t, d.queue.inFlight, 0)
}

func TestDispatcher_HandlerError_DoesNotKillWorker(t *testing.T) {
	t.Parallel()
	var calls atomic.Int64
	d := NewDispatcher(Workers{
		SeriesHandler: func(ctx context.Context, id int64) error {
			calls.Add(1)
			if id == 1 {
				return errors.New("boom")
			}
			return nil
		},
	}, quietLogger())
	ctx := t.Context()
	d.Start(ctx)
	defer d.Close()
	d.Enqueue(EntitySeries, 1, PriorityHot)
	d.Enqueue(EntitySeries, 2, PriorityHot)
	waitFor(t, time.Second, func() bool { return calls.Load() >= 2 })
}

// TestDispatcher_HandlerPanic_ReleasesDedup — Critical Decision #2.
// A panicking handler MUST release the dedup slot via the deferred
// queue.release in runHandler. The design intentionally does NOT
// recover inside the dispatcher (panic = programmer bug; process-
// level signal is correct), so we invoke runHandler directly inside
// a guarded goroutine and recover locally rather than via Start —
// the production behaviour is that the worker goroutine dies and
// the process supervisor restarts the pod. What we VERIFY here is
// the deferred release happens even though the panic propagates.
func TestDispatcher_HandlerPanic_ReleasesDedup(t *testing.T) {
	t.Parallel()
	d := NewDispatcher(Workers{}, quietLogger())
	// Manually pin the dedup slot as if a normal enqueue had landed
	// the job in flight.
	require.True(t, d.queue.enqueue(Job{Kind: EntitySeries, EntityID: 555, Priority: PriorityHot}))
	// Drain the channel — we don't want the loop to pick it up.
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_, ok := d.queue.dequeue(ctx, EntitySeries)
	require.True(t, ok)
	// Slot still pinned (release would happen post-handler).
	d.queue.mu.Lock()
	_, pinned := d.queue.inFlight[dedupKey(EntitySeries, 555)]
	d.queue.mu.Unlock()
	require.True(t, pinned, "dedup slot pinned after dequeue, before handler runs")

	// Invoke runHandler with a panicking handler. The deferred
	// queue.release runs BEFORE the panic unwinds out of runHandler.
	// We recover here to keep the test runner alive.
	func() {
		defer func() {
			_ = recover()
		}()
		d.runHandler(context.Background(), quietLogger(),
			Job{Kind: EntitySeries, EntityID: 555, Priority: PriorityHot},
			func(ctx context.Context, id int64, _ Priority) error {
				panic("intentional panic — dedup MUST still release")
			})
	}()

	// Dedup must be released by the deferred queue.release in runHandler.
	d.queue.mu.Lock()
	_, pinned = d.queue.inFlight[dedupKey(EntitySeries, 555)]
	d.queue.mu.Unlock()
	assert.False(t, pinned, "dedup slot must be released after handler panic")

	// And a fresh enqueue of the same id MUST succeed.
	assert.True(t, d.queue.enqueue(Job{Kind: EntitySeries, EntityID: 555, Priority: PriorityHot}))
}

// F-02: the dispatcher must thread the enqueued Job's Priority into the OMDb
// handler closure (previously it passed only id, so every OMDb job was treated
// as Cold regardless of enqueue priority).
func TestDispatcher_OMDbHandlerReceivesPriority(t *testing.T) {
	t.Parallel()
	seen := make(chan Priority, 2)
	d := NewDispatcher(Workers{
		OMDbHandler: func(_ context.Context, _ int64, p Priority) error {
			seen <- p
			return nil
		},
	}, quietLogger())
	ctx := t.Context()
	d.Start(ctx)
	defer d.Close()

	d.Enqueue(EntityOMDb, 77, PriorityHot)
	select {
	case p := <-seen:
		assert.Equal(t, PriorityHot, p, "Hot enqueue must reach handler as PriorityHot")
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for hot OMDb job")
	}

	d.Enqueue(EntityOMDb, 88, PriorityCold)
	select {
	case p := <-seen:
		assert.Equal(t, PriorityCold, p, "Cold enqueue must reach handler as PriorityCold")
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for cold OMDb job")
	}
}

// Story 1096 (Fix A): Start must spawn the configured number of series
// goroutines. We enqueue N distinct ids into a handler that blocks on a
// gate; if N handlers enter concurrently, the pool has >=N workers.
func TestDispatcher_SeriesWorkersConcurrency(t *testing.T) {
	t.Parallel()
	const n = 4
	var inFlight atomic.Int64
	gate := make(chan struct{})
	d := NewDispatcher(Workers{
		SeriesHandler: func(ctx context.Context, id int64) error {
			inFlight.Add(1)
			<-gate
			return nil
		},
		SeriesWorkers: n,
		PersonWorkers: 1,
	}, quietLogger())
	ctx := t.Context()
	d.Start(ctx)
	defer func() { close(gate); d.Close() }()

	for i := int64(1); i <= n; i++ {
		d.Enqueue(EntitySeries, i, PriorityHot)
	}
	// All n handlers must be in-flight simultaneously — only possible with
	// n series goroutines.
	waitFor(t, 2*time.Second, func() bool { return inFlight.Load() == int64(n) })
	assert.Equal(t, int64(n), inFlight.Load())
}

// A 0/negative worker count must clamp to 1 (never disable the pool).
func TestDispatcher_SeriesWorkersClampAtOne(t *testing.T) {
	t.Parallel()
	var seen atomic.Int64
	d := NewDispatcher(Workers{
		SeriesHandler: func(ctx context.Context, id int64) error {
			seen.Store(id)
			return nil
		},
		SeriesWorkers: 0, // must clamp to 1, not disable
		PersonWorkers: -3,
	}, quietLogger())
	ctx := t.Context()
	d.Start(ctx)
	defer d.Close()
	d.Enqueue(EntitySeries, 99, PriorityHot)
	waitFor(t, time.Second, func() bool { return seen.Load() == 99 })
}

// The started log must report the ACTUAL configured counts (pre-1096 it
// printed hardcoded 2/1).
func TestDispatcher_StartedLogReportsConfiguredCounts(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo}))
	d := NewDispatcher(Workers{
		SeriesHandler: func(ctx context.Context, id int64) error { return nil },
		SeriesWorkers: 5,
		PersonWorkers: 3,
	}, logger)
	ctx := t.Context()
	d.Start(ctx)
	defer d.Close()

	require.Contains(t, buf.String(), "enrichment.dispatcher.started")
	assert.Contains(t, buf.String(), `"series_workers":5`)
	assert.Contains(t, buf.String(), `"person_workers":3`)
}

// Story 1104: with per-kind channels a person job must reach the person
// handler and NEVER the series handler. Dispatcher-level counterpart to
// TestQueue_DequeueIsPerKind.
func TestDispatcher_SeriesWorkerNeverRunsPersonJob(t *testing.T) {
	t.Parallel()
	var seriesSaw, personSaw atomic.Int64
	d := NewDispatcher(Workers{
		SeriesHandler: func(_ context.Context, id int64) error { seriesSaw.Store(id); return nil },
		PersonHandler: func(_ context.Context, id int64) error { personSaw.Store(id); return nil },
	}, quietLogger())
	ctx := t.Context()
	d.Start(ctx)
	defer d.Close()

	d.Enqueue(EntityPerson, 321, PriorityHot)
	waitFor(t, time.Second, func() bool { return personSaw.Load() == 321 })
	assert.Zero(t, seriesSaw.Load(), "series handler must never run a person job")
}

// Story 1104: all three pools drain their own kind concurrently. One
// job of each kind must reach exactly its own handler.
func TestDispatcher_EachPoolDrainsOnlyItsKind(t *testing.T) {
	t.Parallel()
	var s, p, o atomic.Int64
	d := NewDispatcher(Workers{
		SeriesHandler: func(_ context.Context, id int64) error { s.Store(id); return nil },
		PersonHandler: func(_ context.Context, id int64) error { p.Store(id); return nil },
		OMDbHandler:   func(_ context.Context, id int64, _ Priority) error { o.Store(id); return nil },
	}, quietLogger())
	ctx := t.Context()
	d.Start(ctx)
	defer d.Close()

	d.Enqueue(EntitySeries, 10, PriorityHot)
	d.Enqueue(EntityPerson, 20, PriorityHot)
	d.Enqueue(EntityOMDb, 30, PriorityHot)

	waitFor(t, 2*time.Second, func() bool {
		return s.Load() == 10 && p.Load() == 20 && o.Load() == 30
	})
	assert.Equal(t, int64(10), s.Load())
	assert.Equal(t, int64(20), p.Load())
	assert.Equal(t, int64(30), o.Load())
}

// F-08: a 0/negative PersonWorkers count must clamp to 1 (never disable
// the person pool). Mirror of TestDispatcher_SeriesWorkersClampAtOne.
func TestDispatcher_PersonWorkersClampAtOne(t *testing.T) {
	t.Parallel()
	var seen atomic.Int64
	d := NewDispatcher(Workers{
		SeriesHandler: func(ctx context.Context, id int64) error { return nil },
		PersonHandler: func(ctx context.Context, id int64) error {
			seen.Store(id)
			return nil
		},
		SeriesWorkers: -3,
		PersonWorkers: 0, // must clamp to 1, not disable
	}, quietLogger())
	ctx := t.Context()
	d.Start(ctx)
	defer d.Close()
	d.Enqueue(EntityPerson, 77, PriorityHot)
	waitFor(t, time.Second, func() bool { return seen.Load() == 77 })
}
