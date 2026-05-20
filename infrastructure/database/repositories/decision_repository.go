package repositories

import (
	"context"
	"encoding/json"
	"fmt"

	"gorm.io/gorm"

	"github.com/alexmorbo/seasonfill/domain/decision"
	"github.com/alexmorbo/seasonfill/infrastructure/database"
)

type DecisionRepository struct {
	db *gorm.DB
}

func NewDecisionRepository(db *gorm.DB) *DecisionRepository {
	return &DecisionRepository{db: db}
}

func (r *DecisionRepository) Save(ctx context.Context, d decision.Decision) error {
	filteredOut, err := json.Marshal(d.FilteredOut)
	if err != nil {
		return fmt.Errorf("marshal filtered_out: %w", err)
	}
	var selectedData []byte
	selectedGUID := ""
	if d.Selected != nil {
		selectedGUID = d.Selected.Release.GUID
		selectedData, err = json.Marshal(d.Selected)
		if err != nil {
			return fmt.Errorf("marshal selected: %w", err)
		}
	}
	model := database.DecisionModel{
		ID:              d.ID.String(),
		ScanRunID:       d.ScanRunID.String(),
		InstanceName:    d.InstanceName,
		SeriesID:        d.SeriesID,
		SeriesTitle:     d.SeriesTitle,
		SeasonNumber:    d.SeasonNumber,
		Decision:        string(d.Outcome),
		Reason:          string(d.Reason),
		MissingCount:    d.MissingCount,
		ExistingCount:   d.ExistingCount,
		ReleasesFound:   d.ReleasesFound,
		CandidatesCount: d.CandidatesCount,
		FilteredOut:     filteredOut,
		SelectedGUID:    selectedGUID,
		SelectedData:    selectedData,
		DryRunWouldGrab: d.DryRunWouldGrab,
		CreatedAt:       d.CreatedAt,
	}
	if err := r.db.WithContext(ctx).Create(&model).Error; err != nil {
		return fmt.Errorf("save decision: %w", err)
	}
	return nil
}
