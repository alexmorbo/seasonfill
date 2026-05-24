// Package reload hosts the in-process subscribers that react to
// snapshot publishes on `runtime.Bus`. Each subscriber runs in its
// own goroutine, reads from a 1-buffered channel (latest-wins),
// and rebuilds its slice of in-memory state on every receive. All
// rebuilds are fail-open: errors increment a per-component error
// counter and the goroutine keeps serving on the previous state.
package reload

import (
	"context"
	"log/slog"
	"runtime/debug"
	"time"

	"github.com/VictoriaMetrics/metrics"

	"github.com/alexmorbo/seasonfill/internal/runtime"
)

const MetricReloadTotal = `seasonfill_reload_total`
const MetricReloadErrorsTotal = `seasonfill_reload_errors_total`

func IncReloadApplied(component string) {
	metrics.GetOrCreateCounter(
		`seasonfill_reload_total{component="` + component + `"}`).Inc()
}

func IncReloadError(component string) {
	metrics.GetOrCreateCounter(
		`seasonfill_reload_errors_total{component="` + component + `"}`).Inc()
}

func runLoop(
	ctx context.Context,
	bus *runtime.Bus,
	component string,
	logger *slog.Logger,
	apply func(context.Context, runtime.Snapshot) error,
	ready func(),
) {
	var opts []runtime.SubscribeOption
	if ready != nil {
		opts = append(opts, runtime.WithReady(ready))
	}
	ch := bus.Subscribe(component, opts...)
	for {
		select {
		case <-ctx.Done():
			bus.Unsubscribe(component)
			return
		case snap, ok := <-ch:
			if !ok {
				return
			}
			runApplyOnce(ctx, component, logger, apply, snap)
		}
	}
}

// runApplyOnce isolates a single apply invocation so a panic in user
// code (factory closures, gin SetTrustedProxies, scheduler.Replace) cannot
// kill the subscriber goroutine. Fail-open contract: panic → error metric +
// stack log; loop continues on the previous state.
func runApplyOnce(
	ctx context.Context,
	component string,
	logger *slog.Logger,
	apply func(context.Context, runtime.Snapshot) error,
	snap runtime.Snapshot,
) {
	start := time.Now()
	defer func() {
		if r := recover(); r != nil {
			IncReloadError(component)
			logger.ErrorContext(ctx, "reload.panic",
				slog.String("event", "reload.panic"),
				slog.String("component", component),
				slog.Any("recover", r),
				slog.String("stack", string(debug.Stack())),
				slog.Duration("took", time.Since(start)))
		}
	}()
	if err := apply(ctx, snap); err != nil {
		IncReloadError(component)
		logger.ErrorContext(ctx, "reload.failed",
			slog.String("component", component),
			slog.String("error", err.Error()),
			slog.Duration("took", time.Since(start)))
		return
	}
	IncReloadApplied(component)
	logger.InfoContext(ctx, "reload.applied",
		slog.String("component", component),
		slog.Duration("took", time.Since(start)))
}
