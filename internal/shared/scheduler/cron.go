// Package scheduler is a small cron façade over robfig/cron.
//
// History: Story 211 (C-2) added the named-job registry. The
// previous shape — one scan job hard-wired into Start — is preserved
// as a thin wrapper so cmd/server's existing call site
// (Start(ctx, scanUC)) keeps working. New callers register named
// jobs and call StartRegistered.
package scheduler

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math/rand"
	"sync"
	"time"

	"github.com/robfig/cron/v3"

	"github.com/alexmorbo/seasonfill/internal/catalog/app/scan"
)

// ScanJobName is the name the existing scan job registers under.
// Exported so the SchedulerSubscriber can read its schedule via
// EntryByName for diff-skip.
const ScanJobName = "scan"

// Scheduler wraps robfig/cron.Cron with a named-job registry.
type Scheduler struct {
	cron     *cron.Cron
	logger   *slog.Logger
	jitter   time.Duration
	schedule string // legacy: the scan job's schedule (mirrored for SubScheduler.Schedule())

	mu      sync.Mutex
	entries map[string]registeredEntry
	started bool
	entryID cron.EntryID // legacy: id of the scan job, for tests that still inspect it
	runCtxV context.Context
}

type registeredEntry struct {
	schedule string
	entryID  cron.EntryID
}

// New constructs a Scheduler bound to UTC. The schedule + jitter
// arguments are retained for backwards-compat with the existing
// call site (they configure the scan job when Start(ctx, scanUC)
// is called). New callers can pass empty strings and use
// Register/StartRegistered.
//
// Prefer NewWithLocation when the timezone resolver is available —
// this entrypoint is kept only because reload tests + the legacy
// SchedulerFactory shape still depend on the 3-arg signature.
func New(schedule string, jitter time.Duration, logger *slog.Logger) *Scheduler {
	return NewWithLocation(schedule, jitter, logger, time.UTC)
}

// NewWithLocation is the timezone-aware constructor. loc is passed
// to cron.WithLocation so every registered job's schedule is
// interpreted in that timezone. loc must be non-nil; callers
// should defer to tz.Resolver.Get() which guarantees non-nil.
func NewWithLocation(schedule string, jitter time.Duration, logger *slog.Logger, loc *time.Location) *Scheduler {
	if loc == nil {
		loc = time.UTC
	}
	c := cron.New(
		cron.WithParser(cron.NewParser(
			cron.Minute|cron.Hour|cron.Dom|cron.Month|cron.Dow|cron.Descriptor,
		)),
		cron.WithLocation(loc),
	)
	return &Scheduler{
		cron:     c,
		logger:   logger,
		jitter:   jitter,
		schedule: schedule,
		entries:  make(map[string]registeredEntry),
	}
}

// Start is the legacy API. It registers the scan job under
// ScanJobName + starts the underlying cron. Subsequent calls (which
// were never legal under the old shape either) return an error.
func (s *Scheduler) Start(ctx context.Context, scanner *scan.UseCase) error {
	if err := s.Register(ScanJobName, s.schedule, func(jobCtx context.Context) {
		_, err := scanner.Run(jobCtx, scan.TriggerCron)
		if err != nil && !errors.Is(err, scan.ErrScanAlreadyRunning) {
			s.logger.ErrorContext(jobCtx, "cron scan failed",
				slog.String("error", err.Error()))
		}
	}); err != nil {
		return err
	}
	// Preserve legacy entryID surface — set it from the registered entry
	// so existing tests that read s.entryID still pass.
	s.mu.Lock()
	if e, ok := s.entries[ScanJobName]; ok {
		s.entryID = e.entryID
	}
	s.mu.Unlock()
	return s.StartRegistered(ctx)
}

// Register adds a named job. Re-registering the same name returns
// an error — the registry is build-once. The supplied function is
// called with the ctx passed to StartRegistered. Jitter is applied
// uniformly across all jobs (the existing behaviour).
func (s *Scheduler) Register(name, schedule string, fn func(context.Context)) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.started {
		return fmt.Errorf("scheduler: cannot register %q after Start", name)
	}
	if _, ok := s.entries[name]; ok {
		return fmt.Errorf("scheduler: duplicate registration %q", name)
	}
	id, err := s.cron.AddFunc(schedule, func() {
		if s.jitter > 0 {
			d := time.Duration(rand.Int63n(int64(s.jitter)*2)) - s.jitter //nolint:gosec // jitter is not a security primitive
			time.Sleep(d)
		}
		// Each job inherits StartRegistered's ctx via closure capture
		// — registered in StartRegistered below.
		fn(s.runCtx())
	})
	if err != nil {
		return fmt.Errorf("scheduler: register %q: %w", name, err)
	}
	s.entries[name] = registeredEntry{schedule: schedule, entryID: id}
	return nil
}

// StartRegistered starts the underlying cron. After this call no
// further Register() succeeds. ctx is captured for every registered
// job (via runCtx).
func (s *Scheduler) StartRegistered(ctx context.Context) error {
	s.mu.Lock()
	if s.started {
		s.mu.Unlock()
		return errors.New("scheduler: already started")
	}
	s.started = true
	s.runCtxV = ctx
	jobCount := len(s.entries)
	s.mu.Unlock()

	s.cron.Start()
	s.logger.InfoContext(ctx, "scheduler started",
		slog.Int("registered_jobs", jobCount),
		slog.String("scan_schedule", s.schedule),
	)
	return nil
}

// runCtx returns the context StartRegistered captured. Read under
// lock so jobs firing concurrently see a stable ctx.
func (s *Scheduler) runCtx() context.Context {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.runCtxV == nil {
		return context.Background()
	}
	return s.runCtxV
}

// Stop drains the cron. Same semantics as before.
func (s *Scheduler) Stop() context.Context {
	return s.cron.Stop()
}

// Schedule returns the cron expression the scheduler is currently
// bound to. Used by the reload subscriber's diff-skip path so we
// don't tear down a running cron when the schedule hasn't changed.
func (s *Scheduler) Schedule() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.schedule
}

// Jitter returns the configured jitter window. Same diff-skip use.
func (s *Scheduler) Jitter() time.Duration {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.jitter
}

// EntryByName returns the schedule registered under name (or empty
// string on miss). Test surface.
func (s *Scheduler) EntryByName(name string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if e, ok := s.entries[name]; ok {
		return e.schedule
	}
	return ""
}
