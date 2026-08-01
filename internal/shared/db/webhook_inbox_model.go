package database

import (
	"time"

	"gorm.io/datatypes"
)

// WebhookInboxModel persists the webhook_inbox table (ADR 0005,
// migration 000042). It is the durable inbox for raw Sonarr webhook
// events: the handler validates + inserts a pending row and returns 200;
// the drainer (E3) claims pending rows FIFO by created_at, re-maps the
// raw payload, retries with backoff into next_attempt_at, and promotes to
// status='dead' at the attempt ceiling. Retention is delete-on-success —
// only pending/processing/dead rows persist, so the table stays tiny.
//
// Dialect notes (mirrors the migration E1 shipped):
//   - Payload is jsonb (pg) / text (sqlite). datatypes.JSON is the
//     codebase's jsonb/text transcode type (see DecisionModel.Intent,
//     models.go). NOT NULL: a valid webhook always carries a raw body and
//     the drainer re-maps it on every attempt.
//   - Status has no DB CHECK — the pending|processing|dead enum is owned
//     by the drainer state machine (mirrors enrichment_errors, whose
//     enums are app-enforced, not DB-constrained).
//   - NextAttemptAt / LeaseUntil are nullable (*time.Time): NULL
//     next_attempt_at = due-now on insert; NULL lease_until = not leased.
type WebhookInboxModel struct {
	ID            int64          `gorm:"primaryKey;autoIncrement;column:id"`
	InstanceName  string         `gorm:"column:instance_name;type:text;not null"`
	EventType     string         `gorm:"column:event_type;type:text;not null"`
	Payload       datatypes.JSON `gorm:"column:payload;not null"`
	Status        string         `gorm:"column:status;type:text;not null"`
	Attempts      int            `gorm:"column:attempts;not null"`
	NextAttemptAt *time.Time     `gorm:"column:next_attempt_at"`
	LeaseUntil    *time.Time     `gorm:"column:lease_until"`
	LastError     string         `gorm:"column:last_error;type:text"`
	CreatedAt     time.Time      `gorm:"column:created_at;not null"`
}

func (WebhookInboxModel) TableName() string { return "webhook_inbox" }
