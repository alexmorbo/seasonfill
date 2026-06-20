package persistence

import (
	"context"
	"errors"
	"fmt"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/alexmorbo/seasonfill/application/ports"
	"github.com/alexmorbo/seasonfill/infrastructure/database"
	"github.com/alexmorbo/seasonfill/internal/enrichment/domain/people"
)

// PersonBiographiesRepository persists the localised biography rows
// for a person. Mirrors EpisodeTextsRepository / SeriesTextsRepository
// (story 203) verbatim — same PK shape, same fallback semantics,
// same shared helper. The §5.6 fallback helper (pickLanguageFallback
// in i18n_texts.go) is reused unchanged — its `table` and
// `entityCol` arguments are caller-supplied constants, which is
// exactly the extension point this story needed.
type PersonBiographiesRepository struct {
	db *gorm.DB
}

func NewPersonBiographiesRepository(db *gorm.DB) *PersonBiographiesRepository {
	return &PersonBiographiesRepository{db: db}
}

// Get fetches the row for (person_id, language) exactly. Returns
// ports.ErrNotFound when no row matches.
func (r *PersonBiographiesRepository) Get(ctx context.Context, personID int64, language string) (people.PersonBiography, error) {
	var m database.PersonBiographyModel
	err := dbFromContext(ctx, r.db).WithContext(ctx).
		Where("person_id = ? AND language = ?", personID, language).
		First(&m).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return people.PersonBiography{}, ports.ErrNotFound
		}
		return people.PersonBiography{}, fmt.Errorf("get person_biographies: %w", err)
	}
	return toPersonBiography(m), nil
}

// GetWithFallback returns the row for the requested language, or
// the en-US fallback, or the first available row by language
// ascending. ports.ErrNotFound is the only NotFound sentinel.
func (r *PersonBiographiesRepository) GetWithFallback(ctx context.Context, personID int64, language string) (people.PersonBiography, error) {
	var m database.PersonBiographyModel
	if err := pickLanguageFallback(ctx, r.db, "person_biographies", "person_id", personID, language, &m); err != nil {
		return people.PersonBiography{}, err
	}
	if m.PersonID == 0 {
		return people.PersonBiography{}, ports.ErrNotFound
	}
	return toPersonBiography(m), nil
}

// Upsert writes a biography row by composite PK. Idempotent.
func (r *PersonBiographiesRepository) Upsert(ctx context.Context, b people.PersonBiography) error {
	if b.PersonID == 0 {
		return fmt.Errorf("upsert person_biographies: person_id must be non-zero")
	}
	if b.Language == "" {
		return fmt.Errorf("upsert person_biographies: language must be non-empty")
	}
	b.UpdatedAt = time.Now().UTC()
	m := fromPersonBiography(b)
	err := dbFromContext(ctx, r.db).WithContext(ctx).Clauses(clause.OnConflict{
		Columns: []clause.Column{
			{Name: "person_id"},
			{Name: "language"},
		},
		DoUpdates: clause.AssignmentColumns([]string{
			"biography", "updated_at",
		}),
	}).Create(&m).Error
	if err != nil {
		return fmt.Errorf("upsert person_biographies: %w", err)
	}
	return nil
}

func toPersonBiography(m database.PersonBiographyModel) people.PersonBiography {
	return people.PersonBiography{
		PersonID:  m.PersonID,
		Language:  m.Language,
		Biography: m.Biography,
		UpdatedAt: m.UpdatedAt,
	}
}

func fromPersonBiography(b people.PersonBiography) database.PersonBiographyModel {
	return database.PersonBiographyModel{
		PersonID:  b.PersonID,
		Language:  b.Language,
		Biography: b.Biography,
		UpdatedAt: b.UpdatedAt,
	}
}
