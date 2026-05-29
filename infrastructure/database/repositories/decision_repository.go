package repositories

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/alexmorbo/seasonfill/application/ports"
	"github.com/alexmorbo/seasonfill/domain/decision"
	"github.com/alexmorbo/seasonfill/domain/release"
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
		ErrorDetail:     d.ErrorDetail,
		SupersededByID:  supersededByPtr(d.SupersededByID),
		CreatedAt:       d.CreatedAt,
	}
	if err := dbFromContext(ctx, r.db).WithContext(ctx).Create(&model).Error; err != nil {
		return fmt.Errorf("save decision: %w", err)
	}
	return nil
}

// GetByID returns the decision row by primary key, or ports.ErrNotFound.
func (r *DecisionRepository) GetByID(ctx context.Context, id uuid.UUID) (decision.Decision, error) {
	var model database.DecisionModel
	if err := dbFromContext(ctx, r.db).WithContext(ctx).First(&model, "id = ?", id.String()).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return decision.Decision{}, ports.ErrNotFound
		}
		return decision.Decision{}, fmt.Errorf("get decision: %w", err)
	}
	return toDecision(model)
}

func (r *DecisionRepository) UpdateSupersededBy(ctx context.Context, id, newID uuid.UUID) error {
	newIDStr := newID.String()
	res := dbFromContext(ctx, r.db).WithContext(ctx).
		Model(&database.DecisionModel{}).
		Where("id = ?", id.String()).
		Update("superseded_by_id", &newIDStr)
	if res.Error != nil {
		return fmt.Errorf("update superseded_by_id: %w", res.Error)
	}
	if res.RowsAffected == 0 {
		return ports.ErrNotFound
	}
	return nil
}

// ClearSupersededBy nulls the column. Used by the async rescan rollback
// path (the goroutine failed after the prelude already pre-applied the
// supersede pointer; the original must look live again).
func (r *DecisionRepository) ClearSupersededBy(ctx context.Context, id uuid.UUID) error {
	res := dbFromContext(ctx, r.db).WithContext(ctx).
		Model(&database.DecisionModel{}).
		Where("id = ?", id.String()).
		Update("superseded_by_id", gorm.Expr("NULL"))
	if res.Error != nil {
		return fmt.Errorf("clear superseded_by_id: %w", res.Error)
	}
	if res.RowsAffected == 0 {
		return ports.ErrNotFound
	}
	return nil
}

func (r *DecisionRepository) List(ctx context.Context, f ports.DecisionFilter, p ports.Pagination) ([]decision.Decision, *ports.Cursor, error) {
	if p.Limit <= 0 || p.Limit > ports.MaxListLimit {
		return nil, nil, fmt.Errorf("decision list: %w", ports.ErrInvalidLimit)
	}
	q := dbFromContext(ctx, r.db).WithContext(ctx).Model(&database.DecisionModel{})
	if f.ScanRunID != nil {
		q = q.Where("scan_run_id = ?", f.ScanRunID.String())
	}
	if f.Instance != nil {
		q = q.Where("instance_name = ?", *f.Instance)
	}
	if f.SeriesID != nil {
		q = q.Where("series_id = ?", *f.SeriesID)
	}
	if f.SeasonNumber != nil {
		q = q.Where("season_number = ?", *f.SeasonNumber)
	}
	if f.Decision != nil {
		q = q.Where("decision = ?", *f.Decision)
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
	var models []database.DecisionModel
	if err := q.Order("created_at DESC, id DESC").Limit(p.Limit + 1).Find(&models).Error; err != nil {
		return nil, nil, fmt.Errorf("decision list: %w", err)
	}
	var next *ports.Cursor
	if len(models) > p.Limit {
		last := models[p.Limit-1]
		next = &ports.Cursor{Timestamp: last.CreatedAt.UTC(), ID: last.ID}
		models = models[:p.Limit]
	}
	out := make([]decision.Decision, 0, len(models))
	for _, m := range models {
		d, err := toDecision(m)
		if err != nil {
			return nil, nil, fmt.Errorf("decision list: %w", err)
		}
		out = append(out, d)
	}
	return out, next, nil
}

func toDecision(m database.DecisionModel) (decision.Decision, error) {
	id, err := uuid.Parse(m.ID)
	if err != nil {
		return decision.Decision{}, fmt.Errorf("parse decision id: %w", err)
	}
	scanRunID, err := uuid.Parse(m.ScanRunID)
	if err != nil {
		return decision.Decision{}, fmt.Errorf("parse scan_run_id: %w", err)
	}
	var filtered []decision.FilteredCandidate
	if len(m.FilteredOut) > 0 {
		if err := json.Unmarshal(m.FilteredOut, &filtered); err != nil {
			return decision.Decision{}, fmt.Errorf("unmarshal filtered_out: %w", err)
		}
	}
	var selected *release.Scored
	if len(m.SelectedData) > 0 {
		var scored release.Scored
		if err := json.Unmarshal(m.SelectedData, &scored); err != nil {
			return decision.Decision{}, fmt.Errorf("unmarshal selected: %w", err)
		}
		selected = &scored
	}
	return decision.Decision{
		ID:              id,
		ScanRunID:       scanRunID,
		InstanceName:    m.InstanceName,
		SeriesID:        m.SeriesID,
		SeriesTitle:     m.SeriesTitle,
		SeasonNumber:    m.SeasonNumber,
		Outcome:         decision.Outcome(m.Decision),
		Reason:          decision.Reason(m.Reason),
		MissingCount:    m.MissingCount,
		ExistingCount:   m.ExistingCount,
		ReleasesFound:   m.ReleasesFound,
		CandidatesCount: m.CandidatesCount,
		FilteredOut:     filtered,
		Selected:        selected,
		DryRunWouldGrab: m.DryRunWouldGrab,
		ErrorDetail:     m.ErrorDetail,
		SupersededByID:  parseSupersededByPtr(m.SupersededByID),
		CreatedAt:       m.CreatedAt,
	}, nil
}

func supersededByPtr(id *uuid.UUID) *string {
	if id == nil {
		return nil
	}
	s := id.String()
	return &s
}

func parseSupersededByPtr(s *string) *uuid.UUID {
	if s == nil || *s == "" {
		return nil
	}
	id, err := uuid.Parse(*s)
	if err != nil {
		return nil
	}
	return &id
}

var _ ports.DecisionRepository = (*DecisionRepository)(nil)
