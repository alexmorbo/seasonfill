package scheduler

import (
	"context"
	"errors"
	"log/slog"
	"math/rand"
	"sync"
	"time"

	"github.com/robfig/cron/v3"

	"github.com/alexmorbo/seasonfill/application/scan"
)

type Scheduler struct {
	cron     *cron.Cron
	logger   *slog.Logger
	jitter   time.Duration
	schedule string
	mu       sync.Mutex
	entryID  cron.EntryID
}

func New(schedule string, jitter time.Duration, logger *slog.Logger) *Scheduler {
	c := cron.New(cron.WithParser(cron.NewParser(
		cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow | cron.Descriptor,
	)))
	return &Scheduler{
		cron:     c,
		logger:   logger,
		jitter:   jitter,
		schedule: schedule,
	}
}

func (s *Scheduler) Start(ctx context.Context, scanner *scan.UseCase) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	id, err := s.cron.AddFunc(s.schedule, func() {
		if s.jitter > 0 {
			d := time.Duration(rand.Int63n(int64(s.jitter)*2)) - s.jitter //nolint:gosec // jitter is not a security primitive
			time.Sleep(d)
		}
		_, err := scanner.Run(ctx, scan.TriggerCron)
		if err != nil && !errors.Is(err, scan.ErrScanAlreadyRunning) {
			s.logger.ErrorContext(ctx, "cron scan failed", slog.String("error", err.Error()))
		}
	})
	if err != nil {
		return err
	}
	s.entryID = id
	s.cron.Start()
	s.logger.InfoContext(ctx, "scheduler started", slog.String("schedule", s.schedule))
	return nil
}

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
