package dataports

import (
	"context"
	"time"
)

// Notification-outbox row status (ADR-0016 N1). App-owned enum, no DB CHECK.
const (
	NotificationOutboxStatusPending = "pending"
	NotificationOutboxStatusSent    = "sent" // transient in-memory only; delete-on-success on disk
	NotificationOutboxStatusDead    = "dead"
)

// OutboxRow is the app projection of a notification_outbox row.
type OutboxRow struct {
	ID            int64
	EventType     string
	Payload       []byte
	Status        string
	Attempts      int
	NextAttemptAt *time.Time
	DedupKey      *string
	CreatedAt     time.Time
}

//go:generate moq -out notification_outbox_mock.go . OutboxRepository

// OutboxRepository persists notification_outbox. Insert routes through the
// tx-scoped DB from ctx (dbtx) so a caller-opened Transactor.Transaction makes
// the enqueue atomic with the source write (transactional outbox). The
// dispatcher claim/settle methods run standalone.
type OutboxRepository interface {
	// Insert enqueues a pending row. If DedupKey is non-nil AND a pending row
	// with the same dedup_key already exists, Insert is a silent no-op
	// (storm-collapse). CreatedAt stamped now() when zero.
	Insert(ctx context.Context, row OutboxRow) error
	// FetchDueBatch returns up to limit pending rows with
	// (next_attempt_at IS NULL OR next_attempt_at <= now), FIFO by created_at.
	FetchDueBatch(ctx context.Context, now time.Time, limit int) ([]OutboxRow, error)
	// MarkSent deletes the row (delete-on-success). Idempotent.
	MarkSent(ctx context.Context, id int64) error
	// Reschedule records a failed-but-retryable attempt: attempts++,
	// next_attempt_at=nextAttemptAt, status stays pending. ErrNotFound on unknown id.
	Reschedule(ctx context.Context, id int64, nextAttemptAt time.Time) error
	// MarkDead promotes to status=dead (attempt ceiling). Row stays for forensics.
	MarkDead(ctx context.Context, id int64) error
	// CountPending powers the dispatcher's pending-depth gauge.
	CountPending(ctx context.Context) (int64, error)
}

// OutboxEmitter is the narrow emit-only view of the notification outbox used
// by system-event sources (grab/webhook/watchdog/inbox). Insert enlists in the
// caller's tx via dbtx. nil-OK at every call site (feature-off = no emit).
// *persistence.OutboxRepository already satisfies this (it has Insert).
type OutboxEmitter interface {
	Insert(ctx context.Context, row OutboxRow) error
}

// NotifiedEventsRepository is the cross-time dedup ledger for the Ф4 N3
// calendar-event producers. MarkIfNew is the ONLY method: it INSERTs a
// (event_type, entity_key) marker with ON CONFLICT DO NOTHING and reports
// whether the row was newly created. A producer enqueues its outbox row
// only on created==true, inside the SAME tx, so a marker without a delivered
// signal (or vice-versa) is impossible.
type NotifiedEventsRepository interface {
	// MarkIfNew inserts (eventType, entityKey) if absent. Returns
	// created==true when a NEW marker row was written (caller should enqueue),
	// false when the marker already existed (storm/re-scan — skip). now stamps
	// first_seen_at on insert.
	MarkIfNew(ctx context.Context, eventType, entityKey string, now time.Time) (created bool, err error)
}

// NotificationAgent is the app projection of a notification_agents row.
// ConfigEncrypted stays encrypted end-to-end; decryption happens only at
// Send time inside the notifier. NEVER put a decrypted URL on this struct.
type NotificationAgent struct {
	ID              int64
	Name            string
	Enabled         bool
	ConfigEncrypted []byte
	EventTypes      []string
	CreatedAt       time.Time
}

//go:generate moq -out notification_agent_mock.go . NotificationAgentRepository

// NotificationAgentRepository persists notification_agents (admin CRUD + the
// dispatcher's enabled-agents load).
type NotificationAgentRepository interface {
	// Create stamps ownerID (Ф8-U-5 notification_agents.user_id) and persists.
	Create(ctx context.Context, ownerID int64, a NotificationAgent) (int64, error)
	List(ctx context.Context) ([]NotificationAgent, error)
	Get(ctx context.Context, id int64) (NotificationAgent, error) // ErrNotFound if absent
	// Update replaces name/enabled/event_types always; config_encrypted only when
	// newConfig != nil (nil = keep existing ciphertext — "empty URL on edit").
	Update(ctx context.Context, id int64, name string, enabled bool, eventTypes []string, newConfig []byte) error
	Delete(ctx context.Context, id int64) error // ErrNotFound if absent
	// ListEnabledForEvent returns enabled agents whose event_types contains eventType.
	ListEnabledForEvent(ctx context.Context, eventType string) ([]NotificationAgent, error)
}
