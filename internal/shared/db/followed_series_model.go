package database

import "time"

// FollowedSeriesModel persists the followed_series table (ADR-0015 Ф3 C1,
// migration 000046). One row per followed canon series — the global
// (pre-RBAC) watchlist. series_id is BOTH the PK and the FK to series(id)
// (CASCADE): the row is dead once OrphanSeries-GC hard-drops the canon.
// created_at is set by the writer (UTC); the DB default is a belt-and-braces
// fallback for out-of-band inserts.
type FollowedSeriesModel struct {
	SeriesID  int64     `gorm:"primaryKey;column:series_id"`
	CreatedAt time.Time `gorm:"column:created_at;not null"`
}

func (FollowedSeriesModel) TableName() string { return "followed_series" }
