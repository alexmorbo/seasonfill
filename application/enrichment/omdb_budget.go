// Package enrichment — Story 213 OMDb budget guard.
//
// OMDbBudgetGuard is a process-global counter capping OMDb calls per
// day. Initial budget is configurable; default 900 (PRD §5.5). The
// counter resets to the initial value via Reset(); the wiring layer
// schedules Reset() at 04:00 local time via the cron registry.
//
// Concurrency: Reserve() is lock-free (atomic CAS); Reset() is a
// Store under the same field. The metric callback reads via Load,
// also lock-free.
//
// Restart caveat: the counter is in-process only. A pod restart
// zeroes the counter (re-initialises to the configured budget) while
// the upstream OMDb daily-cap keeps accruing. Worst case after a
// restart-storm: the upstream returns "Daily limit reached!" and the
// worker journals outcome=auth_failed. This is documented in the
// story risk section; the budget guard is best-effort.

package enrichment

import (
	"sync/atomic"

	"github.com/VictoriaMetrics/metrics"
)

// DefaultOMDbBudget is the per-day cap PRD §5.5 prescribes. 900 with
// 100 calls of headroom under the upstream 1000/day limit.
const DefaultOMDbBudget = 900

// OMDbBudgetGuard is the production OMDbBudget impl. Construct via
// NewOMDbBudgetGuard; call Reset() at 04:00 daily.
type OMDbBudgetGuard struct {
	initial int64
	current atomic.Int64
}

// NewOMDbBudgetGuard constructs a budget seeded at initial. initial
// ≤0 falls back to DefaultOMDbBudget. The metric
// `seasonfill_omdb_quota_remaining_guess` is registered on first
// call; subsequent constructors reuse it (GetOrCreateGauge is
// idempotent on label-set).
func NewOMDbBudgetGuard(initial int) *OMDbBudgetGuard {
	if initial <= 0 {
		initial = DefaultOMDbBudget
	}
	g := &OMDbBudgetGuard{initial: int64(initial)}
	g.current.Store(int64(initial))
	metrics.GetOrCreateGauge("seasonfill_omdb_quota_remaining_guess", func() float64 {
		return float64(g.current.Load())
	})
	return g
}

// Reserve atomically decrements when the counter is >0. Returns true
// on success, false when the counter is zero (caller should skip
// without error). Lock-free CAS loop.
func (g *OMDbBudgetGuard) Reserve() bool {
	for {
		cur := g.current.Load()
		if cur <= 0 {
			return false
		}
		if g.current.CompareAndSwap(cur, cur-1) {
			return true
		}
	}
}

// Remaining returns the current counter — log / observability surface.
func (g *OMDbBudgetGuard) Remaining() int {
	return int(g.current.Load())
}

// Reset restores the counter to its initial value. Called by the
// 04:00 cron job. Logging is the caller's responsibility (the
// scheduler closure logs before calling Reset so the line carries
// the "what reset" context).
func (g *OMDbBudgetGuard) Reset() {
	g.current.Store(g.initial)
}
