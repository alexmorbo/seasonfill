package database

import (
	"time"

	"gorm.io/datatypes"
)

// NotificationAgentModel persists notification_agents (ADR-0016 N1,
// migration 000048). ConfigEncrypted is the AES-GCM ciphertext of the
// shoutrrr URL (nonce|ct|tag) — NEVER logged, NEVER returned decrypted.
// EventTypes is a JSON array of subscribed event_type strings.
type NotificationAgentModel struct {
	ID              int64          `gorm:"primaryKey;autoIncrement;column:id"`
	UserID          int64          `gorm:"column:user_id;not null"`
	Name            string         `gorm:"column:name;type:text;not null"`
	Enabled         bool           `gorm:"column:enabled;not null"`
	ConfigEncrypted []byte         `gorm:"column:config_encrypted;not null"`
	EventTypes      datatypes.JSON `gorm:"column:event_types;not null"`
	CreatedAt       time.Time      `gorm:"column:created_at;not null"`
}

func (NotificationAgentModel) TableName() string { return "notification_agents" }
