// row_config_repository.go persists ADR-0017 D-1 discovery_rows. S1 is
// read-only (List); the S2 write path (Reorder/Upsert/Delete) lands later.
package persistence

import (
	"context"
	"encoding/json"
	"fmt"

	"gorm.io/datatypes"
	"gorm.io/gorm"

	disco "github.com/alexmorbo/seasonfill/internal/discovery/domain"
)

// discoveryRowModel is the GORM row for discovery_rows. params is
// datatypes.JSON (jsonb pg / text sqlite — same transcode as decisions).
type discoveryRowModel struct {
	ID        int64          `gorm:"column:id;primaryKey;autoIncrement"`
	RowType   string         `gorm:"column:row_type"`
	Source    string         `gorm:"column:source"`
	MediaType string         `gorm:"column:media_type"`
	Params    datatypes.JSON `gorm:"column:params"`
	Position  int            `gorm:"column:position"`
	Enabled   bool           `gorm:"column:enabled"`
	Title     string         `gorm:"column:title"`
}

func (discoveryRowModel) TableName() string { return "discovery_rows" }

// RowConfigRepository reads discovery_rows.
type RowConfigRepository struct {
	db *gorm.DB
}

func NewRowConfigRepository(db *gorm.DB) *RowConfigRepository {
	if db == nil {
		panic("discovery row config repo: db required")
	}
	return &RowConfigRepository{db: db}
}

// List returns every discovery_rows row ordered by position ASC (id ASC
// tiebreak for determinism). Returns an empty (non-nil) slice when the
// table is empty — the handler substitutes the code-default set on empty.
func (r *RowConfigRepository) List(ctx context.Context) ([]disco.Row, error) {
	var models []discoveryRowModel
	if err := r.db.WithContext(ctx).
		Order("position ASC, id ASC").
		Find(&models).Error; err != nil {
		return nil, fmt.Errorf("discovery row config list: %w", err)
	}
	out := make([]disco.Row, 0, len(models))
	for _, m := range models {
		params := map[string]string{}
		if len(m.Params) > 0 {
			if err := json.Unmarshal(m.Params, &params); err != nil {
				return nil, fmt.Errorf("discovery row %d params decode: %w", m.ID, err)
			}
		}
		out = append(out, disco.Row{
			ID:        m.ID,
			RowType:   disco.RowType(m.RowType),
			Source:    disco.RowSource(m.Source),
			MediaType: disco.MediaType(m.MediaType),
			Params:    params,
			Position:  m.Position,
			Enabled:   m.Enabled,
			Title:     m.Title,
		})
	}
	return out, nil
}

// Replace atomically overwrites the entire discovery_rows table with rows,
// re-densifying position to the slice index (0..n-1). An empty slice clears
// the table (valid: GET then falls back to the code-default set). The whole
// operation is one tx — no partial state is ever visible. params is marshalled
// as the exact inverse of List()'s unmarshal (nil map → "{}", never null).
func (r *RowConfigRepository) Replace(ctx context.Context, rows []disco.Row) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// Global delete: AllowGlobalUpdate lifts GORM's missing-WHERE guard.
		if err := tx.Session(&gorm.Session{AllowGlobalUpdate: true}).
			Delete(&discoveryRowModel{}).Error; err != nil {
			return fmt.Errorf("discovery row replace: clear: %w", err)
		}
		if len(rows) == 0 {
			return nil
		}
		models := make([]discoveryRowModel, 0, len(rows))
		for i, row := range rows {
			params := row.Params
			if params == nil {
				params = map[string]string{}
			}
			raw, err := json.Marshal(params)
			if err != nil {
				return fmt.Errorf("discovery row %d params encode: %w", i, err)
			}
			models = append(models, discoveryRowModel{
				RowType:   string(row.RowType),
				Source:    string(row.Source),
				MediaType: string(row.MediaType),
				Params:    datatypes.JSON(raw),
				Position:  i, // dense 0..n-1, slice order authoritative
				Enabled:   row.Enabled,
				Title:     row.Title,
			})
		}
		if err := tx.Create(&models).Error; err != nil {
			return fmt.Errorf("discovery row replace: insert: %w", err)
		}
		return nil
	})
}

// DeleteAll clears discovery_rows (S2 reset-to-default: GET then serves
// domain.DefaultRows). Idempotent — clearing an empty table is a no-op success.
func (r *RowConfigRepository) DeleteAll(ctx context.Context) error {
	if err := r.db.WithContext(ctx).Session(&gorm.Session{AllowGlobalUpdate: true}).
		Delete(&discoveryRowModel{}).Error; err != nil {
		return fmt.Errorf("discovery row delete all: %w", err)
	}
	return nil
}
