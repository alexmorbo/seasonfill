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
