package enrichment

import (
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
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
