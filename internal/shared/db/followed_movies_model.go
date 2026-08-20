package database

import (
	"time"

	"github.com/alexmorbo/seasonfill/internal/shared/domain"
)

// FollowedMovieModel persists followed_movies (migration 000063) — the movie
// mirror of FollowedSeriesModel. Composite identity (UserID, MovieID). Two
// CASCADE FKs: movie_id → movies(id) (dead once the canon is reclaimed) and
// user_id → users(id) (dead once the owner is deleted). created_at is set by
// the writer (UTC); the DB default is a belt-and-braces fallback.
type FollowedMovieModel struct {
	UserID    int64          `gorm:"primaryKey;column:user_id"`
	MovieID   domain.MovieID `gorm:"primaryKey;column:movie_id"`
	CreatedAt time.Time      `gorm:"column:created_at;not null"`
}

func (FollowedMovieModel) TableName() string { return "followed_movies" }
