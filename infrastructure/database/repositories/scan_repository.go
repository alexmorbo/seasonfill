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
			return ports.ScanRecord{}, ErrNotFound
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

var ErrNotFound = errors.New("scan not found")

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
