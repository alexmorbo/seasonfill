package torrentsync

import (
	"context"
	"time"

	"github.com/alexmorbo/seasonfill/infrastructure/qbit"
)

// SyncSessionFactory is the use case's view of
// qbit.Client.NewSyncSession. We accept the narrow factory rather
// than the full Client so the test harness in loop_test.go can
// hand in a fake without dragging the qBit client construction
// into the application layer.
type SyncSessionFactory interface {
	NewSyncSession(ctx context.Context, instance string) (qbit.SyncSession, error)
}

// TorrentsRepo is the persistence surface the persist policy
// exercises. Implemented by
// infrastructure/database/repositories.QbitTorrentsRepository
// (story 220 §6).
//
// All writes are scoped by (instance, hash). The repository owns
// transaction boundaries — BatchUpsert wraps its work in a single
// tx per call (PRD §13 risk 2).
type TorrentsRepo interface {
	// Upsert writes / overwrites the row for (instance, info.Hash).
	// Persists every column except live telemetry (PRD §4.6).
	Upsert(ctx context.Context, instance string, e Entry) error

	// BatchUpsert writes the supplied entries inside a single
	// transaction. Used by FlushCounters; entries beyond the
	// counter set ride along (cheap to write the whole row when
	// we're already holding the lock).
	BatchUpsert(ctx context.Context, instance string, entries []Entry, updatedAt time.Time) error

	// MarkAbsent flips present=false + deleted_at=now for an
	// existing row. Returning nil on "row not found" is allowed —
	// removal of a hash we never persisted is a no-op.
	MarkAbsent(ctx context.Context, instance, hash string, when time.Time) error

	// List returns every persisted Entry for the instance,
	// including `present=false` rows. Used by restart recovery
	// to repopulate the memory store. The returned Entry.Info
	// live fields (DlSpeed/UpSpeed/ETA/NumSeeds/NumLeechs/
	// Progress) are zero — never persisted in the first place.
	List(ctx context.Context, instance string) ([]Entry, error)
}

// EventsRepo is the append-only surface for state-transition and
// synthetic lifecycle events (PRD §4.6). Pruned by the weekly GC
// from story 218 (E-2); the loop never reads, only inserts.
type EventsRepo interface {
	Insert(ctx context.Context, row EventRow) error
}

// EventRow is the value-shape the EventsRepo persists. Kept
// here (application layer) rather than on the infra side so the
// persist policy does not need to import a database model.
type EventRow struct {
	Instance string
	Hash     string
	Event    EventKind
	From     qbit.StateGroup // empty when not a state_change
	To       qbit.StateGroup
	At       time.Time
}

// EventKind enumerates the event types persisted to
// qbit_torrent_events.event. The four values match PRD §4.6.
type EventKind string

const (
	EventAdded       EventKind = "added"
	EventStateChange EventKind = "state_change"
	EventCompleted   EventKind = "completed"
	EventDeleted     EventKind = "deleted"
)
