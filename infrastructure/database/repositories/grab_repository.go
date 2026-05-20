package repositories

import (
	"context"
	"fmt"

	"gorm.io/gorm"

	"github.com/alexmorbo/seasonfill/application/ports"
	"github.com/alexmorbo/seasonfill/domain/grab"
	"github.com/alexmorbo/seasonfill/infrastructure/database"
)

type GrabRepository struct {
	db *gorm.DB
}

func NewGrabRepository(db *gorm.DB) *GrabRepository {
	return &GrabRepository{db: db}
}

func (r *GrabRepository) Create(ctx context.Context, rec grab.Record) error {
	model := toGrabModel(rec)
	if err := dbFromContext(ctx, r.db).WithContext(ctx).Create(&model).Error; err != nil {
		return fmt.Errorf("create grab_record: %w", err)
	}
	return nil
}

func toGrabModel(r grab.Record) database.GrabRecordModel {
	return database.GrabRecordModel{
		ID:                r.ID.String(),
		InstanceName:      r.InstanceName,
		SeriesID:          r.SeriesID,
		SeriesTitle:       r.SeriesTitle,
		SeasonNumber:      r.SeasonNumber,
		ReleaseGUID:       r.ReleaseGUID,
		ReleaseTitle:      r.ReleaseTitle,
		IndexerID:         r.IndexerID,
		IndexerName:       r.IndexerName,
		CustomFormatScore: r.CustomFormatScore,
		Quality:           r.Quality,
		CoverageCount:     r.CoverageCount,
		Status:            string(r.Status),
		ErrorMessage:      r.ErrorMessage,
		ScanRunID:         r.ScanRunID.String(),
		Attempts:          r.Attempts,
		CreatedAt:         r.CreatedAt,
		UpdatedAt:         r.UpdatedAt,
	}
}

// Ensure interface compliance at compile time.
var _ ports.GrabRepository = (*GrabRepository)(nil)
