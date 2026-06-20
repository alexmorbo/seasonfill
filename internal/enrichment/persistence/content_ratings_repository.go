package persistence

import (
	"context"
	"errors"
	"fmt"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/alexmorbo/seasonfill/application/ports"
	"github.com/alexmorbo/seasonfill/infrastructure/database"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
)

// ContentRating is the read-shape returned by ContentRatingsRepository.
// One row per (series_id, country_code) — TMDB's "TV-MA" for US,
// "16+" for RU, etc.
type ContentRating = database.ContentRatingModel

// ContentRatingsRepository persists the `content_ratings` table (PRD
// §5.3 row "content_ratings"). Composite PK (series_id, country_code)
// makes Upsert idempotent on the natural key.
type ContentRatingsRepository struct {
	db *gorm.DB
}

func NewContentRatingsRepository(db *gorm.DB) *ContentRatingsRepository {
	return &ContentRatingsRepository{db: db}
}

// Get fetches the row for (series_id, country_code). Returns
// ports.ErrNotFound on miss.
func (r *ContentRatingsRepository) Get(ctx context.Context, seriesID domain.SeriesID, countryCode string) (ContentRating, error) {
	var m database.ContentRatingModel
	err := dbFromContext(ctx, r.db).WithContext(ctx).
		Where("series_id = ? AND country_code = ?", seriesID, countryCode).
		First(&m).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ContentRating{}, ports.ErrNotFound
		}
		return ContentRating{}, fmt.Errorf("get content_rating: %w", err)
	}
	return m, nil
}

// ListBySeries returns every content rating row for seriesID ordered
// by country_code ASC.
func (r *ContentRatingsRepository) ListBySeries(ctx context.Context, seriesID domain.SeriesID) ([]ContentRating, error) {
	var models []database.ContentRatingModel
	err := dbFromContext(ctx, r.db).WithContext(ctx).
		Where("series_id = ?", seriesID).
		Order("country_code ASC").
		Find(&models).Error
	if err != nil {
		return nil, fmt.Errorf("list content_ratings: %w", err)
	}
	return models, nil
}

// Upsert writes a row by composite PK. Idempotent.
func (r *ContentRatingsRepository) Upsert(ctx context.Context, cr ContentRating) error {
	if cr.SeriesID == 0 {
		return fmt.Errorf("upsert content_rating: series_id must be non-zero")
	}
	if cr.CountryCode == "" {
		return fmt.Errorf("upsert content_rating: country_code must be non-empty")
	}
	if cr.Rating == "" {
		return fmt.Errorf("upsert content_rating: rating must be non-empty")
	}
	cr.UpdatedAt = time.Now().UTC()
	err := dbFromContext(ctx, r.db).WithContext(ctx).Clauses(clause.OnConflict{
		Columns: []clause.Column{
			{Name: "series_id"},
			{Name: "country_code"},
		},
		DoUpdates: clause.AssignmentColumns([]string{"rating", "updated_at"}),
	}).Create(&cr).Error
	if err != nil {
		return fmt.Errorf("upsert content_rating: %w", err)
	}
	return nil
}
