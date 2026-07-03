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

// PersonCreditsTextsRepository persists per-language cast character names
// (person_credits_texts, PK (person_credit_id, language), FK →
// person_credits(id) CASCADE). S-G. COALESCE-protects character_name so a
// blank/nil write never wipes a previously-stored value (memory
// seasonfill-upsert-coalesce-pattern: bare excluded.* orphan branches trip
// SQLSTATE 42601 on Postgres). updated_at always takes the new value.
type PersonCreditsTextsRepository struct {
	db *gorm.DB
}

func NewPersonCreditsTextsRepository(db *gorm.DB) *PersonCreditsTextsRepository {
	return &PersonCreditsTextsRepository{db: db}
}

// Upsert writes one row by composite PK. Idempotent. Rejects a zero
// person_credit_id or empty language.
func (r *PersonCreditsTextsRepository) Upsert(ctx context.Context, t people.PersonCreditText) error {
	return r.BatchUpsert(ctx, []people.PersonCreditText{t})
}

// BatchUpsert writes N rows in one INSERT … ON CONFLICT round-trip.
// Empty input is a no-op. Validates every row before the write so a
// malformed row surfaces its error rather than being silently dropped.
func (r *PersonCreditsTextsRepository) BatchUpsert(ctx context.Context, texts []people.PersonCreditText) error {
	if len(texts) == 0 {
		return nil
	}
	now := time.Now().UTC()
	rows := make([]database.PersonCreditTextModel, 0, len(texts))
	for _, t := range texts {
		if t.PersonCreditID == 0 {
			return fmt.Errorf("upsert person_credits_texts: person_credit_id must be non-zero")
		}
		if t.Language == "" {
			return fmt.Errorf("upsert person_credits_texts: language must be non-empty")
		}
		rows = append(rows, database.PersonCreditTextModel{
			PersonCreditID: t.PersonCreditID,
			Language:       t.Language,
			CharacterName:  t.CharacterName,
			UpdatedAt:      now,
		})
	}
	err := dbFromContext(ctx, r.db).WithContext(ctx).Clauses(clause.OnConflict{
		Columns: []clause.Column{
			{Name: "person_credit_id"},
			{Name: "language"},
		},
		DoUpdates: clause.Assignments(map[string]any{
			"character_name": gorm.Expr("COALESCE(excluded.character_name, person_credits_texts.character_name)"),
			"updated_at":     gorm.Expr("excluded.updated_at"),
		}),
	}).CreateInBatches(&rows, 1000).Error
	if err != nil {
		return fmt.Errorf("batch upsert person_credits_texts: %w", err)
	}
	return nil
}
