package loops

import (
	"context"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/alexmorbo/seasonfill/internal/shared/domain"
	sharedports "github.com/alexmorbo/seasonfill/internal/shared/ports"
	"github.com/alexmorbo/seasonfill/internal/watchdog/app/regrab"
)

// RegrabRunner is the narrow surface RegrabLoop calls on each tick. The
// production type is *regrab.UseCase; tests inject stubs without
// pulling the full use-case.
type RegrabRunner interface {
	RunInstance(ctx context.Context, instanceName domain.InstanceName) (regrab.RunResult, error)
}

// InstanceLoopMetrics is the subset of regrab.Metrics the per-instance
// loop emits directly. The qbit_unreachable_streak gauge plus the
// per-cycle regrab_candidates gauge (story 479b) are owned at this
// level; the rest are owned inside RunInstance.
type InstanceLoopMetrics interface {
	SetQbitUnreachableStreak(instance domain.InstanceName, streak int)
	// SetRegrabCandidates publishes the count of unregistered torrents
	// detected on the last completed RunInstance cycle (story 479b).
	// Sourced from regrab.RunResult.UnregisteredCount.
	SetRegrabCandidates(instance domain.InstanceName, count int)
	// SetRegrabUnresolvedInstance publishes 1 while an instance present in
	// qbit_settings is skipped because regrab does not support its arr
	// type, and 0 once it stops being skipped (ADR-0025 F2). It lives on
	// THIS interface rather than on a second narrow one because it is the
	// same shape of signal as its two neighbours — a per-instance gauge
	// emitted by the loop itself, backed by the same single production
	// implementation (observability.WatchdogMetricsAdapter).
	SetRegrabUnresolvedInstance(instance domain.InstanceName, value int)
}

// InstanceTypeSource hands the loop a name -> arr_instance.type snapshot so
// it can apply regrab.SupportedInstanceTypes (ADR-0025 F2). Declared here,
// consumer-side; the production implementation is
// adapters.RegrabInstanceTypes, backed by the sonarr + radarr instance
// holders.
//
// Map-at-once rather than per-name lookup on purpose: SwapSettings walks
// every name under l.mu, and the holders return a DEFENSIVE COPY of the
// whole map on each Load (cmd/server/adapters/instance_map_holder.go:42-48),
// so a per-name resolver would copy the map once per instance.
//
// A nil or empty return is legal and means "types unknown" — the loop then
// spawns everything, exactly as it did before F2.
type InstanceTypeSource interface {
	InstanceTypes() map[string]string
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
	runner        RegrabRunner
	metrics       InstanceLoopMetrics
	instanceTypes InstanceTypeSource // nil-OK; nil => every type supported
	bgWG          *sync.WaitGroup
	logger        *slog.Logger
	now           func() time.Time

	mu    sync.Mutex
	loops map[string]*instanceLoop
	// skipped remembers which instances were already logged as
	// unsupported, so the INFO is written once per TRANSITION instead of
	// once per snapshot. Reload snapshots are published on every instance
	// edit from the UI plus on the ADR-0023 F4 qbit-settings write path;
	// logging on each of them would just swap the old 30-minute WARN flood
	// for a new one. Guarded by the SAME mu as loops: "who is served and
	// who is skipped" is one invariant, and a second mutex would let the
	// two halves drift between swaps.
	skipped map[string]struct{}
	parent  context.Context // set by Start; never nil after that
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
		skipped: make(map[string]struct{}),
	}
}

// nullStreakMetrics is the test-only default so the constructor never
// panics when callers wire nil metrics.
type nullStreakMetrics struct{}

func (nullStreakMetrics) SetQbitUnreachableStreak(domain.InstanceName, int)    {}
func (nullStreakMetrics) SetRegrabCandidates(domain.InstanceName, int)         {}
func (nullStreakMetrics) SetRegrabUnresolvedInstance(domain.InstanceName, int) {}

// WithInstanceTypes injects the arr-type resolver that backs the
// supported-type gate (ADR-0025 F2). Option method rather than a fifth
// constructor parameter: NewRegrabLoop has ten call sites in tests, and
// the codebase's established idiom for optional collaborators is
// WithXxx (WithMetrics, WithDecisions, WithRadarr, WithQbitProbe, ...).
//
// Leaving it unset is a supported configuration — the loop then treats
// every instance as supported, i.e. pre-F2 behaviour.
func (l *RegrabLoop) WithInstanceTypes(src InstanceTypeSource) *RegrabLoop {
	l.mu.Lock()
	l.instanceTypes = src
	l.mu.Unlock()
	return l
}

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
// ADR-0025 F2 adds one more arm: a name whose arr type regrab does not
// support is never spawned (and an already-running loop for it is torn
// down, because an instance CAN change type). The reaction is a gauge plus
// a single INFO — never a panic. This method runs on EVERY reload
// snapshot, so a panic here would take production down on any instance
// edit from the UI; and an orphan qbit_settings row is impossible anyway
// (FK CASCADE qbit_settings.instance_name → arr_instance.name, see
// buildQbitSettingsTable in infrastructure/database/schema/schema.go).
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

	// name -> resolved arr type, for the instances regrab must NOT serve.
	unsupported := l.unsupportedLocked(settings)

	// Forget instances that are no longer skipped — they left the settings
	// map, got disabled, or their type became supported/unknown. Resetting
	// the gauge to 0 rather than leaving it at 1 is what keeps the ADR-0025
	// F4 alert rule (seasonfill_regrab_unresolved_instance > 0) from
	// latching forever; it mirrors the streak gauge's recovery write.
	for name := range l.skipped {
		if _, still := unsupported[name]; still {
			continue
		}
		delete(l.skipped, name)
		l.metrics.SetRegrabUnresolvedInstance(domain.InstanceName(name), 0)
	}

	// Stop loops for removed / disabled instances, and for any instance
	// whose type regrab no longer supports (an instance CAN change type).
	for name, ll := range l.loops {
		s, ok := settings[name]
		_, bad := unsupported[name]
		if !ok || !s.Enabled || s.PollInterval <= 0 || bad {
			ll.cancel()
			delete(l.loops, name)
			l.logger.InfoContext(l.parent, "regrab_loop_stopped",
				slog.String("instance", name))
		}
	}

	// Start / re-tune loops for present instances.
	for name, s := range settings {
		if typ, bad := unsupported[name]; bad {
			if _, logged := l.skipped[name]; !logged {
				l.skipped[name] = struct{}{}
				l.logger.InfoContext(l.parent, "regrab_skipped_unsupported_type",
					slog.String("instance", name),
					slog.String("instance_type", typ),
					slog.String("supported_types",
						strings.Join(regrab.SupportedInstanceTypes, ",")))
			}
			// Re-affirmed on every swap: a gauge write is idempotent and
			// silent, unlike the log above.
			l.metrics.SetRegrabUnresolvedInstance(domain.InstanceName(name), 1)
			continue
		}
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

// unsupportedLocked returns instance name -> resolved arr type for every
// name in `settings` that regrab must NOT run against. Returning the type
// (rather than a bool set) lets the skip log name the offending type without
// asking the resolver a second time — each ask copies the whole holder map.
// Caller holds l.mu.
//
// ★ FAIL-OPEN is the entire contract of this helper. A name lands in the
// result ONLY when its arr type is explicitly KNOWN and explicitly NOT in
// regrab.SupportedInstanceTypes. A nil resolver, an empty snapshot, an
// unknown name or an empty type all mean "supported", i.e. behave exactly
// as the code did before ADR-0025 F2.
//
// The asymmetry is deliberate and load-bearing. Re-grabbing dead season
// torrents is seasonfill's core value on top of Sonarr; a boot-order race
// that made the type source look empty must degrade into "spawn the loop
// and maybe log one WARN", never into "silently disable regrab in
// production for the homelab instance".
//
// Disabled / zero-interval rows are skipped up front: they were never
// going to spawn a loop, so flagging them would raise a gauge and write a
// log line about work that was not going to happen.
func (l *RegrabLoop) unsupportedLocked(settings map[string]regrab.Settings) map[string]string {
	out := make(map[string]string)
	if l.instanceTypes == nil {
		return out // fail-open: no resolver wired
	}
	types := l.instanceTypes.InstanceTypes()
	if len(types) == 0 {
		return out // fail-open: nothing resolved yet
	}
	for name, s := range settings {
		if !s.Enabled || s.PollInterval <= 0 {
			continue
		}
		t, known := types[name]
		if !known || t == "" {
			continue // fail-open: type not resolvable
		}
		if regrab.SupportsInstanceType(t) {
			continue
		}
		out[name] = t
	}
	return out
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

// skippedNames is a test/diagnostic helper — the instances currently
// suppressed by the ADR-0025 F2 supported-type gate.
func (l *RegrabLoop) skippedNames() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make([]string, 0, len(l.skipped))
	for name := range l.skipped {
		out = append(out, name)
	}
	slices.Sort(out)
	return out
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

	// First tick is immediate so a freshly-enabled qBit instance
	// (operator just saved Settings → qBittorrent OR toggled
	// enabled=true) doesn't wait PollInterval (5-30 min typical)
	// before the first watchdog pass. Restart recovery has already
	// populated persistent state (cooldown table + grab_audit) by
	// the time run is called, so this iterate is idempotent —
	// repeated grabs for the same series/season are absorbed by the
	// cooldown table. Mirrors torrentsync.Loop.Run line 118.
	// Story 477 (B-30).
	il.iterate(ctx)

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
	// Story 479b — per-cycle gauge of unregistered candidates. Even
	// when the use case errored we publish whatever count was
	// computed before the failure (UnregisteredCount stays at its
	// zero value on early-return; the gauge reads as 0 which is the
	// correct "nothing seen" semantic).
	il.parent.metrics.SetRegrabCandidates(instName, res.UnregisteredCount)
}
