package torrentsync

import (
	"context"
	"log/slog"
	"sync/atomic"
	"time"

	"github.com/alexmorbo/seasonfill/internal/shared/domain"
	sharedports "github.com/alexmorbo/seasonfill/internal/shared/ports"
)

// DegradedInterval is the cadence the loop falls back to after
// `MaxFailures` consecutive Refresh errors (PRD §4.4). Recovery on
// the next successful Refresh restores the configured cadence.
const DegradedInterval = 5 * time.Minute

// MaxFailures is the consecutive-error threshold past which the
// loop demotes its cadence. PRD §4.4 calls for 3.
const MaxFailures = 3

// Loop owns one per-instance polling goroutine. Modelled 1:1 on
// cmd/server/regrab_loop.go's instanceLoop: atomic interval, a
// coalesced wake channel for interval re-tunes, and a stop
// function to drain on shutdown.
//
// The Loop does not own the SyncSession — it asks the use case
// on every iteration. This lets the use case rebuild a session
// after authentication drops without the loop needing to model
// the session lifecycle.
type Loop struct {
	instance domain.InstanceName
	uc       *UseCase
	logger   *slog.Logger
	now      func() time.Time

	intervalNS atomic.Int64
	configNS   atomic.Int64 // operator-set cadence; degrade derives from this
	wake       chan struct{}
	failures   atomic.Int32
	degraded   atomic.Bool
}

// NewLoop wires the loop value. Callers fill `cancel` themselves
// (cmd/server/torrentsync_loop.go owns the ctx tree).
func NewLoop(instance domain.InstanceName, uc *UseCase, configured time.Duration, logger *slog.Logger) *Loop {
	if logger == nil {
		logger = sharedports.DomainLogger(slog.Default(), "qbit")
	}
	if configured <= 0 {
		configured = 30 * time.Second
	}
	l := &Loop{
		instance: instance,
		uc:       uc,
		logger:   logger,
		now:      func() time.Time { return time.Now().UTC() },
		wake:     make(chan struct{}, 1),
	}
	l.configNS.Store(int64(configured))
	l.intervalNS.Store(int64(configured))
	return l
}

// SetInterval re-tunes the configured cadence. The wake channel
// nudges a goroutine asleep on the old timer so the new value
// takes effect within one tick instead of waiting out the old
// interval.
//
// If the loop is currently in `degraded` mode (post-3-failures),
// the new configured value is recorded but the live interval
// stays at DegradedInterval — recovery on the next successful
// Refresh promotes the live interval back to configured.
func (l *Loop) SetInterval(d time.Duration) {
	if d <= 0 {
		return
	}
	prev := time.Duration(l.configNS.Swap(int64(d)))
	if prev == d {
		return
	}
	if !l.degraded.Load() {
		l.intervalNS.Store(int64(d))
	}
	select {
	case l.wake <- struct{}{}:
	default:
	}
}

// Interval is a diagnostic accessor — cmd/server/torrentsync_loop.go
// uses it from tests to assert post-degrade cadence.
func (l *Loop) Interval() time.Duration {
	return time.Duration(l.intervalNS.Load())
}

// Degraded reports whether the loop is currently in slow-cadence
// mode. Exposed for the same reason as Interval.
func (l *Loop) Degraded() bool { return l.degraded.Load() }

// Run is the per-instance main loop. Structured identically to
// cmd/server.regrabLoop.instanceLoop.run — time.NewTimer + Reset
// on every iteration, wake channel for retunes, ctx.Done for
// shutdown. Exits when ctx is cancelled; the caller owns the
// WaitGroup for drain.
func (l *Loop) Run(ctx context.Context) {
	timer := time.NewTimer(time.Hour)
	if !timer.Stop() {
		<-timer.C
	}
	defer timer.Stop()

	// First tick is immediate so the read endpoint has live
	// data after a fresh start without waiting one full
	// interval. Restart recovery has already populated the
	// store with the last persisted snapshot by the time Run
	// is called, so the read endpoint is functional from t=0.
	l.iterate(ctx)

	armed := false
	for {
		d := time.Duration(l.intervalNS.Load())
		if d > 0 {
			if armed && !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			timer.Reset(d)
			armed = true
		}
		select {
		case <-ctx.Done():
			return
		case <-l.wake:
			// fall through; the loop top re-reads intervalNS.
		case <-timer.C:
			armed = false
			l.iterate(ctx)
		}
	}
}

// iterate runs one Refresh + persist cycle, handles failure
// degradation, and returns. Errors are logged and counted but
// never propagate — the loop must survive arbitrary qBit /
// network failures across days.
func (l *Loop) iterate(ctx context.Context) {
	err := l.uc.RunInstance(ctx, l.instance, l.now())
	if err != nil {
		streak := l.failures.Add(1)
		l.logger.WarnContext(ctx, "torrentsync_iteration_failed",
			slog.String("instance_name", string(l.instance)),
			slog.Int("streak", int(streak)),
			slog.String("outcome", "error"),
			slog.String("error", err.Error()))
		if streak >= MaxFailures && !l.degraded.Load() {
			l.degraded.Store(true)
			l.intervalNS.Store(int64(DegradedInterval))
			l.logger.WarnContext(ctx, "torrentsync_degraded",
				slog.String("instance_name", string(l.instance)),
				slog.Duration("interval", DegradedInterval),
				slog.String("outcome", "degraded"))
			select {
			case l.wake <- struct{}{}:
			default:
			}
		}
		return
	}
	if l.failures.Load() > 0 {
		l.failures.Store(0)
	}
	if l.degraded.Load() {
		l.degraded.Store(false)
		configured := time.Duration(l.configNS.Load())
		l.intervalNS.Store(int64(configured))
		l.logger.InfoContext(ctx, "torrentsync_recovered",
			slog.String("instance_name", string(l.instance)),
			slog.Duration("interval", configured),
			slog.String("outcome", "recovered"))
		select {
		case l.wake <- struct{}{}:
		default:
		}
	}
}
