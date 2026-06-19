package enrichment

import (
	"context"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	sharedports "github.com/alexmorbo/seasonfill/internal/shared/ports"
)

// DispatcherImpl is the package-public production dispatcher. Construct
// via NewDispatcher; Start kicks off the worker goroutines; Close
// stops them. Enqueue is safe for concurrent callers and never
// blocks for more than the queue's non-blocking send try.
type DispatcherImpl struct {
	queue   *priorityQueue
	workers Workers
	logger  *slog.Logger

	mu     sync.Mutex
	wg     sync.WaitGroup
	cancel context.CancelFunc
	// 306 — guard for late registration of OnSeriesComplete by the
	// cold-start path. Read by runHandler from goroutines; the atomic
	// pointer makes the publication race-free without widening mu.
	onSeriesComplete atomic.Pointer[func(int64)]
}

// Workers is the dependency bundle. SeriesHandler is required; the
// person handler is optional and may be nil (placeholder slot
// reserved for 212 — when nil, the person goroutine still starts but
// every dequeue logs "not implemented" + immediately releases).
type Workers struct {
	SeriesHandler func(ctx context.Context, id int64) error
	PersonHandler func(ctx context.Context, id int64) error
	// 213 (D-1). OMDb handler; nil-OK — when nil the goroutine
	// still spawns but every dequeue logs "handler_nil" and
	// releases the dedup slot (matches the 211 person-nil pattern).
	OMDbHandler func(ctx context.Context, id int64) error
	// 306. Optional per-series completion hook. Fired AFTER the
	// queue release for EntitySeries jobs only — success OR error.
	// Nil-OK (production-only feature for the cold-start gauge;
	// tests that don't care leave it nil).
	OnSeriesComplete func(id int64)
}

// NewDispatcher constructs a not-yet-running dispatcher. Start binds
// it to a context.
func NewDispatcher(workers Workers, logger *slog.Logger) *DispatcherImpl {
	if logger == nil {
		logger = sharedports.DomainLogger(slog.Default(), "enrichment")
	}
	return &DispatcherImpl{
		queue:   newPriorityQueue(),
		workers: workers,
		logger:  logger,
	}
}

// Start launches the worker goroutines (2 × series, 1 × person)
// against a child context. Idempotent — calling Start twice is a
// caller bug; we log + return.
func (d *DispatcherImpl) Start(parent context.Context) {
	d.mu.Lock()
	if d.cancel != nil {
		d.mu.Unlock()
		d.logger.Warn("enrichment.dispatcher.start_twice")
		return
	}
	ctx, cancel := context.WithCancel(parent)
	d.cancel = cancel
	d.mu.Unlock()

	// Two series goroutines.
	for i := range 2 {
		idx := i
		d.wg.Go(func() {
			d.loop(ctx, EntitySeries, idx, d.workers.SeriesHandler)
		})
	}
	// One person goroutine — placeholder until 212 lands the real
	// handler. PersonHandler nil → loop logs "not implemented"
	// per-dequeue.
	d.wg.Go(func() {
		d.loop(ctx, EntityPerson, 0, d.workers.PersonHandler)
	})
	// 213 (D-1): one OMDb goroutine. The shared queue's cross-kind
	// drain in loop() guarantees an OMDb goroutine waking on a
	// series job re-enqueues it. With 2× series + 1× person + 1×
	// OMDb goroutines and 3 EntityKinds the cross-kind spin remains
	// the known caveat documented in 211 §10.
	d.wg.Go(func() {
		d.loop(ctx, EntityOMDb, 0, d.workers.OMDbHandler)
	})
	d.logger.InfoContext(ctx, "enrichment.dispatcher.started",
		slog.Int("series_workers", 2),
		slog.Int("person_workers", 1),
		slog.Int("omdb_workers", 1),
	)
}

// Enqueue is the public Dispatcher port impl.
func (d *DispatcherImpl) Enqueue(kind EntityKind, id int64, p Priority) {
	if !kind.IsValid() {
		d.logger.Warn("enrichment.dispatcher.enqueue_invalid_kind",
			slog.String("kind", string(kind)))
		return
	}
	if id <= 0 {
		d.logger.Warn("enrichment.dispatcher.enqueue_invalid_id",
			slog.Int64("entity_id", id))
		return
	}
	job := Job{Kind: kind, EntityID: id, Priority: p, EnqueuedAt: time.Now().UTC()}
	if !d.queue.enqueue(job) {
		// Dedup-skip OR queue-full — both surface as the same
		// info-level "skipped" line (cardinality cap one tag).
		d.logger.Debug("enrichment.dispatcher.enqueue_skipped",
			slog.String("entity_type", string(kind)),
			slog.Int64("entity_id", id),
			slog.String("priority", priorityLabel(p)),
		)
	}
}

// Close stops every worker. Cancels the child ctx, closes the queue,
// waits for goroutines to drain.
func (d *DispatcherImpl) Close() {
	d.mu.Lock()
	if d.cancel == nil {
		d.mu.Unlock()
		return
	}
	cancel := d.cancel
	d.cancel = nil
	d.mu.Unlock()

	cancel()
	d.queue.close()
	d.wg.Wait()
	d.logger.Info("enrichment.dispatcher.stopped")
}

// loop is one worker's main pump. handler nil → log + release (the
// person placeholder case). Errors bubble up as slog WARN; the
// worker NEVER takes the dispatcher down on a handler error.
func (d *DispatcherImpl) loop(ctx context.Context, kind EntityKind, idx int, handler func(context.Context, int64) error) {
	log := d.logger.With(
		slog.String("entity_type", string(kind)),
		slog.Int("worker_idx", idx),
	)
	for {
		j, ok := d.queue.dequeue(ctx)
		if !ok {
			return
		}
		if j.Kind != kind {
			// Cross-kind drain — re-enqueue + skip. This happens when
			// the person goroutine wakes on a series job in the queue
			// (current impl puts both in the same channels; if/when
			// 212 splits per-kind channels this branch goes away).
			// Release first so the re-enqueue's dedup check is the
			// authoritative one.
			d.queue.release(j.Kind, j.EntityID)
			d.queue.enqueue(j) //nolint:errcheck // best-effort re-queue
			continue
		}
		// Panic-safe dedup release: a handler that panics MUST NOT
		// pin the slot forever. Per Critical Decision #2 below.
		d.runHandler(ctx, log, j, handler)
	}
}

// runHandler invokes handler with a deferred dedup release so a
// panic surfaces (we re-panic after release) without trapping the
// (kind, id) slot in the in-flight map.
func (d *DispatcherImpl) runHandler(ctx context.Context, log *slog.Logger, j Job, handler func(context.Context, int64) error) {
	defer func() {
		d.queue.release(j.Kind, j.EntityID)
		// 306 — cold-start gauge tick. Fires AFTER release so the
		// depth gauge has already dropped. Only EntitySeries jobs
		// participate (person/omdb handlers must not impact the
		// cold-start counter). Two registration paths:
		//   - Workers.OnSeriesComplete: set at NewDispatcher time
		//   - SetOnSeriesComplete: late binding from BackfillSeries
		// Both run if both are set.
		if j.Kind != EntitySeries {
			return
		}
		if d.workers.OnSeriesComplete != nil {
			d.workers.OnSeriesComplete(j.EntityID)
		}
		if cb := d.onSeriesComplete.Load(); cb != nil {
			(*cb)(j.EntityID)
		}
	}()
	start := time.Now()
	if handler == nil {
		log.WarnContext(ctx, "enrichment.dispatcher.handler_nil",
			slog.Int64("entity_id", j.EntityID),
		)
		return
	}
	err := handler(ctx, j.EntityID)
	dur := time.Since(start)
	if err != nil {
		log.WarnContext(ctx, "enrichment.dispatcher.handler_failed",
			slog.Int64("entity_id", j.EntityID),
			slog.String("error", err.Error()),
			slog.Int64("duration_ms", dur.Milliseconds()),
		)
		return
	}
	log.InfoContext(ctx, "enrichment.dispatcher.handler_ok",
		slog.Int64("entity_id", j.EntityID),
		slog.Int64("duration_ms", dur.Milliseconds()),
		slog.String("priority", priorityLabel(j.Priority)),
	)
}

func priorityLabel(p Priority) string {
	if p == PriorityHot {
		return "hot"
	}
	return "cold"
}

// SetOnSeriesComplete registers (or clears, when fn==nil) the late-bound
// per-series completion hook used by the cold-start backfill (Story 306).
// Safe to call concurrently with the worker goroutines — uses an atomic
// pointer for the publication race. The hook is invoked AFTER the queue
// release for EntitySeries jobs only.
func (d *DispatcherImpl) SetOnSeriesComplete(fn func(id int64)) {
	if fn == nil {
		d.onSeriesComplete.Store(nil)
		return
	}
	d.onSeriesComplete.Store(&fn)
}
