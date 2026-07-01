package adapters

import (
	"context"
	"log/slog"
	"time"

	"github.com/alexmorbo/seasonfill/internal/catalog/app/scan"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
)

// librarySyncTimeout bounds the detached sonarr_sync trigger. StartInstance
// dispatches a scan run; 60s is a generous ceiling for the enqueue path.
const librarySyncTimeout = 60 * time.Second

// scanStarter is the narrow slice of scan.UseCase the trigger needs.
type scanStarter interface {
	StartInstance(parent context.Context, name string, trigger scan.Trigger, seriesIDs ...domain.SonarrSeriesID) (scan.RunResult, error)
}

// LibrarySyncTrigger satisfies seriesdetail.LibrarySyncTrigger. M-2: fire a
// best-effort, non-blocking per-series sonarr_sync refresh. scan.UseCase
// coalesces concurrent per-instance runs, so repeated stale reads during the
// refresh window do not stack scans. Story 577 / E-1-B2.
type LibrarySyncTrigger struct {
	scanUC  scanStarter
	logger  *slog.Logger
	timeout time.Duration
}

// NewLibrarySyncTrigger wires the trigger. logger nil-OK.
func NewLibrarySyncTrigger(scanUC scanStarter, logger *slog.Logger) *LibrarySyncTrigger {
	if logger == nil {
		logger = slog.Default()
	}
	return &LibrarySyncTrigger{scanUC: scanUC, logger: logger, timeout: librarySyncTimeout}
}

// TriggerSeriesSync dispatches a detached, filtered scan for one series. Returns
// immediately; the scan runs on a background goroutine with its own timeout.
func (t *LibrarySyncTrigger) TriggerSeriesSync(instanceName domain.InstanceName, sonarrSeriesID domain.SonarrSeriesID) {
	if t == nil || t.scanUC == nil {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), t.timeout)
		defer cancel()
		if _, err := t.scanUC.StartInstance(ctx, string(instanceName), scan.TriggerManual, sonarrSeriesID); err != nil {
			t.logger.WarnContext(ctx, "library_sync_trigger_failed",
				slog.String("instance", string(instanceName)),
				slog.Int("sonarr_series_id", int(sonarrSeriesID)),
				slog.String("error", err.Error()))
		}
	}()
}
