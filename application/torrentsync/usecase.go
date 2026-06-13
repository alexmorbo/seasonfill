package torrentsync

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/alexmorbo/seasonfill/infrastructure/qbit"
)

// UseCase wires the in-memory Store, the PersistPolicy, and the
// per-instance SyncSession lifecycle. One UseCase value is shared
// across all per-instance Loops; concurrent RunInstance calls
// against different instances are safe by construction.
type UseCase struct {
	store    *Store
	policy   *PersistPolicy
	sessions SyncSessionFactory
	repo     TorrentsRepo
	logger   *slog.Logger

	mu sync.Mutex
	// Per-instance session cache: rebuilt lazily on first
	// RunInstance and after any Refresh error so a re-login is
	// transparent to the loop.
	sessionByInstance map[string]qbit.SyncSession
	// lastFlush records the wall-clock of the last counter flush
	// per instance. The use case decides whether the pending set
	// is due based on this + policy.FlushInterval().
	lastFlush map[string]time.Time
	// pending holds rows whose counters changed but have not yet
	// been flushed. Keyed by hash. The use case rebuilds this on
	// every tick from the store diff.
	pending map[string]map[string]Entry
	// hydrated tracks which instances have already had their
	// memory store populated from `qbit_torrents`. Restart
	// recovery runs exactly once per instance lifetime.
	hydrated map[string]bool
	// reconciler runs the 4-source torrent->series mapping pass
	// every 10th tick (PRD §4.5). Nil-OK: pre-Story-221 wiring
	// runs the qBit refresh without the bridge.
	reconciler *Reconciler
}

// NewUseCase wires the dependencies. Construction never touches
// the network; sessions are built lazily inside RunInstance.
func NewUseCase(store *Store, policy *PersistPolicy, sessions SyncSessionFactory, repo TorrentsRepo, logger *slog.Logger) *UseCase {
	if logger == nil {
		logger = slog.Default()
	}
	return &UseCase{
		store:             store,
		policy:            policy,
		sessions:          sessions,
		repo:              repo,
		logger:            logger,
		sessionByInstance: make(map[string]qbit.SyncSession),
		lastFlush:         make(map[string]time.Time),
		pending:           make(map[string]map[string]Entry),
		hydrated:          make(map[string]bool),
	}
}

// WithReconciler installs the reconciler invoked on every 10th
// RunInstance tick (PRD §4.5). Returning *UseCase keeps the
// fluent constructor pattern used elsewhere in the package.
// Passing nil is a no-op — the use case still runs the qBit
// refresh without the bridge.
func (u *UseCase) WithReconciler(r *Reconciler) *UseCase {
	u.reconciler = r
	return u
}

// Hydrate loads `qbit_torrents WHERE present=true` for the named
// instance into the memory store. Idempotent — repeat calls are
// no-ops. Called once per instance by the loop launcher before
// Run is invoked; live fields are zero until the first successful
// Refresh.
func (u *UseCase) Hydrate(ctx context.Context, instance string) error {
	u.mu.Lock()
	if u.hydrated[instance] {
		u.mu.Unlock()
		return nil
	}
	u.mu.Unlock()

	rows, err := u.repo.List(ctx, instance)
	if err != nil {
		return fmt.Errorf("hydrate torrentsync store: %w", err)
	}
	u.store.EnsureInstance(instance)
	for _, e := range rows {
		// Belt-and-braces: live fields are NOT in the schema, so
		// the repo's List leaves them zero. We rewrite Entry to
		// make that contract explicit in case a future repo
		// refactor accidentally backfills them.
		e.Info.DlSpeed = 0
		e.Info.UpSpeed = 0
		e.Info.ETA = 0
		e.Info.NumSeeds = 0
		e.Info.NumLeechs = 0
		e.Info.Progress = 0
		e.LastFlushedCounters = CountersFrom(e.Info)
		u.store.Put(instance, e)
	}
	u.mu.Lock()
	u.hydrated[instance] = true
	u.mu.Unlock()
	u.logger.InfoContext(ctx, "torrentsync_hydrated",
		slog.String("instance_name", instance),
		slog.Int("rows", len(rows)),
		slog.String("outcome", "hydrated"))
	return nil
}

// RunInstance is the per-tick entrypoint the Loop calls. It
// performs:
//  1. Refresh against qBit (lazy-build session on first call).
//  2. Diff against the store — emit state-change events,
//     collect counter deltas into the pending set.
//  3. Persist removals from Snapshot.Removed.
//  4. If FlushInterval has elapsed, drain the pending set
//     through BatchUpsert.
//
// `now` is plumbed by the caller so tests can pin the clock.
func (u *UseCase) RunInstance(ctx context.Context, instance string, now time.Time) error {
	sess, err := u.session(ctx, instance)
	if err != nil {
		return fmt.Errorf("torrentsync session %s: %w", instance, err)
	}
	snap, err := sess.Refresh(ctx)
	if err != nil {
		// Drop the session so the next tick re-logs-in.
		u.mu.Lock()
		delete(u.sessionByInstance, instance)
		u.mu.Unlock()
		return fmt.Errorf("torrentsync refresh %s: %w", instance, err)
	}

	u.store.EnsureInstance(instance)

	for _, info := range snap.Torrents {
		next := Entry{
			Info:       info,
			StateGroup: info.StateGroup,
			SyncedAt:   now,
		}
		prev, hadPrev := u.store.Get(instance, info.Hash)
		var prevPtr *Entry
		if hadPrev {
			prevPtr = &prev
			next.LastFlushedCounters = prev.LastFlushedCounters
		}
		persisted, perr := u.policy.HandleTransition(ctx, instance, prevPtr, next)
		if perr != nil {
			u.logger.ErrorContext(ctx, "torrentsync_persist_failed",
				slog.String("instance_name", instance),
				slog.String("hash", info.Hash),
				slog.String("outcome", "persist_error"),
				slog.String("error", perr.Error()))
			// Keep going — one bad row should not stall the rest.
			continue
		}
		if persisted {
			// State change writes also bring the counters up to
			// date, so drop any pending entry for this hash.
			next.LastFlushedCounters = CountersFrom(info)
			u.dropPending(instance, info.Hash)
		} else if CountersDirty(next.LastFlushedCounters, CountersFrom(info)) {
			u.markPending(instance, next)
		}
		u.store.Put(instance, next)
	}

	for _, hash := range snap.Removed {
		if err := u.policy.HandleRemoval(ctx, instance, hash); err != nil {
			u.logger.ErrorContext(ctx, "torrentsync_remove_failed",
				slog.String("instance_name", instance),
				slog.String("hash", hash),
				slog.String("outcome", "remove_error"),
				slog.String("error", err.Error()))
			continue
		}
		u.store.Delete(instance, hash)
		u.dropPending(instance, hash)
	}

	if u.flushDue(instance, now) {
		pending := u.takePending(instance)
		if err := u.policy.FlushCounters(ctx, instance, pending); err != nil {
			// Restore pending on flush failure so the next tick
			// re-tries. We do not bubble the flush error out —
			// the loop treats the Refresh outcome as the
			// success signal; counter persistence is best-effort.
			u.restorePending(instance, pending)
			u.logger.ErrorContext(ctx, "torrentsync_flush_failed",
				slog.String("instance_name", instance),
				slog.Int("rows", len(pending)),
				slog.String("outcome", "flush_error"),
				slog.String("error", err.Error()))
		} else {
			u.mu.Lock()
			u.lastFlush[instance] = now
			u.mu.Unlock()
			// On successful flush, advance LastFlushedCounters
			// on every store entry that participated.
			for _, e := range pending {
				e.LastFlushedCounters = CountersFrom(e.Info)
				u.store.Put(instance, e)
			}
		}
	}
	if u.reconciler != nil {
		if err := u.reconciler.MaybeRun(ctx, instance); err != nil {
			// Reconciler errors are best-effort — never propagate
			// past the loop. MaybeRun already logged WARN per
			// failing source.
			u.logger.WarnContext(ctx, "torrentsync_reconciler_pass_failed",
				slog.String("instance_name", instance),
				slog.String("outcome", "reconciler_error"),
				slog.String("error", err.Error()))
		}
	}
	return nil
}

// session is a one-method helper that returns (and caches) the
// SyncSession for the instance, building it on demand.
func (u *UseCase) session(ctx context.Context, instance string) (qbit.SyncSession, error) {
	u.mu.Lock()
	sess, ok := u.sessionByInstance[instance]
	u.mu.Unlock()
	if ok {
		return sess, nil
	}
	sess, err := u.sessions.NewSyncSession(ctx, instance)
	if err != nil {
		return nil, err
	}
	u.mu.Lock()
	u.sessionByInstance[instance] = sess
	u.mu.Unlock()
	return sess, nil
}

func (u *UseCase) markPending(instance string, e Entry) {
	u.mu.Lock()
	defer u.mu.Unlock()
	inst, ok := u.pending[instance]
	if !ok {
		inst = make(map[string]Entry)
		u.pending[instance] = inst
	}
	inst[e.Info.Hash] = e
}

func (u *UseCase) dropPending(instance, hash string) {
	u.mu.Lock()
	defer u.mu.Unlock()
	if inst, ok := u.pending[instance]; ok {
		delete(inst, hash)
	}
}

func (u *UseCase) takePending(instance string) []Entry {
	u.mu.Lock()
	defer u.mu.Unlock()
	inst, ok := u.pending[instance]
	if !ok || len(inst) == 0 {
		return nil
	}
	out := make([]Entry, 0, len(inst))
	for _, e := range inst {
		out = append(out, e)
	}
	delete(u.pending, instance)
	return out
}

func (u *UseCase) restorePending(instance string, rows []Entry) {
	u.mu.Lock()
	defer u.mu.Unlock()
	inst, ok := u.pending[instance]
	if !ok {
		inst = make(map[string]Entry, len(rows))
		u.pending[instance] = inst
	}
	for _, e := range rows {
		if _, dup := inst[e.Info.Hash]; dup {
			continue
		}
		inst[e.Info.Hash] = e
	}
}

func (u *UseCase) flushDue(instance string, now time.Time) bool {
	u.mu.Lock()
	defer u.mu.Unlock()
	inst, ok := u.pending[instance]
	if !ok || len(inst) == 0 {
		return false
	}
	last, ok := u.lastFlush[instance]
	if !ok {
		// First tick after start — set the wall-clock so the
		// next decision compares apples-to-apples.
		u.lastFlush[instance] = now
		return false
	}
	return now.Sub(last) >= u.policy.FlushInterval()
}
