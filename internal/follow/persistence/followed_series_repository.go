// Package persistence holds the followed_series GORM adapter (ADR-0015 Ф3 C1).
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
var _ followapp.FollowStore = (*FollowedSeriesRepository)(nil)

// FollowedSeriesRepository persists + reads followed_series.
type FollowedSeriesRepository struct {
	db *gorm.DB
}

// NewFollowedSeriesRepository wires the repository to a GORM DB.
func NewFollowedSeriesRepository(db *gorm.DB) *FollowedSeriesRepository {
	return &FollowedSeriesRepository{db: db}
}

// Follow inserts a followed_series row idempotently: a second follow of the
// same series is ON CONFLICT DO NOTHING (no duplicate, no error). Mirrors the
// download_links InsertOnly idempotency pattern
// (internal/grab/persistence/download_link_repository.go:77).
func (r *FollowedSeriesRepository) Follow(ctx context.Context, userID int64, seriesID domain.SeriesID) error {
	m := database.FollowedSeriesModel{
		UserID:    userID,
		SeriesID:  int64(seriesID),
		CreatedAt: time.Now().UTC(),
	}
	res := dbtx.DBFromContext(ctx, r.db).WithContext(ctx).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "user_id"}, {Name: "series_id"}},
		DoNothing: true,
	}).Create(&m)
	if res.Error != nil {
		return fmt.Errorf("follow u=%d s=%d: %w", userID, int64(seriesID), res.Error)
	}
	return nil
}

// Unfollow deletes the followed_series row. Idempotent: deleting a
// non-existent row is a no-op that returns nil (a 200/204 either way).
func (r *FollowedSeriesRepository) Unfollow(ctx context.Context, userID int64, seriesID domain.SeriesID) error {
	err := dbtx.DBFromContext(ctx, r.db).WithContext(ctx).
		Where("user_id = ? AND series_id = ?", userID, int64(seriesID)).
		Delete(&database.FollowedSeriesModel{}).Error
	if err != nil {
		return fmt.Errorf("unfollow u=%d s=%d: %w", userID, int64(seriesID), err)
	}
	return nil
}

// ListFollowed returns the followed series as minimal cards, newest first.
// Reads canon series + the localized title (requested lang → en-US → canon
// original_title) and poster (requested lang → en-US). Bounded scan — the
// watchlist is a personal list, not a catalog. Portable across pg/sqlite
// (LEFT JOIN + COALESCE only, no dialect casts).
func (r *FollowedSeriesRepository) ListFollowed(ctx context.Context, userID int64, lang string) ([]followapp.FollowedItem, error) {
	const q = `
SELECT s.id            AS series_id,
       s.tmdb_id       AS tmdb_id,
       s.year          AS year,
       fs.created_at   AS followed_at,
       COALESCE(st_req.title, st_en.title, s.original_title) AS title,
       COALESCE(smt_req.poster_asset, smt_en.poster_asset)   AS poster_asset
  FROM followed_series fs
  JOIN series s ON s.id = fs.series_id
  LEFT JOIN series_texts st_req
    ON st_req.series_id = s.id AND st_req.language = ?
  LEFT JOIN series_texts st_en
    ON st_en.series_id = s.id AND st_en.language = 'en-US'
  LEFT JOIN series_media_texts smt_req
    ON smt_req.series_id = s.id AND smt_req.language = ?
  LEFT JOIN series_media_texts smt_en
    ON smt_en.series_id = s.id AND smt_en.language = 'en-US'
 WHERE fs.user_id = ?
 ORDER BY fs.created_at DESC
 LIMIT 500`

	type row struct {
		SeriesID    domain.SeriesID `gorm:"column:series_id"`
		TMDBID      *int64          `gorm:"column:tmdb_id"`
		Year        *int            `gorm:"column:year"`
		FollowedAt  time.Time       `gorm:"column:followed_at"`
		Title       *string         `gorm:"column:title"`
		PosterAsset *string         `gorm:"column:poster_asset"`
	}
	var rows []row
	if err := dbtx.DBFromContext(ctx, r.db).WithContext(ctx).
		Raw(q, lang, lang, userID).Scan(&rows).Error; err != nil {
		return nil, fmt.Errorf("list followed u=%d: %w", userID, err)
	}
	out := make([]followapp.FollowedItem, 0, len(rows))
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
		out = append(out, followapp.FollowedItem{
			SeriesID:    rw.SeriesID,
			TMDBID:      tmdb,
			Title:       title,
			PosterAsset: rw.PosterAsset,
			Year:        rw.Year,
			FollowedAt:  rw.FollowedAt,
		})
	}
	return out, nil
}
