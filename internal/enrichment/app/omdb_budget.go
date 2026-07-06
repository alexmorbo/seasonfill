// Package enrichment — Story 213 OMDb budget guard.
//
// OMDbBudgetGuard caps OMDb calls per day. Story 305 made the
// underlying counter DB-backed via the quota.QuotaCounter port so
// pod restarts no longer reset the count — the upstream's daily
// reset boundary (UTC midnight) is the single source of truth.
//
// Window: daily UTC. OMDb itself resets at UTC midnight, so the
// guard mirrors that — see internal/runtime/quota.Daily(t, time.UTC).
//
// Backwards compat: the legacy in-process Reserve/Remaining/Reset
// contract is preserved so the OMDb worker (which depends on the
// OMDbBudget interface in omdb_worker.go) does not need changes.
//
// Concurrency: Reserve and Remaining call into the DB on every
// hit. OMDb is rate-limited to ≤900 calls/day so the QPS is ≤1/m
// at peak — the DB round-trip is invisible vs. the OMDb HTTP call
// it guards.
//
// Restart behaviour (the bug Story 305 fixes): after pod restart
// the counter is rehydrated from the DB row for the current UTC
// day. No more "300 free calls after every restart" loophole.

package enrichment

import (
	"context"
	"log/slog"
	"sync/atomic"
	"time"

	"github.com/VictoriaMetrics/metrics"

	"github.com/alexmorbo/seasonfill/internal/runtime/quota"
	sharedports "github.com/alexmorbo/seasonfill/internal/shared/ports"
)

// DefaultOMDbBudget is the per-day cap PRD §5.5 prescribes. 900 with
// 100 calls of headroom under the upstream 1000/day limit.
const DefaultOMDbBudget = 900

// DefaultOMDbHotReserve is the W18-9 Hot-lane floor: Cold reservations
// back off once remaining <= this value, leaving headroom for on-view
// (Hot) fetches. 200 of 1000 (20%). Env-tunable via the wiring.
const DefaultOMDbHotReserve = 200

// OMDbServiceName is the canonical service identifier used in the
// quota_state table and the `service` metric label. Exported so the
// metrics exporter goroutine + the GC sweeper can name it without
// stringly-coupling.
const OMDbServiceName = "omdb"

// quotaClock is package-injectable for tests.
type quotaClock func() time.Time

// OMDbBudgetGuard is the production OMDbBudget impl. Construct via
// NewOMDbBudgetGuard (in-process fallback) or NewOMDbBudgetGuardDB
// (DB-backed, the production wiring).
type OMDbBudgetGuard struct {
	initial int64
	// hotFloor is the W18-9 Cold cutoff: ReserveCold denies while
	// remaining <= hotFloor; ReserveHot ignores it (spends to 0).
	hotFloor int
	// fallback is consulted when counter is nil (the OMDbBudgetGuard
	// constructed by the legacy in-process constructor). When counter
	// is set, fallback is unused.
	fallback atomic.Int64
	counter  quota.QuotaCounter
	clock    quotaClock
	logger   *slog.Logger
}

// NewOMDbBudgetGuard preserves the legacy in-process constructor.
// Storage is in-process atomic (NOT DB-backed). hotFloor is the W18-9
// Cold cutoff (negative → 0 = no floor).
func NewOMDbBudgetGuard(initial, hotFloor int) *OMDbBudgetGuard {
	if initial <= 0 {
		initial = DefaultOMDbBudget
	}
	if hotFloor < 0 {
		hotFloor = 0
	}
	g := &OMDbBudgetGuard{
		initial:  int64(initial),
		hotFloor: hotFloor,
		clock:    func() time.Time { return time.Now().UTC() },
		logger:   sharedports.DomainLogger(slog.Default(), "omdb"),
	}
	g.fallback.Store(int64(initial))
	metrics.GetOrCreateGauge("seasonfill_omdb_quota_remaining_guess", func() float64 {
		return float64(g.Remaining())
	})
	metrics.GetOrCreateGauge("seasonfill_omdb_quota_hot_reserve", func() float64 {
		return float64(g.hotFloor)
	})
	return g
}

// NewOMDbBudgetGuardDB constructs the production guard backed by a
// DB-persisted QuotaCounter. `initial` is the per-day cap (defaults to
// DefaultOMDbBudget when ≤0); `hotFloor` is the W18-9 Cold cutoff
// (negative → 0); `counter` is the durable store. Pass clock=nil to use
// time.Now().UTC().
func NewOMDbBudgetGuardDB(initial, hotFloor int, counter quota.QuotaCounter, logger *slog.Logger, clock func() time.Time) *OMDbBudgetGuard {
	if initial <= 0 {
		initial = DefaultOMDbBudget
	}
	if hotFloor < 0 {
		hotFloor = 0
	}
	if clock == nil {
		clock = func() time.Time { return time.Now().UTC() }
	}
	if logger == nil {
		logger = sharedports.DomainLogger(slog.Default(), "omdb")
	}
	g := &OMDbBudgetGuard{
		initial:  int64(initial),
		hotFloor: hotFloor,
		counter:  counter,
		clock:    clock,
		logger:   logger,
	}
	metrics.GetOrCreateGauge("seasonfill_omdb_quota_remaining_guess", func() float64 {
		return float64(g.Remaining())
	})
	metrics.GetOrCreateGauge(`seasonfill_external_service_quota_used{service="omdb"}`, func() float64 {
		used, _ := g.UsedAndCap()
		return float64(used)
	})
	metrics.GetOrCreateGauge("seasonfill_omdb_quota_hot_reserve", func() float64 {
		return float64(g.hotFloor)
	})
	return g
}

// currentWindow returns the daily UTC window for the current clock.
// OMDb's reset boundary is UTC midnight so the call is parameterless.
func (g *OMDbBudgetGuard) currentWindow() time.Time {
	return quota.Daily(g.clock(), time.UTC)
}

// ReserveHot consumes one slot when any remain (down to 0). On-view path.
func (g *OMDbBudgetGuard) ReserveHot() bool {
	return g.reserve()
}

// ReserveCold consumes one slot only while remaining stays ABOVE the Hot
// floor. Checks BEFORE spending so a denial never decrements (no leak).
// Under concurrency the floor is soft (two Colds racing at floor+1 can
// both pass) — same phantom-overshoot tolerance as reserve()'s DB path;
// OMDb QPS ≤1/min makes it immaterial.
func (g *OMDbBudgetGuard) ReserveCold() bool {
	if g.Remaining() <= g.hotFloor {
		return false
	}
	return g.reserve()
}

// ColdAvailable reports whether a Cold reservation would currently succeed
// (remaining ABOVE the Hot floor) WITHOUT consuming a slot. W18-8 uses it as a
// non-consuming pre-check before enqueuing an OMDb Cold job on the imdb_id
// null→value transition. Advisory: the actual spend still flows through
// ReserveCold in the worker (no double-spend); the floor is soft under
// concurrency (same tolerance ReserveCold documents).
func (g *OMDbBudgetGuard) ColdAvailable() bool {
	return g.Remaining() > g.hotFloor
}

// reserve atomically consumes one slot from the daily budget when
// available. Returns true on success, false when the daily cap has been
// hit. (Formerly the exported Reserve; W18-9 made it private behind the
// lane methods.)
//
// DB-backed path: Increment is "INSERT ... ON CONFLICT DO UPDATE"
// which returns the post-update count. The cap comparison happens
// AFTER the bump — this means one "phantom" call past the cap can
// occur in a multi-pod race (pod A bumps to 900, pod B bumps to
// 901 in parallel). Worst case: the OMDb upstream returns
// "Daily limit reached!" once per restart, which Story 213's
// auth_failed dispatch already handles gracefully.
func (g *OMDbBudgetGuard) reserve() bool {
	if g.counter == nil {
		// Legacy in-process path — Reserve via CAS loop on fallback.
		for {
			cur := g.fallback.Load()
			if cur <= 0 {
				return false
			}
			if g.fallback.CompareAndSwap(cur, cur-1) {
				return true
			}
		}
	}

	w := g.currentWindow()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	n, err := g.counter.Increment(ctx, OMDbServiceName, w)
	if err != nil {
		// DB transient — degrade open. The next Reserve will retry;
		// the auth_failed enrichment_errors entry is the upstream-enforced
		// failsafe.
		g.logger.WarnContext(ctx, "enrichment.omdb.budget.increment_failed",
			slog.String("error", err.Error()))
		return true
	}
	return int64(n) <= g.initial
}

// Remaining estimates the current remaining headroom for the daily
// window. Used by log + the legacy gauge. Returns DefaultOMDbBudget
// when the DB read fails (degrade open — better to under-report
// pressure than to spuriously trip the OMDbDailyBatch logging).
func (g *OMDbBudgetGuard) Remaining() int {
	if g.counter == nil {
		return int(g.fallback.Load())
	}
	w := g.currentWindow()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	used, err := g.counter.Get(ctx, OMDbServiceName, w)
	if err != nil {
		g.logger.WarnContext(ctx, "enrichment.omdb.budget.get_failed",
			slog.String("error", err.Error()))
		return int(g.initial)
	}
	r := int(g.initial) - used
	if r < 0 {
		return 0
	}
	return r
}

// UsedAndCap returns (used, cap) for the current window. Used by
// the generic metric exporter so the gauge value reflects calls
// CONSUMED, not "remaining headroom".
func (g *OMDbBudgetGuard) UsedAndCap() (int, int) {
	if g.counter == nil {
		used := max(int(g.initial)-int(g.fallback.Load()), 0)
		return used, int(g.initial)
	}
	w := g.currentWindow()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	used, err := g.counter.Get(ctx, OMDbServiceName, w)
	if err != nil {
		return 0, int(g.initial)
	}
	return used, int(g.initial)
}

// Reset is the no-op compatibility shim for the legacy `omdb-budget-reset`
// cron job. The DB-backed guard does not need explicit reset — the
// daily window key rotates at UTC midnight and a fresh row is
// auto-created on the next Increment. main.go REMOVES the cron
// registration; this method exists so the OMDbBudgetReset closure
// in enrichment_wiring.go keeps compiling if (future-proofing) a
// caller still needs the symbol. In-process path delegates to
// fallback.Store(initial) for back-compat.
func (g *OMDbBudgetGuard) Reset() {
	if g.counter == nil {
		g.fallback.Store(g.initial)
		return
	}
	// DB path: no-op. The window rotates naturally.
}
