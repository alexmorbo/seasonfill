package loops

import (
	"context"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/alexmorbo/seasonfill/application/regrab"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
	sharedports "github.com/alexmorbo/seasonfill/internal/shared/ports"
)

// RegrabRunner is the narrow surface RegrabLoop calls on each tick. The
// production type is *regrab.UseCase; tests inject stubs without
// pulling the full use-case.
type RegrabRunner interface {
	RunInstance(ctx context.Context, instanceName domain.InstanceName) (regrab.RunResult, error)
}

// InstanceLoopMetrics is the subset of regrab.Metrics the per-instance
// loop emits directly. Currently only the qbit_unreachable_streak gauge
// is owned at this level — the rest are owned inside RunInstance.
type InstanceLoopMetrics interface {
	SetQbitUnreachableStreak(instance domain.InstanceName, streak int)
}

// RegrabLoop owns one polling goroutine per qBit-enabled Sonarr
// instance. SwapSettings is invoked from the OnApplied fanout under
// the SonarrClientsSubscriber lock so callers cannot race against
// runtime config publishes.
//
// Each instance loop runs at its own configured cadence; loops are
// independent and isolated by goroutine boundary so a slow qBit on
// instance A never blocks instance B's poll.
//
// Lifecycle:
//   - NewRegrabLoop is called once at server boot.
//   - Start(ctx) primes the loop with the bootstrap settings + sets
//     the parent context every per-instance goroutine derives from.
//   - SwapSettings(...) is called on every runtime snapshot publish.
//     It diffs the incoming map against `loops`: removed instances
//     get their goroutine cancelled, added instances get a fresh
//     goroutine spawned under bgWG, and existing instances get the
//     interval re-tuned via SetInterval (which signals wake).
//   - When ctx is cancelled (SIGTERM), every per-instance goroutine
//     exits and bgWG drains.
type RegrabLoop struct {
	runner  RegrabRunner
	metrics InstanceLoopMetrics
	bgWG    *sync.WaitGroup
	logger  *slog.Logger
	now     func() time.Time

	mu     sync.Mutex
	loops  map[string]*instanceLoop
	parent context.Context // set by Start; never nil after that
}

// instanceLoop is the per-instance polling goroutine state. intervalNS
// is the cadence in nanoseconds (atomic so SetInterval is lock-free).
// wake is a coalesced signal channel — SetInterval drops a single
// non-blocking send so a goroutine asleep on the old timer picks up
// the new value without waiting for the old timer to expire.
type instanceLoop struct {
	name       string
	intervalNS atomic.Int64
	wake       chan struct{}
	cancel     context.CancelFunc
	streak     atomic.Int32 // consecutive qBit errors
	parent     *RegrabLoop
}

// NewRegrabLoop wires the loop owner. runner is the regrab use case,
// metrics is the production adapter (nullMetrics is acceptable for
// tests), bgWG is the process-wide drain WaitGroup so SIGTERM blocks
// on in-flight RunInstance calls.
func NewRegrabLoop(runner RegrabRunner, metrics InstanceLoopMetrics, bgWG *sync.WaitGroup, log *slog.Logger) *RegrabLoop {
	if log == nil {
		log = sharedports.DomainLogger(slog.Default(), "watchdog")
	}
	if metrics == nil {
		metrics = nullStreakMetrics{}
	}
	return &RegrabLoop{
		runner:  runner,
		metrics: metrics,
		bgWG:    bgWG,
		logger:  log,
		now:     func() time.Time { return time.Now().UTC() },
		loops:   make(map[string]*instanceLoop),
	}
}

// nullStreakMetrics is the test-only default so the constructor never
// panics when callers wire nil metrics.
type nullStreakMetrics struct{}

func (nullStreakMetrics) SetQbitUnreachableStreak(domain.InstanceName, int) {}

// Start records the parent context. Must be called before SwapSettings.
// The actual goroutines are spawned by SwapSettings on the first
// publish — there's no work to do here other than capturing the ctx.
func (l *RegrabLoop) Start(ctx context.Context) {
	l.mu.Lock()
	l.parent = ctx
	l.mu.Unlock()
}

// SwapSettings is the reload-bus entrypoint. It is called from inside
// buildOnAppliedFanout (under the SonarrClientsSubscriber lock) every
// time the runtime config publishes a new snapshot. Diff semantics:
//
//   - name in `next` but not in `loops` → spawn new goroutine
//   - name in `loops` but not in `next` → cancel + remove
//   - name in both → if interval changed, call SetInterval (signals wake)
//
// The caller must NOT pass a nil map; an empty map is the valid
// "no instances enabled" state.
func (l *RegrabLoop) SwapSettings(settings map[string]regrab.Settings) {
	l.mu.Lock()
	defer l.mu.Unlock()

	if l.parent == nil {
		// Start() not called yet — refuse to spawn goroutines without a
		// parent ctx. Defensive; production order is always Start before
		// SwapSettings (main.go enforces).
		return
	}

	// Stop loops for removed / disabled instances.
	for name, ll := range l.loops {
		s, ok := settings[name]
		if !ok || !s.Enabled || s.PollInterval <= 0 {
			ll.cancel()
			delete(l.loops, name)
			l.logger.InfoContext(l.parent, "regrab_loop_stopped",
				slog.String("instance", name))
		}
	}

	// Start / re-tune loops for present instances.
	for name, s := range settings {
		if !s.Enabled || s.PollInterval <= 0 {
			continue
		}
		if existing, ok := l.loops[name]; ok {
			existing.setInterval(s.PollInterval)
			continue
		}
		il := newInstanceLoop(name, s.PollInterval, l)
		ctx, cancel := context.WithCancel(l.parent)
		il.cancel = cancel
		l.loops[name] = il
		if l.bgWG != nil {
			l.bgWG.Add(1)
		}
		go func(loop *instanceLoop, runCtx context.Context) {
			defer func() {
				if l.bgWG != nil {
					l.bgWG.Done()
				}
			}()
			loop.run(runCtx)
		}(il, ctx)
		l.logger.InfoContext(l.parent, "regrab_loop_started",
			slog.String("instance", name),
			slog.Duration("interval", s.PollInterval))
	}
}

// active is a test/diagnostic helper — count of running per-instance
// loops at this moment.
func (l *RegrabLoop) active() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.loops)
}

// intervalOf returns the current cadence for the named instance, or 0
// if no loop is running for it. Test-only helper.
func (l *RegrabLoop) intervalOf(name string) time.Duration {
	l.mu.Lock()
	defer l.mu.Unlock()
	if ll, ok := l.loops[name]; ok {
		return time.Duration(ll.intervalNS.Load())
	}
	return 0
}

// newInstanceLoop wires a per-instance loop value. intervalNS is set;
// cancel is filled in by the caller (it derives the ctx).
func newInstanceLoop(name string, initial time.Duration, parent *RegrabLoop) *instanceLoop {
	il := &instanceLoop{
		name:   name,
		wake:   make(chan struct{}, 1),
		parent: parent,
	}
	il.intervalNS.Store(int64(initial))
	return il
}

// setInterval mirrors SweepLoop.SetInterval — atomic swap + non-
// blocking wake nudge. The check on prev == d skips the wake when the
// cadence is unchanged so a flood of identical publishes does not
// spin the goroutine.
func (il *instanceLoop) setInterval(d time.Duration) {
	prev := time.Duration(il.intervalNS.Swap(int64(d)))
	if prev == d {
		return
	}
	select {
	case il.wake <- struct{}{}:
	default:
	}
}

// run is the per-instance main loop. Structured exactly like
// SweepLoop.Run for symmetry: time.NewTimer + Reset on each iteration
// so a stale timer never fires after SetInterval changes the cadence.
func (il *instanceLoop) run(ctx context.Context) {
	timer := time.NewTimer(time.Hour)
	if !timer.Stop() {
		<-timer.C
	}
	defer timer.Stop()

	armed := false
	for {
		d := time.Duration(il.intervalNS.Load())
		if d > 0 {
			if armed && !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			timer.Reset(d)
			armed = true
		} else if armed {
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			armed = false
		}

		select {
		case <-ctx.Done():
			return
		case <-il.wake:
			// re-read interval next iteration
		case <-timer.C:
			armed = false
			if time.Duration(il.intervalNS.Load()) <= 0 {
				continue
			}
			il.iterate(ctx)
		}
	}
}

// iterate is one RunInstance call. Errors from the use case are
// logged + counted but never propagate out of the loop — the loop
// must survive arbitrary qBit / Sonarr failures.
//
// The detached writeCtx convention from D60 lives inside the
// use case, not here; this method just bridges the per-cycle ctx
// to RunInstance. We do NOT detach here because the per-instance
// ctx is already long-lived (only cancelled on SIGTERM or loop
// removal); shortening it via the request scope would surprise
// the use case.
func (il *instanceLoop) iterate(ctx context.Context) {
	instName := domain.InstanceName(il.name)
	res, err := il.parent.runner.RunInstance(ctx, instName)
	if err != nil {
		il.parent.logger.WarnContext(ctx, "regrab_iteration_failed",
			slog.String("instance", il.name),
			slog.String("error", err.Error()))
	}
	if res.QbitError != nil {
		s := il.streak.Add(1)
		il.parent.metrics.SetQbitUnreachableStreak(instName, int(s))
	} else if il.streak.Load() > 0 {
		il.streak.Store(0)
		il.parent.metrics.SetQbitUnreachableStreak(instName, 0)
	}
}
