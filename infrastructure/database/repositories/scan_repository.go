package repositories

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/alexmorbo/seasonfill/application/ports"
	"github.com/alexmorbo/seasonfill/infrastructure/database"
)

type ScanRepository struct {
	db *gorm.DB
}

func NewScanRepository(db *gorm.DB) *ScanRepository {
	return &ScanRepository{db: db}
}

func (r *ScanRepository) Create(ctx context.Context, rec ports.ScanRecord) error {
	model := toScanModel(rec)
	if err := r.db.WithContext(ctx).Create(&model).Error; err != nil {
		return fmt.Errorf("create scan: %w", err)
	}
	return nil
}

func (r *ScanRepository) Update(ctx context.Context, rec ports.ScanRecord) error {
	model := toScanModel(rec)
	if err := r.db.WithContext(ctx).Save(&model).Error; err != nil {
		return fmt.Errorf("update scan: %w", err)
	}
	return nil
}

func (r *ScanRepository) GetByID(ctx context.Context, id uuid.UUID) (ports.ScanRecord, error) {
	var model database.ScanRunModel
	if err := r.db.WithContext(ctx).First(&model, "id = ?", id.String()).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ports.ScanRecord{}, ports.ErrNotFound
		}
		return ports.ScanRecord{}, fmt.Errorf("get scan: %w", err)
	}
	return toScanRecord(model)
}

func (r *ScanRepository) MarkAborted(ctx context.Context, id uuid.UUID, reason string) error {
	res := r.db.WithContext(ctx).Model(&database.ScanRunModel{}).
		Where("id = ?", id.String()).
		Updates(map[string]any{
			"status":        "aborted",
			"error_message": reason,
		})
	if res.Error != nil {
		return fmt.Errorf("mark aborted: %w", res.Error)
	}
	return nil
}

func (r *ScanRepository) List(ctx context.Context, f ports.ScanFilter, p ports.Pagination) ([]ports.ScanRecord, *ports.Cursor, error) {
	if p.Limit <= 0 || p.Limit > ports.MaxListLimit {
		return nil, nil, fmt.Errorf("scan list: %w", ports.ErrInvalidLimit)
	}
	q := dbFromContext(ctx, r.db).WithContext(ctx).Model(&database.ScanRunModel{})
	if f.Instance != nil {
		q = q.Where("instance_name = ?", *f.Instance)
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
	var models []database.ScanRunModel
	if err := q.Order("created_at DESC, id DESC").Limit(p.Limit + 1).Find(&models).Error; err != nil {
		return nil, nil, fmt.Errorf("scan list: %w", err)
	}
	var next *ports.Cursor
	if len(models) > p.Limit {
		last := models[p.Limit-1]
		next = &ports.Cursor{Timestamp: last.CreatedAt.UTC(), ID: last.ID}
		models = models[:p.Limit]
	}
	out := make([]ports.ScanRecord, 0, len(models))
	for _, m := range models {
		rec, err := toScanRecord(m)
		if err != nil {
			return nil, nil, fmt.Errorf("scan list: %w", err)
		}
		out = append(out, rec)
	}
	return out, next, nil
}

func toScanModel(r ports.ScanRecord) database.ScanRunModel {
	m := database.ScanRunModel{
		ID:              r.ID.String(),
		InstanceName:    r.InstanceName,
		Trigger:         r.Trigger,
		StartedAt:       r.StartedAt,
		FinishedAt:      r.FinishedAt,
		Status:          r.Status,
		SeriesScanned:   r.SeriesScanned,
		CandidatesFound: r.CandidatesFound,
		GrabsPerformed:  r.GrabsPerformed,
		GrabsFailed:     r.GrabsFailed,
		ErrorsCount:     r.ErrorsCount,
		ErrorMessage:    r.ErrorMessage,
		DryRun:          r.DryRun,
	}
	return m
}

func toScanRecord(m database.ScanRunModel) (ports.ScanRecord, error) {
	id, err := uuid.Parse(m.ID)
	if err != nil {
		return ports.ScanRecord{}, fmt.Errorf("parse uuid: %w", err)
	}
	return ports.ScanRecord{
		ID:              id,
		InstanceName:    m.InstanceName,
		Trigger:         m.Trigger,
		CreatedAt:       m.CreatedAt.UTC(),
		StartedAt:       m.StartedAt,
		FinishedAt:      m.FinishedAt,
		Status:          m.Status,
		SeriesScanned:   m.SeriesScanned,
		CandidatesFound: m.CandidatesFound,
		GrabsPerformed:  m.GrabsPerformed,
		GrabsFailed:     m.GrabsFailed,
		ErrorsCount:     m.ErrorsCount,
		ErrorMessage:    m.ErrorMessage,
		DryRun:          m.DryRun,
	}, nil
}
