package repositories

import (
	"context"
	"fmt"

	"github.com/google/uuid"
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

func (r *GrabRepository) List(ctx context.Context, f ports.GrabFilter, p ports.Pagination) ([]grab.Record, *ports.Cursor, error) {
	if p.Limit <= 0 || p.Limit > ports.MaxListLimit {
		return nil, nil, fmt.Errorf("grab list: %w", ports.ErrInvalidLimit)
	}
	q := dbFromContext(ctx, r.db).WithContext(ctx).Model(&database.GrabRecordModel{})
	if f.Instance != nil {
		q = q.Where("instance_name = ?", *f.Instance)
	}
	if f.SeriesID != nil {
		q = q.Where("series_id = ?", *f.SeriesID)
	}
	if f.SeasonNumber != nil {
		q = q.Where("season_number = ?", *f.SeasonNumber)
	}
	if f.Status != nil {
		q = q.Where("status = ?", *f.Status)
	}
	if f.From != nil {
		q = q.Where("created_at >= ?", *f.From)
	}
	if f.To != nil {
		q = q.Where("created_at < ?", *f.To)
	}
	if p.Cursor != nil {
		q = q.Where("(created_at, id) < (?, ?)", p.Cursor.Timestamp, p.Cursor.ID)
	}
	var models []database.GrabRecordModel
	if err := q.Order("created_at DESC, id DESC").Limit(p.Limit + 1).Find(&models).Error; err != nil {
		return nil, nil, fmt.Errorf("grab list: %w", err)
	}
	var next *ports.Cursor
	if len(models) > p.Limit {
		last := models[p.Limit-1]
		next = &ports.Cursor{Timestamp: last.CreatedAt.UTC(), ID: last.ID}
		models = models[:p.Limit]
	}
	out := make([]grab.Record, 0, len(models))
	for _, m := range models {
		rec, err := toGrabRecord(m)
		if err != nil {
			return nil, nil, fmt.Errorf("grab list: %w", err)
		}
		out = append(out, rec)
	}
	return out, next, nil
}

func toGrabRecord(m database.GrabRecordModel) (grab.Record, error) {
	id, err := uuid.Parse(m.ID)
	if err != nil {
		return grab.Record{}, fmt.Errorf("parse grab id: %w", err)
	}
	scanRunID, err := uuid.Parse(m.ScanRunID)
	if err != nil {
		return grab.Record{}, fmt.Errorf("parse scan_run_id: %w", err)
	}
	return grab.Record{
		ID:                id,
		InstanceName:      m.InstanceName,
		SeriesID:          m.SeriesID,
		SeriesTitle:       m.SeriesTitle,
		SeasonNumber:      m.SeasonNumber,
		ReleaseGUID:       m.ReleaseGUID,
		ReleaseTitle:      m.ReleaseTitle,
		IndexerID:         m.IndexerID,
		IndexerName:       m.IndexerName,
		CustomFormatScore: m.CustomFormatScore,
		Quality:           m.Quality,
		CoverageCount:     m.CoverageCount,
		Status:            grab.Status(m.Status),
		ErrorMessage:      m.ErrorMessage,
		ScanRunID:         scanRunID,
		Attempts:          m.Attempts,
		CreatedAt:         m.CreatedAt,
		UpdatedAt:         m.UpdatedAt,
	}, nil
}

// Ensure interface compliance at compile time.
var _ ports.GrabRepository = (*GrabRepository)(nil)
