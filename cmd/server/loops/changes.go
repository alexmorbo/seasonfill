// changes.go — Wave 2 (W2-6) ticker driver for the TMDB /tv/changes firehose
// poller. Unlike refresh.go (whose RefreshScheduler owns its own RunForever
// ticker), the ChangesPoller exposes only Poll(ctx) — one tick — so THIS loop
// owns the ticker, the 2-minute boot-stagger startup delay (plan §10), and
// graceful drain on ctx cancel.
package loops

import (
	"context"
	"log/slog"
	"time"
)

// DefaultChangesInterval is the production cadence — 8h per plan §0-G8.
// interval <= 0 falls back to this so a misconfig can't tick-storm the
// firehose. (config.go already min-clamps the env to 1h; this is defence in
// depth for direct callers.)
const DefaultChangesInterval = 8 * time.Hour

// changesStartupDelay staggers the first tick 2 minutes after boot (plan §10,
// line 517): a restart-crash loop must not hammer the firehose, and the poll
// is not latency-critical. A package var (not const) so tests can shrink it
// via SetChangesStartupDelayForTest; production never mutates it.
var changesStartupDelay = 2 * time.Minute

// SetChangesStartupDelayForTest overrides the boot→first-tick delay and
// returns a restore closure. Test-only (loops unit test + cmd/server E2E);
// production never calls it.
func SetChangesStartupDelayForTest(d time.Duration) func() {
	prev := changesStartupDelay
	changesStartupDelay = d
	return func() { changesStartupDelay = prev }
}

// changesPoller is the narrow tick seam RunChanges drives.
// *appenrich.ChangesPoller satisfies it; the loops unit test passes a
// recording fake. Keeping the loop off the concrete type makes its timing /
// drain logic unit-testable without constructing a real poller + four ports.
type changesPoller interface {
	Poll(ctx context.Context) error
}

// RunChanges blocks until ctx is cancelled. It waits changesStartupDelay, runs
// the first poll, then ticks every interval. A failed poll is logged at Warn
// and the loop continues — one bad tick must not kill the poller.
func RunChanges(ctx context.Context, p changesPoller, interval time.Duration, log *slog.Logger) {
	if p == nil {
		// Defensive — wirer skipped construction (required port absent).
		// server.go already lifecycle.Go-wrapped us; log + return so the
		// goroutine drains. NOTE: server.go nil-checks the *concrete*
		// *ChangesPoller before wrapping it in this interface, so a typed-nil
		// never reaches here in production.
		if log != nil {
			log.Info("changes poller not configured; loop is a no-op")
		}
		return
	}
	if interval <= 0 {
		interval = DefaultChangesInterval
	}
	if log != nil {
		log.InfoContext(ctx, "changes poller started",
			slog.Duration("interval", interval),
			slog.Duration("startup_delay", changesStartupDelay),
		)
	}

	// Boot-stagger: delay the first tick, but honour cancel during the wait.
	select {
	case <-ctx.Done():
		if log != nil {
			log.InfoContext(ctx, "changes poller stopped")
		}
		return
	case <-time.After(changesStartupDelay):
	}

	pollOnce := func() {
		if err := p.Poll(ctx); err != nil && log != nil {
			log.WarnContext(ctx, "changes poll failed", slog.String("error", err.Error()))
		}
	}

	pollOnce() // first tick, post-startup-delay

	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			if log != nil {
				log.InfoContext(ctx, "changes poller stopped")
			}
			return
		case <-ticker.C:
			pollOnce()
		}
	}
}
