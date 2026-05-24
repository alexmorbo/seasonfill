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
	"time"

	"github.com/VictoriaMetrics/metrics"

	"github.com/alexmorbo/seasonfill/internal/runtime"
)

// MetricReloadTotal counts successful rebuild applies, labeled by
// the stable subscriber name (`scheduler`, `sonarrClients`, ...).
const MetricReloadTotal = `seasonfill_reload_total`

// MetricReloadErrorsTotal counts rebuild failures with the same
// label set. A rebuild error never crashes the process; the
// subscriber keeps running on its previous state.
const MetricReloadErrorsTotal = `seasonfill_reload_errors_total`

// IncReloadApplied records a successful rebuild for the named
// component. `component` is one of the six stable names.
func IncReloadApplied(component string) {
	metrics.GetOrCreateCounter(
		`seasonfill_reload_total{component="` + component + `"}`).Inc()
}

// IncReloadError records a rebuild failure for the named component.
func IncReloadError(component string) {
	metrics.GetOrCreateCounter(
		`seasonfill_reload_errors_total{component="` + component + `"}`).Inc()
}

// runLoop is the shared goroutine body used by every subscriber.
// It subscribes to bus under `component`, calls `apply` for every
// received snapshot, and exits cleanly when ctx is cancelled OR
// when bus.Close() closes the channel. `apply` returning an error
// is logged + metric'd; the loop continues with the previous state.
//
// `ready`, if non-nil, is invoked synchronously immediately after
// the bus.Subscribe call completes — by the time it runs, the
// subscriber's channel is registered and a concurrent Publish will
// reach it. cmd/server uses this to barrier the boot publish.
// Subscriber unit tests may pass nil.
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
			start := time.Now()
			if err := apply(ctx, snap); err != nil {
				IncReloadError(component)
				logger.ErrorContext(ctx, "reload.failed",
					slog.String("component", component),
					slog.String("error", err.Error()),
					slog.Duration("took", time.Since(start)))
				continue
			}
			IncReloadApplied(component)
			logger.InfoContext(ctx, "reload.applied",
				slog.String("component", component),
				slog.Duration("took", time.Since(start)))
		}
	}
}
