package persistence

import (
	"context"
	"fmt"
	"time"

	"gorm.io/gorm"

	"github.com/alexmorbo/seasonfill/infrastructure/database"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
)

// SeriesRecommendation is the read-shape returned by
// RecommendationsRepository. Self-joining row: recommended_series_id
// references series.id (typically a stub row).
type SeriesRecommendation = database.SeriesRecommendationModel

// RecommendationsRepository persists the `series_recommendations`
// table (PRD §5.3 row "series_recommendations"). Composite PK
// (series_id, recommended_series_id) makes Upsert + Set idempotent.
// Set replaces the full recommendation set for a series in one
// transaction, same shape as the taxonomy joins in 205.
type RecommendationsRepository struct {
	db *gorm.DB
}

func NewRecommendationsRepository(db *gorm.DB) *RecommendationsRepository {
	return &RecommendationsRepository{db: db}
}

// ListBySeries returns the recommended_series_ids in position-ASC
// order (NULL positions last). The composer JOINs the returned ids
// against series + series_cache to compute the per-instance "in
// library" badge.
func (r *RecommendationsRepository) ListBySeries(ctx context.Context, seriesID domain.SeriesID) ([]domain.SeriesID, error) {
	var rows []database.SeriesRecommendationModel
	err := dbFromContext(ctx, r.db).WithContext(ctx).
		Where("series_id = ?", seriesID).
		Order("position ASC, recommended_series_id ASC").
		Find(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("list series_recommendations: %w", err)
	}
	out := make([]domain.SeriesID, 0, len(rows))
	for _, row := range rows {
		out = append(out, row.RecommendedSeriesID)
	}
	return out, nil
}

// Upsert writes one recommendation row by composite PK. Idempotent.
// position is preserved as-passed. Most callers go through Set; this
// path exists for completeness + test convenience.
func (r *RecommendationsRepository) Upsert(ctx context.Context, rec SeriesRecommendation) error {
	if rec.SeriesID == 0 {
		return fmt.Errorf("upsert series_recommendations: series_id must be non-zero")
	}
	if rec.RecommendedSeriesID == 0 {
		return fmt.Errorf("upsert series_recommendations: recommended_series_id must be non-zero")
	}
	if rec.SeriesID == rec.RecommendedSeriesID {
		return fmt.Errorf("upsert series_recommendations: series_id must differ from recommended_series_id")
	}
	rec.UpdatedAt = time.Now().UTC()
	// Single-row replace-or-insert by PK. We can't use OnConflict
	// here uniformly across dialects for a composite-PK pure upsert
	// without an autoincrement id, so the Save() path (UPSERT
	// semantics on the embedded primary key tuple) is the canonical
	// shape.
	err := dbFromContext(ctx, r.db).WithContext(ctx).
		Save(&rec).Error
	if err != nil {
		return fmt.Errorf("upsert series_recommendations: %w", err)
	}
	return nil
}

// Set replaces the full series_recommendations set for seriesID with
// the given recommended ids, in a single transaction (DELETE +
// INSERT). Position is preserved as the input index (0-based) so
// callers can pass TMDB-ordered ids and get TMDB-ordered rows back.
// Idempotent: re-running with the same ids yields zero row delta in
// steady state.
//
// Empty ids slice clears the set for seriesID. Caller is responsible
// for the recommended series rows existing (typically stub rows
// upserted in the same enrichment batch — FK is application-side).
func (r *RecommendationsRepository) Set(ctx context.Context, seriesID domain.SeriesID, recommendedIDs []domain.SeriesID) error {
	if seriesID == 0 {
		return fmt.Errorf("set series_recommendations: series_id must be non-zero")
	}
	db := dbFromContext(ctx, r.db).WithContext(ctx)
	now := time.Now().UTC()
	return db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("series_id = ?", seriesID).
			Delete(&database.SeriesRecommendationModel{}).Error; err != nil {
			return fmt.Errorf("set series_recommendations: clear: %w", err)
		}
		if len(recommendedIDs) == 0 {
			return nil
		}
		rows := make([]database.SeriesRecommendationModel, 0, len(recommendedIDs))
		for i, rid := range recommendedIDs {
			if rid == seriesID {
				return fmt.Errorf("set series_recommendations: recommended_series_id must differ from series_id")
			}
			pos := i
			rows = append(rows, database.SeriesRecommendationModel{
				SeriesID:            seriesID,
				RecommendedSeriesID: rid,
				Position:            &pos,
				UpdatedAt:           now,
			})
		}
		if err := tx.Create(&rows).Error; err != nil {
			return fmt.Errorf("set series_recommendations: insert: %w", err)
		}
		return nil
	})
}
