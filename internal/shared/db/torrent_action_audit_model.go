package database

import "time"

// TorrentActionAuditModel persists the torrent_action_audit table
// (ADR-0013 Q2, migration 000044). One row per pause/resume/recheck
// attempt. result ∈ {ok, error}; actor is the auth.username context
// value ("api-key", a session username, or "" for bypass). No FK — the
// audit trail outlives grab_records rows and instance deletes (mirrors
// webhook_inbox / decisions standalone-audit pattern).
type TorrentActionAuditModel struct {
	ID           int64     `gorm:"primaryKey;autoIncrement;column:id"`
	InstanceName string    `gorm:"column:instance_name;type:text;not null"`
	Hash         string    `gorm:"column:hash;type:text;not null"`
	Action       string    `gorm:"column:action;type:text;not null"`
	Actor        string    `gorm:"column:actor;type:text;not null"`
	Result       string    `gorm:"column:result;type:text;not null"`
	CreatedAt    time.Time `gorm:"column:created_at;not null"`
}

func (TorrentActionAuditModel) TableName() string { return "torrent_action_audit" }
