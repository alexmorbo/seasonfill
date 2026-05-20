package main

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/google/uuid"
)

// scanStateReader is the subset of scan.UseCase needed by waitForScans.
type scanStateReader interface {
	IsAnyRunning() bool
	InflightScans() map[string]uuid.UUID
}

// scanAborter is the subset of repositories.ScanRepository needed by waitForScans.
type scanAborter interface {
	MarkAborted(ctx context.Context, id uuid.UUID, reason string) error
}

// drainBackground waits for all goroutines tracked by wg to exit.
// If they do not finish within timeout, it logs a warning and returns.
func drainBackground(wg *sync.WaitGroup, timeout time.Duration, log *slog.Logger) {
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(timeout):
		log.Warn("background goroutines did not exit within 10s")
	}
}

// waitForScans polls until no scans are running or grace expires.
// If scans remain after grace, it marks them aborted in the repository.
func waitForScans(ctx context.Context, uc scanStateReader, repo scanAborter, log *slog.Logger, grace time.Duration) {
	deadline := time.Now().Add(grace)
	for time.Now().Before(deadline) {
		if !uc.IsAnyRunning() {
			return
		}
		time.Sleep(500 * time.Millisecond)
	}
	if !uc.IsAnyRunning() {
		return
	}
	log.Warn("scans still in flight after grace, marking aborted")
	for inst, id := range uc.InflightScans() {
		if err := repo.MarkAborted(ctx, id, "shutdown grace exceeded"); err != nil {
			log.Error("mark aborted failed",
				slog.String("instance", inst),
				slog.String("scan_id", id.String()),
				slog.String("error", err.Error()),
			)
		}
	}
}
