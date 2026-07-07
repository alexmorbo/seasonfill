package persistence

import (
	"context"
	"fmt"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/alexmorbo/seasonfill/internal/enrichment/domain/people"
	database "github.com/alexmorbo/seasonfill/internal/shared/db"
)

// PeopleTextsRepository persists per-language person display names
// (people_texts, PK (person_id, language), FK → people(id) CASCADE). Story
// 1083. COALESCE-protects name so a blank/nil write never wipes a
// previously-stored value (memory seasonfill-upsert-coalesce-pattern: bare
// excluded.* orphan branches trip SQLSTATE 42601 on Postgres). updated_at
// always takes the new value. Mirrors PersonCreditsTextsRepository verbatim.
type PeopleTextsRepository struct {
	db *gorm.DB
}

func NewPeopleTextsRepository(db *gorm.DB) *PeopleTextsRepository {
	return &PeopleTextsRepository{db: db}
}

// Upsert writes one row by composite PK. Idempotent. Rejects a zero person_id
// or empty language.
func (r *PeopleTextsRepository) Upsert(ctx context.Context, t people.PersonText) error {
	return r.BatchUpsert(ctx, []people.PersonText{t})
}

// BatchUpsert writes N rows in one INSERT … ON CONFLICT round-trip. Empty
// input is a no-op. Validates every row before the write so a malformed row
// surfaces its error rather than being silently dropped.
func (r *PeopleTextsRepository) BatchUpsert(ctx context.Context, texts []people.PersonText) error {
	if len(texts) == 0 {
		return nil
	}
	now := time.Now().UTC()
	rows := make([]database.PeopleTextModel, 0, len(texts))
	for _, t := range texts {
		if t.PersonID == 0 {
			return fmt.Errorf("upsert people_texts: person_id must be non-zero")
		}
		if t.Language == "" {
			return fmt.Errorf("upsert people_texts: language must be non-empty")
		}
		rows = append(rows, database.PeopleTextModel{
			PersonID:  t.PersonID,
			Language:  t.Language,
			Name:      t.Name,
			UpdatedAt: now,
		})
	}
	err := dbFromContext(ctx, r.db).WithContext(ctx).Clauses(clause.OnConflict{
		Columns: []clause.Column{
			{Name: "person_id"},
			{Name: "language"},
		},
		DoUpdates: clause.Assignments(map[string]any{
			"name":       gorm.Expr("COALESCE(excluded.name, people_texts.name)"),
			"updated_at": gorm.Expr("excluded.updated_at"),
		}),
	}).CreateInBatches(&rows, 1000).Error
	if err != nil {
		return fmt.Errorf("batch upsert people_texts: %w", err)
	}
	return nil
}
