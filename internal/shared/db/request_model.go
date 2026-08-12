package database

import (
	"time"

	"gorm.io/datatypes"
)

// RequestModel persists the `requests` table (Ф8-U-2, migration 000056).
// A pending row is created by the permission-gated add path when the caller
// lacks auto_approve; approve/deny transition status and stamp approver_id.
//
// tmdb_id carries the add-flow content id: the TMDB id for movies, the TVDB
// id for TV (the add-to-sonarr flow is tvdb-keyed). media_type disambiguates
// the two id-spaces so the pending-dedup key never collides across verticals.
//
// payload is the full serialized AddSpec (instance / profile / folder / monitor
// flags) so approve can replay AddToSonarrUseCase.Add / AddToRadarrUseCase.Add
// faithfully. seasons mirrors the TV per-season selection as a first-class
// column (NULL for movies or no-override).
type RequestModel struct {
	ID         uint           `gorm:"primaryKey;column:id"`
	UserID     uint           `gorm:"column:user_id;not null"`
	MediaType  string         `gorm:"column:media_type;not null"`
	TMDBID     int64          `gorm:"column:tmdb_id;not null"`
	Seasons    datatypes.JSON `gorm:"column:seasons"` // NULL = movie / no per-season override
	Payload    datatypes.JSON `gorm:"column:payload;not null"`
	Status     string         `gorm:"column:status;not null"`
	ApproverID *uint          `gorm:"column:approver_id"`
	CreatedAt  time.Time      `gorm:"column:created_at;not null"`
	UpdatedAt  time.Time      `gorm:"column:updated_at;not null"`
}

func (RequestModel) TableName() string { return "requests" }
