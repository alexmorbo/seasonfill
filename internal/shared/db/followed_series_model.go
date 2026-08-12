package database

import "time"

// FollowedSeriesModel persists followed_series (migration 000046; Ф8-U-5
// per-user 000058). Composite identity (UserID, SeriesID). Two CASCADE FKs:
// series_id → series(id) (dead once OrphanSeries-GC drops the canon) and
// user_id → users(id) (dead once the owner is deleted). created_at is set by
// the writer (UTC); the DB default is a belt-and-braces fallback.
type FollowedSeriesModel struct {
	UserID    int64     `gorm:"primaryKey;column:user_id"`
	SeriesID  int64     `gorm:"primaryKey;column:series_id"`
	CreatedAt time.Time `gorm:"column:created_at;not null"`
}

func (FollowedSeriesModel) TableName() string { return "followed_series" }
