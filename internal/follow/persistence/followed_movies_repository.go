package persistence

import (
	"context"
	"fmt"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	followapp "github.com/alexmorbo/seasonfill/internal/follow/app"
	database "github.com/alexmorbo/seasonfill/internal/shared/db"
	"github.com/alexmorbo/seasonfill/internal/shared/dbtx"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
)

// Compile-time proof the repo satisfies the app-layer store port.
var _ followapp.MovieFollowStore = (*FollowedMoviesRepository)(nil)

// FollowedMoviesRepository persists + reads followed_movies — the movie mirror
// of FollowedSeriesRepository (migration 000063).
type FollowedMoviesRepository struct {
	db *gorm.DB
}

// NewFollowedMoviesRepository wires the repository to a GORM DB.
func NewFollowedMoviesRepository(db *gorm.DB) *FollowedMoviesRepository {
	return &FollowedMoviesRepository{db: db}
}

// Follow inserts a followed_movies row idempotently: a second follow of the
// same movie is ON CONFLICT DO NOTHING (no duplicate, no error).
func (r *FollowedMoviesRepository) Follow(ctx context.Context, userID int64, movieID domain.MovieID) error {
	m := database.FollowedMovieModel{
		UserID:    userID,
		MovieID:   movieID,
		CreatedAt: time.Now().UTC(),
	}
	res := dbtx.DBFromContext(ctx, r.db).WithContext(ctx).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "user_id"}, {Name: "movie_id"}},
		DoNothing: true,
	}).Create(&m)
	if res.Error != nil {
		return fmt.Errorf("follow movie u=%d m=%d: %w", userID, int64(movieID), res.Error)
	}
	return nil
}

// Unfollow deletes the followed_movies row. Idempotent: deleting a
// non-existent row is a no-op that returns nil.
func (r *FollowedMoviesRepository) Unfollow(ctx context.Context, userID int64, movieID domain.MovieID) error {
	err := dbtx.DBFromContext(ctx, r.db).WithContext(ctx).
		Where("user_id = ? AND movie_id = ?", userID, int64(movieID)).
		Delete(&database.FollowedMovieModel{}).Error
	if err != nil {
		return fmt.Errorf("unfollow movie u=%d m=%d: %w", userID, int64(movieID), err)
	}
	return nil
}

// ListFollowed returns the followed movies as minimal cards, newest first.
// Reads canon movies + the localized title/poster from movie_i18n (requested
// lang → en-US → canon). Bounded scan — the watchlist is a personal list, not
// a catalog. Portable across pg/sqlite (LEFT JOIN + COALESCE only, no casts).
func (r *FollowedMoviesRepository) ListFollowed(ctx context.Context, userID int64, lang string) ([]followapp.FollowedMovieItem, error) {
	const q = `
SELECT m.id          AS movie_id,
       m.tmdb_id     AS tmdb_id,
       m.year        AS year,
       fm.created_at AS followed_at,
       COALESCE(mi_req.title, mi_en.title, m.title)                       AS title,
       COALESCE(mi_req.poster_asset, mi_en.poster_asset, m.poster_asset)  AS poster_asset
  FROM followed_movies fm
  JOIN movies m ON m.id = fm.movie_id
  LEFT JOIN movie_i18n mi_req
    ON mi_req.movie_id = m.id AND mi_req.language = ?
  LEFT JOIN movie_i18n mi_en
    ON mi_en.movie_id = m.id AND mi_en.language = 'en-US'
 WHERE fm.user_id = ?
 ORDER BY fm.created_at DESC
 LIMIT 500`

	type row struct {
		MovieID     domain.MovieID `gorm:"column:movie_id"`
		TMDBID      *int64         `gorm:"column:tmdb_id"`
		Year        *int           `gorm:"column:year"`
		FollowedAt  time.Time      `gorm:"column:followed_at"`
		Title       *string        `gorm:"column:title"`
		PosterAsset *string        `gorm:"column:poster_asset"`
	}
	var rows []row
	if err := dbtx.DBFromContext(ctx, r.db).WithContext(ctx).
		Raw(q, lang, userID).Scan(&rows).Error; err != nil {
		return nil, fmt.Errorf("list followed movies u=%d: %w", userID, err)
	}
	out := make([]followapp.FollowedMovieItem, 0, len(rows))
	for _, rw := range rows {
		var tmdb *domain.TMDBID
		if rw.TMDBID != nil {
			t := domain.TMDBID(*rw.TMDBID)
			tmdb = &t
		}
		title := ""
		if rw.Title != nil {
			title = *rw.Title
		}
		out = append(out, followapp.FollowedMovieItem{
			MovieID:     rw.MovieID,
			TMDBID:      tmdb,
			Title:       title,
			PosterAsset: rw.PosterAsset,
			Year:        rw.Year,
			FollowedAt:  rw.FollowedAt,
		})
	}
	return out, nil
}
