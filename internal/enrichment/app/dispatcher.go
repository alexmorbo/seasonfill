package enrichment

import (
	"context"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/alexmorbo/seasonfill/internal/observability"
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
	// S1b — late-bound movie hydration handler. The movie goroutine
	// resolves this per job (resolveMovieHandler) so the MovieWorker,
	// built AFTER dispatcher.Start, can be wired via SetMovieHandler
	// without racing the goroutine spawn. nil (unset) → falls back to
	// Workers.MovieHandler → handler_nil path.
	movieHandler atomic.Pointer[jobHandler]
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
	// W18-12 (F-02): priority-aware. The dispatcher threads the Job's
	// Priority so the handler can select the OMDb budget lane —
	// PriorityHot → HandleHot (spends into the Hot floor), else
	// HandleCold (backs off at the floor). Series/person handlers stay
	// id-only (they have no lane).
	OMDbHandler func(ctx context.Context, id int64, p Priority) error
	// MovieHandler — S1b (ADR-0021 §S1 Part B). Movie TMDB hydration
	// handler; id-only (no lane) like Series/Person. nil-OK at
	// NewDispatcher time: the production handler is late-bound via
	// SetMovieHandler AFTER the MovieWorker is built (dispatcher.Start
	// runs first). When neither this nor SetMovieHandler is set, the
	// movie loop logs "handler_nil" + releases the slot — same pattern as
	// the OMDb pre-activation path. Tests may set it directly for a
	// deterministic drain assertion.
	MovieHandler func(ctx context.Context, id int64) error
	// 306. Optional per-series completion hook. Fired AFTER the
	// queue release for EntitySeries jobs only — success OR error.
	// Nil-OK (production-only feature for the cold-start gauge;
	// tests that don't care leave it nil).
	OnSeriesComplete func(id int64)
	// SeriesWorkers / PersonWorkers — Story 1096. Number of concurrent
	// series / person goroutines Start spawns. 0/negative → clamped to 1
	// inside Start (defensive; the config layer already floors to the >=1
	// default). Pre-1096 these were hardcoded 2 / 1; the operator now tunes
	// them via SEASONFILL_ENRICHMENT_{SERIES,PERSON}_WORKERS. OMDb stays 1.
	SeriesWorkers int
	PersonWorkers int
	// MovieWorkers — S1b. Number of concurrent movie-hydration goroutines
	// Start spawns. 0/negative → clamped to 1 inside Start (defensive; the
	// config layer already floors to the >=1 default via
	// SEASONFILL_ENRICHMENT_MOVIE_WORKERS).
	MovieWorkers int
}

// jobHandler is the internal, priority-aware handler shape the worker loop
// invokes. The public Workers fields for series/person expose the id-only
// shape (they do not select a lane); Start lifts them via liftIDHandler. The
// OMDb field is priority-aware (F-02) so queue priority can pick the OMDb
// budget lane (Hot vs Cold), not just dequeue ordering.
type jobHandler func(ctx context.Context, id int64, p Priority) error

// liftIDHandler adapts an id-only worker handler to the jobHandler shape,
// discarding the priority. Returns nil when h is nil so the loop's
// handler_nil path is preserved for an unset optional handler.
func liftIDHandler(h func(context.Context, int64) error) jobHandler {
	if h == nil {
		return nil
	}
	return func(ctx context.Context, id int64, _ Priority) error { return h(ctx, id) }
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

	// Series goroutines. Story 1096 — count is configurable; clamp at 1
	// defensively so a mis-set 0/negative never disables the pool.
	// Series/person handlers are lifted to the priority-aware jobHandler
	// shape (priority discarded — no lane).
	seriesWorkers := max(d.workers.SeriesWorkers, 1)
	personWorkers := max(d.workers.PersonWorkers, 1)
	seriesH := liftIDHandler(d.workers.SeriesHandler)
	for i := range seriesWorkers {
		idx := i
		d.wg.Go(func() {
			d.loop(ctx, EntitySeries, idx, seriesH)
		})
	}
	// Person goroutines. Story 1096 — count is configurable (default 1).
	// PersonHandler nil → loop logs "not implemented" per-dequeue.
	personH := liftIDHandler(d.workers.PersonHandler)
	for i := range personWorkers {
		idx := i
		d.wg.Go(func() {
			d.loop(ctx, EntityPerson, idx, personH)
		})
	}
	// 213 (D-1): one OMDb goroutine. Story 1104 gave each kind its own
	// channel pair, so this goroutine drains ONLY EntityOMDb jobs — the
	// old cross-kind drain / hot-spin caveat (211 §10) is gone.
	// W18-12 (F-02): OMDbHandler is already the priority-aware shape;
	// pass it straight through so Job.Priority reaches the closure.
	d.wg.Go(func() {
		d.loop(ctx, EntityOMDb, 0, d.workers.OMDbHandler)
	})
	// S1b — movie goroutines. Count is configurable (default 1). The
	// handler is resolved per-job via resolveMovieHandler so the late-bound
	// SetMovieHandler (wired after the MovieWorker is built) is observed
	// without restarting the pool. nil handler → loop logs "handler_nil".
	movieWorkers := max(d.workers.MovieWorkers, 1)
	for i := range movieWorkers {
		idx := i
		d.wg.Go(func() {
			d.movieLoop(ctx, idx)
		})
	}
	d.logger.InfoContext(ctx, "enrichment.dispatcher.started",
		slog.Int("series_workers", seriesWorkers),
		slog.Int("person_workers", personWorkers),
		slog.Int("omdb_workers", 1),
		slog.Int("movie_workers", movieWorkers),
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
//
// Story 1104: dequeue is per-kind, so a worker only ever receives its
// own kind's jobs — the previous cross-kind drain branch (which
// re-enqueued foreign jobs and busy-spun) is deleted.
func (d *DispatcherImpl) loop(ctx context.Context, kind EntityKind, idx int, handler jobHandler) {
	log := d.logger.With(
		slog.String("entity_type", string(kind)),
		slog.Int("worker_idx", idx),
	)
	for {
		j, ok := d.queue.dequeue(ctx, kind)
		if !ok {
			return
		}
		// Panic-safe dedup release: a handler that panics MUST NOT
		// pin the slot forever. Per Critical Decision #2 below.
		d.runHandler(ctx, log, j, handler)
	}
}

// runHandler invokes handler with a deferred dedup release so a
// panic surfaces (we re-panic after release) without trapping the
// (kind, id) slot in the in-flight map.
func (d *DispatcherImpl) runHandler(ctx context.Context, log *slog.Logger, j Job, handler jobHandler) {
	// M-2 — per-job RED metrics. Single choke point covering every kind.
	// result defaults to "error" so a panicking handler is counted as an
	// error; the deferred close-out below Decrements inflight on EVERY exit
	// path (success/error/skipped/panic).
	kindStr := string(j.Kind)
	start := time.Now()
	observability.IncEnrichmentJobInflight(kindStr)
	result := "error"
	defer func() {
		observability.ObserveEnrichmentJobDone(kindStr, result, time.Since(start))
	}()
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
	if handler == nil {
		result = "skipped"
		log.WarnContext(ctx, "enrichment.dispatcher.handler_nil",
			slog.Int64("entity_id", j.EntityID),
		)
		return
	}
	err := handler(ctx, j.EntityID, j.Priority)
	dur := time.Since(start)
	if err != nil {
		log.WarnContext(ctx, "enrichment.dispatcher.handler_failed",
			slog.Int64("entity_id", j.EntityID),
			slog.String("error", err.Error()),
			slog.Int64("duration_ms", dur.Milliseconds()),
		)
		return
	}
	result = "success"
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

// movieLoop is the S1b movie worker pump. Unlike loop(), it resolves the
// handler FRESH on every job via resolveMovieHandler so a late-bound
// SetMovieHandler (the MovieWorker is built after dispatcher.Start) is observed
// without a pool restart. dequeue is per-kind (Story 1104), so a movie worker
// only ever receives EntityMovie jobs.
func (d *DispatcherImpl) movieLoop(ctx context.Context, idx int) {
	log := d.logger.With(
		slog.String("entity_type", string(EntityMovie)),
		slog.Int("worker_idx", idx),
	)
	for {
		j, ok := d.queue.dequeue(ctx, EntityMovie)
		if !ok {
			return
		}
		d.runHandler(ctx, log, j, d.resolveMovieHandler())
	}
}

// resolveMovieHandler returns the effective movie handler: the late-bound
// SetMovieHandler override if present, else the static Workers.MovieHandler
// (lifted to the priority-aware shape). Returns nil when neither is set so
// runHandler's handler_nil path fires (pre-late-bind boot window).
func (d *DispatcherImpl) resolveMovieHandler() jobHandler {
	if cb := d.movieHandler.Load(); cb != nil {
		return *cb
	}
	return liftIDHandler(d.workers.MovieHandler)
}

// SetMovieHandler late-binds the movie hydration handler (S1b). Called from
// cmd/server after the MovieWorker is constructed — dispatcher.Start already
// spawned the movie goroutines, which resolve this pointer per job. fn nil
// clears the override (falls back to Workers.MovieHandler). Safe to call
// concurrently with the worker goroutines (atomic publication).
func (d *DispatcherImpl) SetMovieHandler(fn func(ctx context.Context, id int64) error) {
	lifted := liftIDHandler(fn)
	if lifted == nil {
		d.movieHandler.Store(nil)
		return
	}
	d.movieHandler.Store(&lifted)
}
