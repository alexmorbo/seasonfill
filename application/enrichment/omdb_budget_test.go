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
)

func TestBudget_ReserveDecrementsCounter(t *testing.T) {
	t.Parallel()
	g := NewOMDbBudgetGuard(5)
	assert.True(t, g.Reserve())
	assert.Equal(t, 4, g.Remaining())
}

func TestBudget_ReserveBlocksAtZero(t *testing.T) {
	t.Parallel()
	g := NewOMDbBudgetGuard(2)
	assert.True(t, g.Reserve())
	assert.True(t, g.Reserve())
	assert.False(t, g.Reserve())
	assert.Equal(t, 0, g.Remaining())
	// Subsequent calls keep returning false; counter does not go negative.
	assert.False(t, g.Reserve())
	assert.Equal(t, 0, g.Remaining())
}

func TestBudget_ResetRestoresInitial(t *testing.T) {
	t.Parallel()
	g := NewOMDbBudgetGuard(5)
	for i := 0; i < 2; i++ {
		assert.True(t, g.Reserve())
	}
	assert.Equal(t, 3, g.Remaining())
	g.Reset()
	assert.Equal(t, 5, g.Remaining())
}

func TestBudget_ConcurrentReserve_AtomicAccounting(t *testing.T) {
	t.Parallel()
	const initial = 200
	g := NewOMDbBudgetGuard(initial)
	const goroutines = 16
	const tries = 50 // total tries = 800; only first 200 should succeed
	var successes int64
	var wg sync.WaitGroup
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < tries; j++ {
				if g.Reserve() {
					atomic.AddInt64(&successes, 1)
				}
			}
		}()
	}
	wg.Wait()
	assert.Equal(t, int64(initial), atomic.LoadInt64(&successes),
		"exactly `initial` Reserve calls must succeed under contention")
	assert.Equal(t, 0, g.Remaining())
}

func TestBudget_DefaultBudget_WhenInitialZero(t *testing.T) {
	t.Parallel()
	g := NewOMDbBudgetGuard(0)
	assert.Equal(t, DefaultOMDbBudget, g.Remaining())
}

func TestBudget_DefaultBudget_WhenInitialNegative(t *testing.T) {
	t.Parallel()
	g := NewOMDbBudgetGuard(-1)
	assert.Equal(t, DefaultOMDbBudget, g.Remaining())
}

// fakeQuotaCounter is a controllable quota.QuotaCounter for the
// DB-backed budget guard tests.
type fakeQuotaCounter struct {
	mu         sync.Mutex
	rows       map[string]int
	incErr     error
	getErr     error
	resetErr   error
	resetCalls int
}

func newFakeQuotaCounter() *fakeQuotaCounter {
	return &fakeQuotaCounter{rows: make(map[string]int)}
}

func (f *fakeQuotaCounter) Increment(_ context.Context, service string, window time.Time) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.incErr != nil {
		return 0, f.incErr
	}
	k := service + "|" + window.UTC().Format(time.RFC3339)
	f.rows[k]++
	return f.rows[k], nil
}

func (f *fakeQuotaCounter) Get(_ context.Context, service string, window time.Time) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.getErr != nil {
		return 0, f.getErr
	}
	k := service + "|" + window.UTC().Format(time.RFC3339)
	return f.rows[k], nil
}

func (f *fakeQuotaCounter) Reset(_ context.Context, _ time.Time) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.resetCalls++
	return 0, f.resetErr
}

func TestBudgetDB_Reserve_StartsWithFullCap(t *testing.T) {
	t.Parallel()
	c := newFakeQuotaCounter()
	g := NewOMDbBudgetGuardDB(5, c, quietLogger(), func() time.Time {
		return time.Date(2026, 6, 14, 12, 0, 0, 0, time.UTC)
	})
	assert.True(t, g.Reserve())
	assert.Equal(t, 4, g.Remaining())
}

func TestBudgetDB_Reserve_BlocksAtCap(t *testing.T) {
	t.Parallel()
	c := newFakeQuotaCounter()
	g := NewOMDbBudgetGuardDB(2, c, quietLogger(), func() time.Time {
		return time.Date(2026, 6, 14, 12, 0, 0, 0, time.UTC)
	})
	assert.True(t, g.Reserve())
	assert.True(t, g.Reserve())
	assert.False(t, g.Reserve(), "third Reserve denied — count(3) > cap(2)")
	assert.Equal(t, 0, g.Remaining())
}

func TestBudgetDB_Reserve_SurvivesProcessRestart(t *testing.T) {
	t.Parallel()
	c := newFakeQuotaCounter()
	clock := func() time.Time { return time.Date(2026, 6, 14, 12, 0, 0, 0, time.UTC) }

	// "Process 1" — consume 3 slots.
	g1 := NewOMDbBudgetGuardDB(5, c, quietLogger(), clock)
	for i := 0; i < 3; i++ {
		assert.True(t, g1.Reserve())
	}
	assert.Equal(t, 2, g1.Remaining())

	// "Process 2" — fresh guard, same DB. Remaining must NOT reset to 5.
	g2 := NewOMDbBudgetGuardDB(5, c, quietLogger(), clock)
	assert.Equal(t, 2, g2.Remaining(), "restart preserves count across guard instances")
}

func TestBudgetDB_Reserve_NewDayResetsImplicitly(t *testing.T) {
	t.Parallel()
	c := newFakeQuotaCounter()
	var virtual atomic.Int64
	virtual.Store(time.Date(2026, 6, 14, 12, 0, 0, 0, time.UTC).UnixNano())
	clock := func() time.Time { return time.Unix(0, virtual.Load()).UTC() }

	g := NewOMDbBudgetGuardDB(2, c, quietLogger(), clock)
	assert.True(t, g.Reserve())
	assert.True(t, g.Reserve())
	assert.False(t, g.Reserve(), "day 1 capped")

	// Advance to the next UTC day.
	virtual.Store(time.Date(2026, 6, 15, 0, 30, 0, 0, time.UTC).UnixNano())
	assert.True(t, g.Reserve(), "day 2 starts fresh (different window row)")
	assert.Equal(t, 1, g.Remaining())
}

func TestBudgetDB_Reserve_DegradesOpenOnIncrementError(t *testing.T) {
	t.Parallel()
	c := newFakeQuotaCounter()
	c.incErr = errors.New("db unreachable")
	g := NewOMDbBudgetGuardDB(5, c, quietLogger(), func() time.Time {
		return time.Date(2026, 6, 14, 12, 0, 0, 0, time.UTC)
	})
	assert.True(t, g.Reserve(), "Reserve degrades open when DB unreachable")
}

func TestBudgetDB_UsedAndCap(t *testing.T) {
	t.Parallel()
	c := newFakeQuotaCounter()
	g := NewOMDbBudgetGuardDB(10, c, quietLogger(), func() time.Time {
		return time.Date(2026, 6, 14, 12, 0, 0, 0, time.UTC)
	})
	_ = g.Reserve()
	_ = g.Reserve()
	_ = g.Reserve()
	used, capacity := g.UsedAndCap()
	assert.Equal(t, 3, used)
	assert.Equal(t, 10, capacity)
}

func TestBudgetDB_Reset_IsNoop(t *testing.T) {
	t.Parallel()
	c := newFakeQuotaCounter()
	g := NewOMDbBudgetGuardDB(5, c, quietLogger(), func() time.Time {
		return time.Date(2026, 6, 14, 12, 0, 0, 0, time.UTC)
	})
	_ = g.Reserve()
	_ = g.Reserve()
	require.Equal(t, 3, g.Remaining())
	g.Reset()
	assert.Equal(t, 3, g.Remaining(), "Reset is a no-op on the DB-backed guard")
}

// Compile-time interface check: production guard still satisfies the
// worker's OMDbBudget seam.
var _ OMDbBudget = (*OMDbBudgetGuard)(nil)
