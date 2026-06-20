package loops

import (
	"context"
	"log/slog"
	"sync/atomic"
	"time"

	ports "github.com/alexmorbo/seasonfill/internal/shared/dataports"
)

// SweepLoop runs the cooldown-row sweep on a cadence that can be
// changed at runtime. Interval is stored as atomic nanoseconds so
// SetInterval (called from the reload fan-out) doesn't have to take
// any lock the Run loop also holds. The loop uses time.NewTimer + Reset
// rather than time.NewTicker so an in-flight Sweep + SetInterval race
// resolves cleanly: the next iteration always reads the current value.
//
// Zero / negative intervals disable the sweep: the timer is stopped and
// Run blocks on a wake channel until SetInterval restores a positive
// value. This lets operators turn the sweep off via the runtime config
// UI without restarting the pod.
type SweepLoop struct {
	repo     ports.CooldownRepository
	log      *slog.Logger
	interval atomic.Int64 // nanoseconds; <=0 means disabled
	wake     chan struct{}
}

// NewSweepLoop wires the loop. nil log → caller-owned; SweepLoop
// never substitutes slog.Default here (the production call site at
// cmd/server/server.go always passes the application logger).
func NewSweepLoop(repo ports.CooldownRepository, initial time.Duration, log *slog.Logger) *SweepLoop {
	s := &SweepLoop{
		repo: repo,
		log:  log,
		wake: make(chan struct{}, 1),
	}
	s.interval.Store(int64(initial))
	return s
}

// SetInterval updates the cadence. A non-blocking signal nudges Run so
// a sweeper currently sleeping on the old interval picks up the new
// value (or wakes from disabled state) without waiting for the old
// timer to expire.
func (s *SweepLoop) SetInterval(d time.Duration) {
	prev := time.Duration(s.interval.Swap(int64(d)))
	if prev == d {
		return
	}
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

// Interval returns the current cadence; primarily for tests.
func (s *SweepLoop) Interval() time.Duration {
	return time.Duration(s.interval.Load())
}

// Run blocks until ctx is cancelled. It must run on a goroutine
// attached to the background wait-group so graceful shutdown drains it.
func (s *SweepLoop) Run(ctx context.Context) {
	timer := time.NewTimer(time.Hour)
	if !timer.Stop() {
		<-timer.C
	}
	defer timer.Stop()

	armed := false
	for {
		d := s.Interval()
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
		case <-s.wake:
			// loop and re-read interval
		case <-timer.C:
			armed = false
			if s.Interval() <= 0 {
				continue
			}
			n, err := s.repo.Sweep(ctx, time.Now().UTC())
			if err != nil {
				s.log.ErrorContext(ctx, "cooldown sweep failed", slog.String("error", err.Error()))
				continue
			}
			if n > 0 {
				s.log.DebugContext(ctx, "cooldown sweep removed expired rows", slog.Int64("rows", n))
			}
		}
	}
}
