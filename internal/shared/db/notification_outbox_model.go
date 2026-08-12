package database

import (
	"time"

	"gorm.io/datatypes"
)

// NotificationOutboxModel persists notification_outbox (ADR-0016 N1,
// migration 000048). Durable outbox: domain writes a pending row in the
// same tx as the source event; the dispatcher (app/notification) claims
// due rows, fans them to subscribed agents, retries with backoff into
// next_attempt_at, and promotes to status='dead' at the attempt ceiling.
// Retention: delete-on-success (only pending/dead persist).
type NotificationOutboxModel struct {
	ID            int64          `gorm:"primaryKey;autoIncrement;column:id"`
	UserID        int64          `gorm:"column:user_id;not null"` // Ф8-U-5c target follower
	EventType     string         `gorm:"column:event_type;type:text;not null"`
	Payload       datatypes.JSON `gorm:"column:payload;not null"`
	Status        string         `gorm:"column:status;type:text;not null"`
	Attempts      int            `gorm:"column:attempts;not null"`
	NextAttemptAt *time.Time     `gorm:"column:next_attempt_at"`
	DedupKey      *string        `gorm:"column:dedup_key"`
	CreatedAt     time.Time      `gorm:"column:created_at;not null"`
}

func (NotificationOutboxModel) TableName() string { return "notification_outbox" }
