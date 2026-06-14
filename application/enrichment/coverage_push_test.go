package enrichment

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- Dispatcher.SetOnSeriesComplete ---

func TestDispatcher_SetOnSeriesComplete_StoresAndClears(t *testing.T) {
	t.Parallel()
	d := NewDispatcher(Workers{
		SeriesHandler: func(context.Context, int64) error { return nil },
	}, quietLogger())
	// Initially nil.
	assert.Nil(t, d.onSeriesComplete.Load())

	called := int64(0)
	fn := func(id int64) { atomic.StoreInt64(&called, id) }
	d.SetOnSeriesComplete(fn)
	got := d.onSeriesComplete.Load()
	require.NotNil(t, got, "Store wrote a non-nil pointer")
	// Verify the stored function actually works.
	(*got)(42)
	assert.Equal(t, int64(42), atomic.LoadInt64(&called))

	// Clearing sets back to nil.
	d.SetOnSeriesComplete(nil)
	assert.Nil(t, d.onSeriesComplete.Load())
}

func TestDispatcher_SetOnSeriesComplete_InvokedAfterSeriesJob(t *testing.T) {
	t.Parallel()
	var doneIDs sync.Map
	d := NewDispatcher(Workers{
		SeriesHandler: func(_ context.Context, id int64) error { return nil },
	}, quietLogger())
	d.SetOnSeriesComplete(func(id int64) { doneIDs.Store(id, struct{}{}) })
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	d.Start(ctx)
	defer d.Close()
	d.Enqueue(EntitySeries, 17, PriorityHot)
	waitFor(t, time.Second, func() bool {
		_, ok := doneIDs.Load(int64(17))
		return ok
	})
}

// --- NewOMDbBudgetGuardDB nil-defaults branches ---

func TestNewOMDbBudgetGuardDB_NilClockDefaults(t *testing.T) {
	t.Parallel()
	c := newFakeQuotaCounter()
	// Pass clock=nil; constructor must install a UTC time.Now default.
	g := NewOMDbBudgetGuardDB(5, c, quietLogger(), nil)
	require.NotNil(t, g)
	// Reserve drives the clock through one branch — proves the
	// installed default works without panicking.
	assert.True(t, g.Reserve())
}

func TestNewOMDbBudgetGuardDB_NilLoggerDefaults(t *testing.T) {
	t.Parallel()
	c := newFakeQuotaCounter()
	g := NewOMDbBudgetGuardDB(5, c, nil, func() time.Time {
		return time.Date(2026, 6, 14, 12, 0, 0, 0, time.UTC)
	})
	require.NotNil(t, g)
	assert.True(t, g.Reserve())
}

func TestNewOMDbBudgetGuardDB_ZeroInitialUsesDefault(t *testing.T) {
	t.Parallel()
	c := newFakeQuotaCounter()
	g := NewOMDbBudgetGuardDB(0, c, quietLogger(), func() time.Time {
		return time.Date(2026, 6, 14, 12, 0, 0, 0, time.UTC)
	})
	_, capacity := g.UsedAndCap()
	assert.Equal(t, DefaultOMDbBudget, capacity)
}

func TestNewOMDbBudgetGuardDB_NegativeInitialUsesDefault(t *testing.T) {
	t.Parallel()
	c := newFakeQuotaCounter()
	g := NewOMDbBudgetGuardDB(-3, c, quietLogger(), func() time.Time {
		return time.Date(2026, 6, 14, 12, 0, 0, 0, time.UTC)
	})
	_, capacity := g.UsedAndCap()
	assert.Equal(t, DefaultOMDbBudget, capacity)
}

// --- NewOMDbWorker validation branches ---

func TestNewOMDbWorker_NilClientReturnsError(t *testing.T) {
	t.Parallel()
	_, err := NewOMDbWorker(OMDbWorkerDeps{
		Budget:  &fakeOMDbBudget{allow: true},
		Tx:      fakeTxr{},
		Series:  &fakeOMDbSeries{},
		SyncLog: &fakeOMDbSyncLog{},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "Client")
}

func TestNewOMDbWorker_NilBudgetReturnsError(t *testing.T) {
	t.Parallel()
	_, err := NewOMDbWorker(OMDbWorkerDeps{
		Client:  func() OMDbClient { return nil },
		Tx:      fakeTxr{},
		Series:  &fakeOMDbSeries{},
		SyncLog: &fakeOMDbSyncLog{},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "Budget")
}

func TestNewOMDbWorker_NilTxReturnsError(t *testing.T) {
	t.Parallel()
	_, err := NewOMDbWorker(OMDbWorkerDeps{
		Client:  func() OMDbClient { return nil },
		Budget:  &fakeOMDbBudget{allow: true},
		Series:  &fakeOMDbSeries{},
		SyncLog: &fakeOMDbSyncLog{},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "Transactor")
}

func TestNewOMDbWorker_NilSeriesReturnsError(t *testing.T) {
	t.Parallel()
	_, err := NewOMDbWorker(OMDbWorkerDeps{
		Client:  func() OMDbClient { return nil },
		Budget:  &fakeOMDbBudget{allow: true},
		Tx:      fakeTxr{},
		SyncLog: &fakeOMDbSyncLog{},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "repository")
}

func TestNewOMDbWorker_NilSyncLogReturnsError(t *testing.T) {
	t.Parallel()
	_, err := NewOMDbWorker(OMDbWorkerDeps{
		Client: func() OMDbClient { return nil },
		Budget: &fakeOMDbBudget{allow: true},
		Tx:     fakeTxr{},
		Series: &fakeOMDbSeries{},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "repository")
}

func TestNewOMDbWorker_NilLoggerAndClockDefault(t *testing.T) {
	t.Parallel()
	w, err := NewOMDbWorker(OMDbWorkerDeps{
		Client:  func() OMDbClient { return nil },
		Budget:  &fakeOMDbBudget{allow: true},
		Tx:      fakeTxr{},
		Series:  &fakeOMDbSeries{},
		SyncLog: &fakeOMDbSyncLog{},
		// Logger and Clock intentionally nil — should default.
	})
	require.NoError(t, err)
	require.NotNil(t, w)
	require.NotNil(t, w.deps.Logger, "Logger gets slog.Default")
	require.NotNil(t, w.deps.Clock, "Clock gets time.Now.UTC")
	// The clock function should not panic and should return a recent time.
	now := w.deps.Clock()
	assert.WithinDuration(t, time.Now(), now, 5*time.Second)
}
