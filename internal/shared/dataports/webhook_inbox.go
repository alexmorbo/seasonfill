package dataports

import (
	"context"
	"time"
)

// Webhook-inbox row status values (ADR 0005). The enum is app-owned —
// there is no DB CHECK constraint on the status column.
const (
	WebhookInboxStatusPending    = "pending"
	WebhookInboxStatusProcessing = "processing"
	WebhookInboxStatusDead       = "dead"
)

// WebhookInboxRow is the application-layer projection of a webhook_inbox
// row (ADR 0005). Payload is the raw Sonarr webhook body verbatim
// (jsonb on pg / text on sqlite); it is re-mapped on drain, not on
// enqueue, so the stored bytes are exactly what Sonarr sent (forensics).
type WebhookInboxRow struct {
	ID            int64
	InstanceName  string
	EventType     string
	Payload       []byte
	Status        string
	Attempts      int
	NextAttemptAt *time.Time
	LeaseUntil    *time.Time
	LastError     string
	CreatedAt     time.Time
}

//go:generate moq -out webhook_inbox_mock.go . WebhookInboxRepository

// WebhookInboxRepository is the durable-inbox persistence port (ADR 0005).
// The webhook handler (E4) enqueues via Insert inside a
// ports.Transactor.Transaction so the durable write is atomic with any
// sibling write; the drainer (E3) claims/settles rows via the remaining
// methods.
//
// Time computation is caller-side: ClaimDue takes the claim `now` and the
// `leaseUntil` stamp, MarkFailure takes the already-computed
// `nextAttemptAt`. The repository persists what it is told — the backoff
// schedule and retry-vs-dead-letter decision live in the drainer (E3).
type WebhookInboxRepository interface {
	// Insert enqueues a pending row. Routes through the tx-scoped DB from
	// ctx (dbFromContext), so a caller-opened Transactor.Transaction makes
	// the enqueue atomic with sibling writes. CreatedAt is stamped now()
	// when zero. Rejects empty instance/event_type/payload.
	Insert(ctx context.Context, row WebhookInboxRow) error

	// ClaimDue atomically claims up to `limit` due-pending rows FIFO by
	// created_at, flipping them pending->processing and stamping
	// leaseUntil. "Due" = next_attempt_at IS NULL OR next_attempt_at <=
	// now. Returns the claimed rows (now status=processing). Portable
	// two-step conditional UPDATE inside one transaction — no SKIP LOCKED,
	// no RETURNING (F-07).
	ClaimDue(ctx context.Context, now, leaseUntil time.Time, limit int) ([]WebhookInboxRow, error)

	// MarkSuccess deletes the row (delete-on-success retention, ADR
	// Decision 7). Idempotent: a missing row is not an error.
	MarkSuccess(ctx context.Context, id int64) error

	// MarkFailure records a failed-but-retryable attempt: attempts++,
	// next_attempt_at=nextAttemptAt, last_error=lastErr, status back to
	// pending, lease_until cleared. ErrNotFound on unknown id.
	MarkFailure(ctx context.Context, id int64, lastErr string, nextAttemptAt time.Time) error

	// MarkDead promotes the row to status=dead (attempt ceiling reached,
	// ADR Decision 6): last_error=lastErr, lease_until cleared, row stays
	// for forensics. ErrNotFound on unknown id.
	MarkDead(ctx context.Context, id int64, lastErr string) error

	// ReclaimStale returns rows stuck in status=processing whose
	// lease_until < now (a crash mid-process) back to pending and clears
	// their lease. attempts is left untouched — a crashed attempt did not
	// consume the retry budget. Returns the number of rows reclaimed.
	ReclaimStale(ctx context.Context, now time.Time) (int64, error)
}
