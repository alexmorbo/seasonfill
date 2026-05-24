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
func (s *SchedulerSubscriber) Run(ctx context.Context, bus *runtime.Bus) {
	runLoop(ctx, bus, "scheduler", s.logger, s.apply)
}

// Current returns the active scheduler (or nil if cron is disabled
// in the live snapshot). cmd/server's graceful shutdown calls
// Current().Stop() to drain the cron job.
func (s *SchedulerSubscriber) Current() *scheduler.Scheduler {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.current
}

func (s *SchedulerSubscriber) apply(_ context.Context, snap runtime.Snapshot) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	want := snap.Cron
	// Diff-skip: identical {Enabled, Schedule, Jitter} → no-op.
	if s.matches(want) {
		return nil
	}
	// Tear down the old one (if any) before installing the new.
	if s.current != nil {
		stopCtx := s.current.Stop()
		select {
		case <-stopCtx.Done():
		case <-time.After(5 * time.Second):
			s.logger.Warn("scheduler stop timed out — proceeding with rebuild")
		}
		s.current = nil
	}
	s.enabled = want.Enabled
	if !want.Enabled {
		return nil
	}
	next := s.factory(want.Schedule, want.Jitter, s.logger)
	if err := next.Start(s.rootCtx, s.scanUC); err != nil {
		// Surface the error so runLoop counts it. The previous
		// scheduler is already torn down — fail-open means cron
		// pauses until the next valid snapshot arrives.
		return fmt.Errorf("start new scheduler: %w", err)
	}
	s.current = next
	return nil
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
