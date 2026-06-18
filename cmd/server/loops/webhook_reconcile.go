package loops

import (
	"context"
	"errors"
	"log/slog"
	"sync/atomic"
	"time"

	"github.com/alexmorbo/seasonfill/application/scan"
	"github.com/alexmorbo/seasonfill/application/webhookinstall"
	"github.com/alexmorbo/seasonfill/internal/observability"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
	sharedports "github.com/alexmorbo/seasonfill/internal/shared/ports"
)

const (
	defaultWebhookReconcileTickInterval = 5 * time.Minute
	webhookReconcilePerInstanceTimeout  = 3 * time.Second
	webhookReconcileLogComponent        = "webhook_reconcile_loop"
)

// Narrow surfaces the loop needs — production types are
// *webhookinstall.Reconciler, *webhookinstall.StatusCache, and
// instanceMapHolder.load; tests substitute fakes.
type WebhookReconcileReconciler interface {
	Reconcile(ctx context.Context, instanceName string) (webhookinstall.Status, error)
}

type WebhookReconcileStatusReader interface {
	Get(name string) (webhookinstall.Status, bool)
}

type WebhookReconcileInstanceLister func() map[string]scan.Instance

// WebhookReconcileLoop is the safety-net background reconciler. Boot
// once; Run(ctx) blocks until ctx is cancelled. Each tick walks the
// instance map and calls Reconcile per-instance (3 s timeout), skipping
// fresh / disabled / backoff'd entries. Errors are logged WARN and
// swallowed — the loop must NEVER fail-fast.
type WebhookReconcileLoop struct {
	reconciler WebhookReconcileReconciler
	cache      WebhookReconcileStatusReader
	instances  WebhookReconcileInstanceLister
	log        *slog.Logger

	tickIntervalNS atomic.Int64 // nanoseconds; <=0 → use default
	wake           chan struct{}
	now            func() time.Time
}

// NewWebhookReconcileLoop wires the loop. Nil logger → slog.Default.
func NewWebhookReconcileLoop(
	reconciler WebhookReconcileReconciler,
	cache WebhookReconcileStatusReader,
	instances WebhookReconcileInstanceLister,
	log *slog.Logger,
) *WebhookReconcileLoop {
	if log == nil {
		log = sharedports.DomainLogger(slog.Default(), "webhook")
	}
	l := &WebhookReconcileLoop{
		reconciler: reconciler,
		cache:      cache,
		instances:  instances,
		log:        log,
		wake:       make(chan struct{}, 1),
		now:        func() time.Time { return time.Now().UTC() },
	}
	l.tickIntervalNS.Store(int64(defaultWebhookReconcileTickInterval))
	return l
}

// SetTickInterval atomically updates the cadence + nudges Run via the
// wake channel so the new interval takes effect on the next iteration.
// d <= 0 falls back to the default — never goes fully idle.
func (l *WebhookReconcileLoop) SetTickInterval(d time.Duration) {
	if d <= 0 {
		d = defaultWebhookReconcileTickInterval
	}
	prev := time.Duration(l.tickIntervalNS.Swap(int64(d)))
	if prev == d {
		return
	}
	select {
	case l.wake <- struct{}{}:
	default:
	}
}

// TickInterval / withClock — tests only.
func (l *WebhookReconcileLoop) TickInterval() time.Duration {
	return time.Duration(l.tickIntervalNS.Load())
}
func (l *WebhookReconcileLoop) withClock(f func() time.Time) *WebhookReconcileLoop {
	l.now = f
	return l
}

// Run blocks until ctx is cancelled. Standard SweepLoop pattern:
// time.NewTimer + Reset so SetTickInterval can drop the stale timer.
func (l *WebhookReconcileLoop) Run(ctx context.Context) {
	timer := time.NewTimer(time.Hour)
	if !timer.Stop() {
		<-timer.C
	}
	defer timer.Stop()

	armed := false
	for {
		d := l.TickInterval()
		if d <= 0 {
			d = defaultWebhookReconcileTickInterval
		}
		if armed && !timer.Stop() {
			select {
			case <-timer.C:
			default:
			}
		}
		timer.Reset(d)
		armed = true

		select {
		case <-ctx.Done():
			return
		case <-l.wake:
			// re-read interval and re-arm next iteration
		case <-timer.C:
			armed = false
			l.iterate(ctx)
		}
	}
}

// iterate is one full tick. NEVER propagates errors — the loop must
// survive arbitrary Sonarr / DB failures.
func (l *WebhookReconcileLoop) iterate(ctx context.Context) {
	if l.instances == nil {
		return
	}
	all := l.instances()
	tick := l.TickInterval()
	if tick <= 0 {
		tick = defaultWebhookReconcileTickInterval
	}
	now := l.now()

	for name, inst := range all {
		select {
		case <-ctx.Done():
			return
		default:
		}

		snap := inst.Config
		instName := domain.InstanceName(name)
		if !snap.WebhookInstallEnabled {
			observability.IncWebhookReconcileResult(instName, observability.WebhookReconcileResultSkipped)
			continue
		}

		if l.skipByCache(name, now, tick) {
			observability.IncWebhookReconcileResult(instName, observability.WebhookReconcileResultSkipped)
			continue
		}

		l.reconcileOne(ctx, name, now)
	}
}

// skipByCache returns true when the cached Status says don't reconcile:
// fresh success (within tick) or active backoff (now < NextRetryAt).
// Missing entry, stale success, error past NextRetryAt, error without
// NextRetryAt → false (reconcile).
func (l *WebhookReconcileLoop) skipByCache(name string, now time.Time, tick time.Duration) bool {
	cur, ok := l.cache.Get(name)
	if !ok {
		return false
	}
	if cur.LastError == nil {
		if cur.LastCheckedAt.IsZero() {
			return false
		}
		return now.Sub(cur.LastCheckedAt) < tick
	}
	if cur.NextRetryAt != nil && now.Before(*cur.NextRetryAt) {
		return true
	}
	return false
}

// reconcileOne fires a single Reconcile + metrics. Errors are logged
// WARN and swallowed; ErrUnknownInstance (deletion race) is silent.
func (l *WebhookReconcileLoop) reconcileOne(ctx context.Context, name string, start time.Time) {
	rctx, cancel := context.WithTimeout(ctx, webhookReconcilePerInstanceTimeout)
	defer cancel()

	instName := domain.InstanceName(name)
	_, err := l.reconciler.Reconcile(rctx, name)
	dur := l.now().Sub(start)
	observability.ObserveWebhookReconcileDuration(instName, dur.Seconds())

	switch {
	case err == nil:
		observability.IncWebhookReconcileResult(instName, observability.WebhookReconcileResultOK)
	case errors.Is(err, webhookinstall.ErrUnknownInstance):
		// Deletion race between l.instances() and Reconcile.
		observability.IncWebhookReconcileResult(instName, observability.WebhookReconcileResultSkipped)
	default:
		observability.IncWebhookReconcileResult(instName, observability.WebhookReconcileResultError)
		l.log.WarnContext(ctx, "webhook_reconcile_loop_iteration_failed",
			slog.String("component", webhookReconcileLogComponent),
			slog.String("instance", name),
			slog.String("error", err.Error()))
	}
}
