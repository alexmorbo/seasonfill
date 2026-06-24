package enrichment

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	enrichdomain "github.com/alexmorbo/seasonfill/internal/enrichment/domain/enrichment"
)

type fakeRefreshPicker struct {
	mu    sync.Mutex
	rows  []RefreshCandidate
	err   error
	calls int
}

func (f *fakeRefreshPicker) PickRefreshCandidates(_ context.Context, _ time.Time, _ enrichdomain.RefreshTTL, _ int) ([]RefreshCandidate, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	if f.err != nil {
		return nil, f.err
	}
	out := make([]RefreshCandidate, len(f.rows))
	copy(out, f.rows)
	return out, nil
}

type fakeRefreshWorker struct {
	mu      sync.Mutex
	seen    []int64
	errs    map[int64]error
	blockCh chan struct{} // when non-nil, HandleForced blocks until close
}

func (w *fakeRefreshWorker) HandleForced(ctx context.Context, id int64) error {
	if w.blockCh != nil {
		select {
		case <-w.blockCh:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	w.mu.Lock()
	w.seen = append(w.seen, id)
	w.mu.Unlock()
	if e, ok := w.errs[id]; ok {
		return e
	}
	return nil
}

type recRefreshMetrics struct {
	mu        sync.Mutex
	inc       map[string]int // key="tier:result"
	batchSize int
	ticks     int
}

func newRecRefreshMetrics() *recRefreshMetrics { return &recRefreshMetrics{inc: map[string]int{}} }

func (r *recRefreshMetrics) IncRefresh(t enrichdomain.RefreshTier, result string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.inc[t.String()+":"+result]++
}

func (r *recRefreshMetrics) ObserveBatchSize(n int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.batchSize = n
}

func (r *recRefreshMetrics) ObserveTickDuration(time.Duration) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.ticks++
}

func newRefreshSched(t *testing.T, picker RefreshPicker, worker SeriesForceRefresher, m RefreshMetrics) *RefreshScheduler {
	t.Helper()
	s, err := NewRefreshScheduler(RefreshSchedulerDeps{
		Picker:    picker,
		Worker:    worker,
		BatchSize: 50,
		TTL:       enrichdomain.DefaultRefreshTTL(),
		Metrics:   m,
		Logger:    slog.New(slog.NewTextHandler(refreshTestWriter(t), nil)),
		Clock:     func() time.Time { return time.Date(2026, 6, 25, 12, 0, 0, 0, time.UTC) },
	})
	require.NoError(t, err)
	return s
}

func TestRefreshScheduler_TierOrderingPreserved(t *testing.T) {
	t.Parallel()
	// Picker returns hot, normal, cold already ordered; assert worker
	// is called in the same order.
	picker := &fakeRefreshPicker{rows: []RefreshCandidate{
		{SeriesID: 11, Tier: enrichdomain.RefreshTierHot},
		{SeriesID: 22, Tier: enrichdomain.RefreshTierNormal},
		{SeriesID: 33, Tier: enrichdomain.RefreshTierCold},
	}}
	worker := &fakeRefreshWorker{errs: map[int64]error{}}
	m := newRecRefreshMetrics()
	s := newRefreshSched(t, picker, worker, m)

	s.Tick(context.Background())

	assert.Equal(t, []int64{11, 22, 33}, worker.seen)
	assert.Equal(t, 3, m.batchSize)
	assert.Equal(t, 1, m.inc["hot:ok"])
	assert.Equal(t, 1, m.inc["normal:ok"])
	assert.Equal(t, 1, m.inc["cold:ok"])
}

func TestRefreshScheduler_PerSeriesErrorDoesNotAbortBatch(t *testing.T) {
	t.Parallel()
	picker := &fakeRefreshPicker{rows: []RefreshCandidate{
		{SeriesID: 1, Tier: enrichdomain.RefreshTierHot},
		{SeriesID: 2, Tier: enrichdomain.RefreshTierHot},
		{SeriesID: 3, Tier: enrichdomain.RefreshTierHot},
	}}
	worker := &fakeRefreshWorker{errs: map[int64]error{2: errors.New("tmdb 503")}}
	m := newRecRefreshMetrics()
	s := newRefreshSched(t, picker, worker, m)

	s.Tick(context.Background())

	assert.Equal(t, []int64{1, 2, 3}, worker.seen)
	assert.Equal(t, 2, m.inc["hot:ok"])
	assert.Equal(t, 1, m.inc["hot:error"])
}

func TestRefreshScheduler_PickerErrorReturnsCleanly(t *testing.T) {
	t.Parallel()
	picker := &fakeRefreshPicker{err: errors.New("db down")}
	worker := &fakeRefreshWorker{}
	m := newRecRefreshMetrics()
	s := newRefreshSched(t, picker, worker, m)

	s.Tick(context.Background())

	assert.Empty(t, worker.seen, "worker should not run on picker failure")
	assert.Equal(t, 0, m.batchSize)
}

func TestRefreshScheduler_EmptyBatchSkipsWorker(t *testing.T) {
	t.Parallel()
	picker := &fakeRefreshPicker{rows: nil}
	worker := &fakeRefreshWorker{}
	m := newRecRefreshMetrics()
	s := newRefreshSched(t, picker, worker, m)

	s.Tick(context.Background())

	assert.Empty(t, worker.seen)
	assert.Equal(t, 0, m.batchSize)
	assert.Equal(t, 1, m.ticks, "tick duration observed even on empty batch")
}

func TestRefreshScheduler_InFlightTickSkipped(t *testing.T) {
	t.Parallel()
	block := make(chan struct{})
	picker := &fakeRefreshPicker{rows: []RefreshCandidate{{SeriesID: 1, Tier: enrichdomain.RefreshTierHot}}}
	worker := &fakeRefreshWorker{blockCh: block}
	m := newRecRefreshMetrics()
	s := newRefreshSched(t, picker, worker, m)

	// Tick 1 in a goroutine — will block in the worker until we close
	// the channel.
	done := make(chan struct{})
	go func() {
		s.Tick(context.Background())
		close(done)
	}()

	// Spin until tick 1 has acquired the inFlight slot (worker.seen
	// is recorded AFTER blockCh closes, so we instead poll the picker
	// call count which increments before the worker is called).
	require.Eventually(t, func() bool {
		picker.mu.Lock()
		defer picker.mu.Unlock()
		return picker.calls == 1
	}, time.Second, 5*time.Millisecond)

	// Second concurrent tick — must return immediately with no picker
	// call.
	s.Tick(context.Background())
	picker.mu.Lock()
	assert.Equal(t, 1, picker.calls, "second concurrent tick must NOT call picker")
	picker.mu.Unlock()

	// Unblock + drain.
	close(block)
	<-done
}

func TestRefreshScheduler_CtxCancelDrainsRemaining(t *testing.T) {
	t.Parallel()
	picker := &fakeRefreshPicker{rows: []RefreshCandidate{
		{SeriesID: 1, Tier: enrichdomain.RefreshTierHot},
		{SeriesID: 2, Tier: enrichdomain.RefreshTierHot},
		{SeriesID: 3, Tier: enrichdomain.RefreshTierHot},
	}}
	worker := &fakeRefreshWorker{}
	m := newRecRefreshMetrics()
	s := newRefreshSched(t, picker, worker, m)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel before tick — first iteration should bail out

	s.Tick(ctx)

	assert.Empty(t, worker.seen, "no worker call should run on cancelled ctx")
}

func TestRefreshScheduler_RunForeverImmediateFirstTick(t *testing.T) {
	t.Parallel()
	picker := &fakeRefreshPicker{rows: []RefreshCandidate{{SeriesID: 1, Tier: enrichdomain.RefreshTierHot}}}
	worker := &fakeRefreshWorker{}
	m := newRecRefreshMetrics()
	s := newRefreshSched(t, picker, worker, m)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var ran atomic.Bool
	go func() {
		s.RunForever(ctx, time.Hour)
		ran.Store(true)
	}()

	// Wait for the immediate first tick to land.
	require.Eventually(t, func() bool {
		worker.mu.Lock()
		defer worker.mu.Unlock()
		return len(worker.seen) == 1
	}, time.Second, 5*time.Millisecond)

	cancel()
	require.Eventually(t, ran.Load, time.Second, 5*time.Millisecond)
}

func TestNewRefreshScheduler_RequiresPickerAndWorker(t *testing.T) {
	t.Parallel()
	_, err := NewRefreshScheduler(RefreshSchedulerDeps{Worker: &fakeRefreshWorker{}})
	assert.ErrorContains(t, err, "Picker is required")
	_, err = NewRefreshScheduler(RefreshSchedulerDeps{Picker: &fakeRefreshPicker{}})
	assert.ErrorContains(t, err, "Worker is required")
}

func TestNewRefreshScheduler_DefaultsApply(t *testing.T) {
	t.Parallel()
	s, err := NewRefreshScheduler(RefreshSchedulerDeps{
		Picker: &fakeRefreshPicker{},
		Worker: &fakeRefreshWorker{},
	})
	require.NoError(t, err)
	assert.Equal(t, 50, s.deps.BatchSize)
	assert.Equal(t, enrichdomain.DefaultRefreshTTL(), s.deps.TTL)
	assert.NotNil(t, s.deps.Metrics)
	assert.NotNil(t, s.deps.Logger)
}

// refreshTLogger is a tiny shim that routes slog through t.Log so failing
// runs surface the scheduler's logs in test output.
type refreshTLogger struct{ t *testing.T }

func (l refreshTLogger) Write(p []byte) (int, error) { l.t.Log(string(p)); return len(p), nil }

func refreshTestWriter(t *testing.T) refreshTLogger { return refreshTLogger{t: t} }
