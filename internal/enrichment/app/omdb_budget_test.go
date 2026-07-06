package enrichment

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBudget_ReserveDecrementsCounter(t *testing.T) {
	t.Parallel()
	g := NewOMDbBudgetGuard(5, 0)
	assert.True(t, g.ReserveHot())
	assert.Equal(t, 4, g.Remaining())
}

func TestBudget_ReserveBlocksAtZero(t *testing.T) {
	t.Parallel()
	g := NewOMDbBudgetGuard(2, 0)
	assert.True(t, g.ReserveHot())
	assert.True(t, g.ReserveHot())
	assert.False(t, g.ReserveHot())
	assert.Equal(t, 0, g.Remaining())
	// Subsequent calls keep returning false; counter does not go negative.
	assert.False(t, g.ReserveHot())
	assert.Equal(t, 0, g.Remaining())
}

func TestBudget_ResetRestoresInitial(t *testing.T) {
	t.Parallel()
	g := NewOMDbBudgetGuard(5, 0)
	for range 2 {
		assert.True(t, g.ReserveHot())
	}
	assert.Equal(t, 3, g.Remaining())
	g.Reset()
	assert.Equal(t, 5, g.Remaining())
}

func TestBudget_ConcurrentReserve_AtomicAccounting(t *testing.T) {
	t.Parallel()
	const initial = 200
	g := NewOMDbBudgetGuard(initial, 0)
	const goroutines = 16
	const tries = 50 // total tries = 800; only first 200 should succeed
	var successes atomic.Int64
	var wg sync.WaitGroup
	for range goroutines {
		wg.Go(func() {
			for range tries {
				if g.ReserveHot() {
					successes.Add(1)
				}
			}
		})
	}
	wg.Wait()
	assert.Equal(t, int64(initial), successes.Load(),
		"exactly `initial` Reserve calls must succeed under contention")
	assert.Equal(t, 0, g.Remaining())
}

func TestBudget_DefaultBudget_WhenInitialZero(t *testing.T) {
	t.Parallel()
	g := NewOMDbBudgetGuard(0, 0)
	assert.Equal(t, DefaultOMDbBudget, g.Remaining())
}

func TestBudget_DefaultBudget_WhenInitialNegative(t *testing.T) {
	t.Parallel()
	g := NewOMDbBudgetGuard(-1, 0)
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

// SetQuota satisfies the D-5 466c port extension. The guard does not
// call SetQuota today (the cap is set from app code, not upstream
// headers), so the no-op preserves test semantics.
func (f *fakeQuotaCounter) SetQuota(_ context.Context, _ string, _ time.Time, _ int) error {
	return nil
}

// MarkExhausted satisfies the D-5 466c port extension. The guard
// does not call MarkExhausted today, so the no-op preserves test
// semantics.
func (f *fakeQuotaCounter) MarkExhausted(_ context.Context, _ string, _ time.Time) error {
	return nil
}

func TestBudgetDB_Reserve_StartsWithFullCap(t *testing.T) {
	t.Parallel()
	c := newFakeQuotaCounter()
	g := NewOMDbBudgetGuardDB(5, 0, c, quietLogger(), func() time.Time {
		return time.Date(2026, 6, 14, 12, 0, 0, 0, time.UTC)
	})
	assert.True(t, g.ReserveHot())
	assert.Equal(t, 4, g.Remaining())
}

func TestBudgetDB_Reserve_BlocksAtCap(t *testing.T) {
	t.Parallel()
	c := newFakeQuotaCounter()
	g := NewOMDbBudgetGuardDB(2, 0, c, quietLogger(), func() time.Time {
		return time.Date(2026, 6, 14, 12, 0, 0, 0, time.UTC)
	})
	assert.True(t, g.ReserveHot())
	assert.True(t, g.ReserveHot())
	assert.False(t, g.ReserveHot(), "third Reserve denied — count(3) > cap(2)")
	assert.Equal(t, 0, g.Remaining())
}

func TestBudgetDB_Reserve_SurvivesProcessRestart(t *testing.T) {
	t.Parallel()
	c := newFakeQuotaCounter()
	clock := func() time.Time { return time.Date(2026, 6, 14, 12, 0, 0, 0, time.UTC) }

	// "Process 1" — consume 3 slots.
	g1 := NewOMDbBudgetGuardDB(5, 0, c, quietLogger(), clock)
	for range 3 {
		assert.True(t, g1.ReserveHot())
	}
	assert.Equal(t, 2, g1.Remaining())

	// "Process 2" — fresh guard, same DB. Remaining must NOT reset to 5.
	g2 := NewOMDbBudgetGuardDB(5, 0, c, quietLogger(), clock)
	assert.Equal(t, 2, g2.Remaining(), "restart preserves count across guard instances")
}

func TestBudgetDB_Reserve_NewDayResetsImplicitly(t *testing.T) {
	t.Parallel()
	c := newFakeQuotaCounter()
	var virtual atomic.Int64
	virtual.Store(time.Date(2026, 6, 14, 12, 0, 0, 0, time.UTC).UnixNano())
	clock := func() time.Time { return time.Unix(0, virtual.Load()).UTC() }

	g := NewOMDbBudgetGuardDB(2, 0, c, quietLogger(), clock)
	assert.True(t, g.ReserveHot())
	assert.True(t, g.ReserveHot())
	assert.False(t, g.ReserveHot(), "day 1 capped")

	// Advance to the next UTC day.
	virtual.Store(time.Date(2026, 6, 15, 0, 30, 0, 0, time.UTC).UnixNano())
	assert.True(t, g.ReserveHot(), "day 2 starts fresh (different window row)")
	assert.Equal(t, 1, g.Remaining())
}

func TestBudgetDB_Reserve_DegradesOpenOnIncrementError(t *testing.T) {
	t.Parallel()
	c := newFakeQuotaCounter()
	c.incErr = errors.New("db unreachable")
	g := NewOMDbBudgetGuardDB(5, 0, c, quietLogger(), func() time.Time {
		return time.Date(2026, 6, 14, 12, 0, 0, 0, time.UTC)
	})
	assert.True(t, g.ReserveHot(), "Reserve degrades open when DB unreachable")
}

func TestBudgetDB_UsedAndCap(t *testing.T) {
	t.Parallel()
	c := newFakeQuotaCounter()
	g := NewOMDbBudgetGuardDB(10, 0, c, quietLogger(), func() time.Time {
		return time.Date(2026, 6, 14, 12, 0, 0, 0, time.UTC)
	})
	_ = g.ReserveHot()
	_ = g.ReserveHot()
	_ = g.ReserveHot()
	used, capacity := g.UsedAndCap()
	assert.Equal(t, 3, used)
	assert.Equal(t, 10, capacity)
}

func TestBudgetDB_Reset_IsNoop(t *testing.T) {
	t.Parallel()
	c := newFakeQuotaCounter()
	g := NewOMDbBudgetGuardDB(5, 0, c, quietLogger(), func() time.Time {
		return time.Date(2026, 6, 14, 12, 0, 0, 0, time.UTC)
	})
	_ = g.ReserveHot()
	_ = g.ReserveHot()
	require.Equal(t, 3, g.Remaining())
	g.Reset()
	assert.Equal(t, 3, g.Remaining(), "Reset is a no-op on the DB-backed guard")
}

func TestBudget_HotSpendsIntoFloor(t *testing.T) {
	t.Parallel()
	// cap 5, floor 3 → Hot may spend all 5 down to 0.
	g := NewOMDbBudgetGuard(5, 3)
	for i := range 5 {
		assert.True(t, g.ReserveHot(), "Hot spends into the floor, call %d", i+1)
	}
	assert.Equal(t, 0, g.Remaining())
	assert.False(t, g.ReserveHot(), "Hot denied only at 0")
}

func TestBudget_ColdDeniedAtFloor_NoLeak(t *testing.T) {
	t.Parallel()
	// cap 5, floor 3 → Cold may spend the 2 above the floor, then backs off.
	g := NewOMDbBudgetGuard(5, 3)
	assert.True(t, g.ReserveCold()) // 5→4
	assert.True(t, g.ReserveCold()) // 4→3
	assert.Equal(t, 3, g.Remaining())
	assert.False(t, g.ReserveCold(), "Cold backs off at the floor")
	assert.Equal(t, 3, g.Remaining(), "denied Cold must NOT decrement (no leak)")
	// Hot still has the whole reserve to itself.
	assert.True(t, g.ReserveHot())
	assert.Equal(t, 2, g.Remaining())
}

func TestBudget_ColdSucceedsAboveFloor(t *testing.T) {
	t.Parallel()
	g := NewOMDbBudgetGuard(10, 5)
	assert.True(t, g.ReserveCold(), "remaining(10) > floor(5)")
	assert.Equal(t, 9, g.Remaining())
}

func TestBudget_ZeroFloor_ColdSpendsToZero(t *testing.T) {
	t.Parallel()
	g := NewOMDbBudgetGuard(2, 0)
	assert.True(t, g.ReserveCold())
	assert.True(t, g.ReserveCold())
	assert.False(t, g.ReserveCold(), "floor 0 → Cold behaves like Hot, denied at 0")
	assert.Equal(t, 0, g.Remaining())
}

func TestBudgetDB_ColdDeniedAtFloor_NoLeak(t *testing.T) {
	t.Parallel()
	c := newFakeQuotaCounter()
	clock := func() time.Time { return time.Date(2026, 6, 14, 12, 0, 0, 0, time.UTC) }
	// cap 5, floor 3.
	g := NewOMDbBudgetGuardDB(5, 3, c, quietLogger(), clock)
	assert.True(t, g.ReserveCold()) // used 1, remaining 4
	assert.True(t, g.ReserveCold()) // used 2, remaining 3
	assert.False(t, g.ReserveCold(), "Cold backs off at floor 3")
	assert.Equal(t, 3, g.Remaining(), "denied Cold must not Increment (no leak)")
	// Hot dips into the reserve.
	assert.True(t, g.ReserveHot())
	assert.Equal(t, 2, g.Remaining())
}

// Compile-time interface check: production guard still satisfies the
// worker's OMDbBudget seam.
var _ OMDbBudget = (*OMDbBudgetGuard)(nil)

// F-06: an in-process floor >= cap must be clamped below the cap so the Cold
// lane keeps at least one slot (ColdAvailable true at full budget) instead of
// being permanently denied.
func TestBudget_HotFloorClampedBelowCap(t *testing.T) {
	t.Parallel()

	// floor == cap → without the clamp, ColdAvailable() would be false
	// (Remaining 5 > floor 5 is false). Clamped to 4 → 5 > 4 == true.
	gEq := NewOMDbBudgetGuard(5, 5)
	assert.True(t, gEq.ColdAvailable(), "floor==cap must clamp so Cold keeps a slot")
	assert.True(t, gEq.ReserveCold(), "clamped floor must let Cold spend at least once")

	// floor > cap → same clamp.
	gGt := NewOMDbBudgetGuard(5, 100)
	assert.True(t, gGt.ColdAvailable(), "floor>cap must clamp so Cold keeps a slot")

	// Sanity: a normal floor below cap is untouched (Cold denied AT the floor).
	gOk := NewOMDbBudgetGuard(5, 3)
	assert.True(t, gOk.ReserveCold()) // 5→4
	assert.True(t, gOk.ReserveCold()) // 4→3
	assert.False(t, gOk.ReserveCold(), "unchanged floor still backs off at 3")
}

// F-06: the DB-backed constructor logs a loud WARN when the configured floor
// is clamped, so the misconfig is visible at boot.
func TestBudgetDB_HotFloorClamp_LogsWarn(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn}))
	c := newFakeQuotaCounter()
	clock := func() time.Time { return time.Date(2026, 6, 14, 12, 0, 0, 0, time.UTC) }

	g := NewOMDbBudgetGuardDB(10, 10 /* floor == cap */, c, logger, clock)

	assert.True(t, g.ColdAvailable(), "clamped DB guard keeps a Cold slot at full budget")
	logs := buf.String()
	assert.Contains(t, logs, "enrichment.omdb.budget.hot_reserve_clamped")
	assert.Contains(t, logs, "\"clamped_to\":9")
}
