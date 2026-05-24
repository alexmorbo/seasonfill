package reload

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/alexmorbo/seasonfill/application/scan"
	"github.com/alexmorbo/seasonfill/infrastructure/scheduler"
	"github.com/alexmorbo/seasonfill/internal/runtime"
)

// SchedulerFactory builds a fresh scheduler bound to the supplied
// cron expression + jitter. Production wiring passes
// `scheduler.New`. Tests pass a fake to avoid spawning real cron
// goroutines.
type SchedulerFactory func(schedule string, jitter time.Duration, logger *slog.Logger) *scheduler.Scheduler

// SchedulerSubscriber owns the live `*scheduler.Scheduler` and
// swaps it out when a new snapshot arrives with a different
// schedule/jitter/enabled tuple. The currently-running scheduler is
// also exposed via Current() for cmd/server's shutdown path.
type SchedulerSubscriber struct {
	mu      sync.Mutex
	current *scheduler.Scheduler
	enabled bool
	scanUC  *scan.UseCase
	factory SchedulerFactory
	rootCtx context.Context
	logger  *slog.Logger
}

// NewSchedulerSubscriber takes ownership of the boot-time scheduler.
// `rootCtx` is the long-lived context cmd/server passes to
// `Start(...)` — every new scheduler is started against the same
// ctx so SIGTERM tears them all down. Pass nil `boot` if the boot
// config had cron disabled.
func NewSchedulerSubscriber(rootCtx context.Context, boot *scheduler.Scheduler, scanUC *scan.UseCase, factory SchedulerFactory, logger *slog.Logger) *SchedulerSubscriber {
	if logger == nil {
		logger = slog.Default()
	}
	return &SchedulerSubscriber{
		current: boot, enabled: boot != nil,
		scanUC: scanUC, factory: factory, rootCtx: rootCtx, logger: logger,
	}
}

// Run blocks until ctx is done. Decremented from bgWG by the caller.
func (s *SchedulerSubscriber) Run(ctx context.Context, bus *runtime.Bus, ready func()) {
	runLoop(ctx, bus, "scheduler", s.logger, s.apply, ready)
}

// Current returns the active scheduler (or nil if cron is disabled
// in the live snapshot). cmd/server's graceful shutdown calls
// Current().Stop() to drain the cron job.
func (s *SchedulerSubscriber) Current() *scheduler.Scheduler {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.current
}

// apply rebuilds the live scheduler if the snapshot's cron block
// differs. Rebuild order (hot-swap):
//
//  1. diff-skip if {Enabled, Schedule, Jitter} matches.
//  2. if !Enabled → tear down old (if any), nil out, return.
//  3. factory builds `next`. Failures bubble up; old keeps running.
//  4. next.Start(...). Failures bubble up; old keeps running.
//  5. tear down old, swap pointer to next.
//
// Steps 3-4 returning early leaves s.current pointing at the OLD
// scheduler. The error reaches runLoop which counts
// seasonfill_reload_errors_total{component="scheduler"}.
func (s *SchedulerSubscriber) apply(_ context.Context, snap runtime.Snapshot) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	want := snap.Cron
	if s.matches(want) {
		return nil
	}

	// Explicit-disable branch: just stop the old one. No factory call.
	if !want.Enabled {
		if s.current != nil {
			s.gracefulStop(s.current)
			s.current = nil
		}
		s.enabled = false
		return nil
	}

	// Build + start the NEW scheduler BEFORE tearing down the old.
	// If either step fails the old one keeps ticking; the error
	// surfaces through runLoop's metric.
	next := s.factory(want.Schedule, want.Jitter, s.logger)
	if next == nil {
		return fmt.Errorf("scheduler factory returned nil")
	}
	if err := next.Start(s.rootCtx, s.scanUC); err != nil {
		return fmt.Errorf("start new scheduler: %w", err)
	}

	// New is alive — now tear down old.
	old := s.current
	s.current = next
	s.enabled = true
	if old != nil {
		s.gracefulStop(old)
	}
	return nil
}

// gracefulStop waits up to 5s for the cron's in-flight job (if any)
// to drain. Timeout is fail-open — caller proceeds even if the job
// is still running, matching the pre-028h-2 behaviour.
func (s *SchedulerSubscriber) gracefulStop(sched *scheduler.Scheduler) {
	stopCtx := sched.Stop()
	select {
	case <-stopCtx.Done():
	case <-time.After(5 * time.Second):
		s.logger.Warn("scheduler stop timed out — proceeding with rebuild")
	}
}

func (s *SchedulerSubscriber) matches(want runtime.CronSnapshot) bool {
	if s.enabled != want.Enabled {
		return false
	}
	if !want.Enabled {
		// Both disabled → still a match.
		return true
	}
	if s.current == nil {
		return false
	}
	return s.current.Schedule() == want.Schedule &&
		s.current.Jitter() == want.Jitter
}
