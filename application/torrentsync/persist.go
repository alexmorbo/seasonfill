package torrentsync

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/alexmorbo/seasonfill/infrastructure/qbit"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
	sharedports "github.com/alexmorbo/seasonfill/internal/shared/ports"
)

// PersistPolicy implements the three-grain write rule from PRD
// §4.6: state-group transitions are persisted immediately
// alongside an event row; mutable counters flush at most once
// per `flushInterval` (5 minutes) in a single transaction; live
// fields never persist.
//
// The policy owns no state of its own — the loop hands it the
// previous Entry, the new Entry, and the last-flush wall clock.
// The pending counter set is held by the caller (the use case)
// so the policy stays a pure function of its inputs.
type PersistPolicy struct {
	repo      TorrentsRepo
	events    EventsRepo
	logger    *slog.Logger
	now       func() time.Time
	flushFreq time.Duration
}

// DefaultFlushInterval is the PRD §4.6 floor on counter-flush
// cadence — sqlite write-amplification guard.
const DefaultFlushInterval = 5 * time.Minute

// NewPersistPolicy wires the policy. flushFreq <= 0 falls back
// to DefaultFlushInterval; logger nil → slog.Default(); now nil →
// `time.Now().UTC` so tests can inject a deterministic clock.
func NewPersistPolicy(repo TorrentsRepo, events EventsRepo, logger *slog.Logger) *PersistPolicy {
	if logger == nil {
		logger = sharedports.DomainLogger(slog.Default(), "qbit")
	}
	return &PersistPolicy{
		repo:      repo,
		events:    events,
		logger:    logger,
		now:       func() time.Time { return time.Now().UTC() },
		flushFreq: DefaultFlushInterval,
	}
}

// WithClock overrides the wall-clock source. Test seam.
func (p *PersistPolicy) WithClock(now func() time.Time) *PersistPolicy {
	if now != nil {
		p.now = now
	}
	return p
}

// WithFlushInterval overrides the counter-flush floor. Tests use
// it to shrink the window; production callers leave it at the
// default.
func (p *PersistPolicy) WithFlushInterval(d time.Duration) *PersistPolicy {
	if d > 0 {
		p.flushFreq = d
	}
	return p
}

// FlushInterval is the read-side accessor for the loop, which
// uses it to decide whether the pending-counter set is due for a
// flush on this tick.
func (p *PersistPolicy) FlushInterval() time.Duration { return p.flushFreq }

// HandleTransition is called by the use case for every torrent
// in the freshly-Refresh-ed snapshot. It writes:
//   - an immediate upsert + state_change event row when the
//     state_group differs from the previous Entry (or from the
//     row found in `qbit_torrents` for never-seen torrents);
//   - an `added` event the first time the loop ever sees the
//     hash on this instance;
//   - a `completed` event the first time the state transitions
//     into the seeding bucket.
//
// Returns true when the row was persisted on this call (so the
// caller knows it can refresh `LastFlushedCounters` on the Entry).
// State-change writes also persist the latest counter values
// transactionally so a separate flush is not needed for them.
func (p *PersistPolicy) HandleTransition(ctx context.Context, instance domain.InstanceName, prev *Entry, next Entry) (bool, error) {
	from := qbit.StateGroup("")
	if prev != nil {
		from = prev.StateGroup
	}
	to := next.StateGroup

	// `added` is the synthetic event we emit the first time the
	// loop ever sees the hash on this instance. prev==nil here
	// covers both first-sync-of-a-fresh-torrent and post-restart
	// before any counter flush has happened — restart recovery
	// pre-populates `prev` from DB so this branch only fires for
	// genuinely new torrents.
	if prev == nil {
		if err := p.repo.Upsert(ctx, instance, next); err != nil {
			return false, fmt.Errorf("upsert torrent on add: %w", err)
		}
		if err := p.events.Insert(ctx, EventRow{
			Instance: instance, Hash: next.Info.Hash,
			Event: EventAdded, To: to, At: p.now(),
		}); err != nil {
			return false, fmt.Errorf("insert added event: %w", err)
		}
		p.logger.InfoContext(ctx, "torrentsync_added",
			slog.String("instance_name", string(instance)),
			slog.String("hash", next.Info.Hash),
			slog.String("state_to", string(to)),
			slog.String("outcome", "added"))
		return true, nil
	}

	if from == to {
		return false, nil
	}

	if err := p.repo.Upsert(ctx, instance, next); err != nil {
		return false, fmt.Errorf("upsert torrent on transition: %w", err)
	}
	if err := p.events.Insert(ctx, EventRow{
		Instance: instance, Hash: next.Info.Hash,
		Event: EventStateChange, From: from, To: to, At: p.now(),
	}); err != nil {
		return false, fmt.Errorf("insert state_change event: %w", err)
	}
	// The first transition into seeding is also a synthetic
	// `completed` event — it gives the UI a stable anchor for
	// "this season finished downloading at T" without making the
	// reader compute it from a state_change scan.
	if to == qbit.StateGroupSeeding && from != qbit.StateGroupSeeding {
		if err := p.events.Insert(ctx, EventRow{
			Instance: instance, Hash: next.Info.Hash,
			Event: EventCompleted, To: to, At: p.now(),
		}); err != nil {
			return false, fmt.Errorf("insert completed event: %w", err)
		}
	}
	p.logger.InfoContext(ctx, "torrentsync_state_change",
		slog.String("instance_name", string(instance)),
		slog.String("hash", next.Info.Hash),
		slog.String("state_from", string(from)),
		slog.String("state_to", string(to)),
		slog.String("outcome", "state_change"))
	return true, nil
}

// HandleRemoval is called for each hash in Snapshot.Removed. It
// stamps `present=false, deleted_at=now` and emits a `deleted`
// event. A later sync without the hash is a no-op (MarkAbsent
// detects the existing absent row).
func (p *PersistPolicy) HandleRemoval(ctx context.Context, instance domain.InstanceName, hash string) error {
	now := p.now()
	if err := p.repo.MarkAbsent(ctx, instance, hash, now); err != nil {
		return fmt.Errorf("mark torrent absent: %w", err)
	}
	if err := p.events.Insert(ctx, EventRow{
		Instance: instance, Hash: hash,
		Event: EventDeleted, At: now,
	}); err != nil {
		return fmt.Errorf("insert deleted event: %w", err)
	}
	p.logger.InfoContext(ctx, "torrentsync_deleted",
		slog.String("instance_name", string(instance)),
		slog.String("hash", hash),
		slog.String("outcome", "deleted"))
	return nil
}

// FlushCounters drains the pending-counter set into a single
// transaction. `pending` is a snapshot the caller built since the
// last flush; the policy does NOT clear it (the use case does, so
// failures don't lose state). The implementation rides through
// the repo's BatchUpsert which opens one tx per call.
//
// `now` is captured once per flush so every row in the batch
// carries the same `updated_at` — easier to reason about in a
// trace.
func (p *PersistPolicy) FlushCounters(ctx context.Context, instance domain.InstanceName, pending []Entry) error {
	if len(pending) == 0 {
		return nil
	}
	now := p.now()
	if err := p.repo.BatchUpsert(ctx, instance, pending, now); err != nil {
		return fmt.Errorf("flush counters batch: %w", err)
	}
	p.logger.DebugContext(ctx, "torrentsync_counters_flushed",
		slog.String("instance_name", string(instance)),
		slog.Int("rows", len(pending)),
		slog.String("outcome", "counters_flushed"))
	return nil
}

// CountersDirty reports whether the freshly-Refresh-ed counters
// differ from the last-flushed snapshot. Cheap value comparison —
// the diff is symmetric (any non-equal field counts).
func CountersDirty(last, next Counters) bool {
	if last.Ratio != next.Ratio ||
		last.Uploaded != next.Uploaded ||
		last.TimeActiveS != next.TimeActiveS ||
		last.SeedingTimeS != next.SeedingTimeS ||
		last.Popularity != next.Popularity {
		return true
	}
	if !last.LastActivity.Equal(next.LastActivity) {
		return true
	}
	return false
}
