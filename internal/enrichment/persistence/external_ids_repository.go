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
	"github.com/alexmorbo/seasonfill/internal/enrichment/domain/enrichment"
)

// ExternalID is the read-shape returned by ExternalIDsRepository.
// Polymorphic row: (entity_type, entity_id, provider) is the natural
// key. entity_type domain (series|person|episode) is enforced at
// the boundary via the typed enrichment.EntityType enum.
type ExternalID = database.ExternalIDModel

// ExternalIDsRepository persists the `external_ids` table (PRD §5.3
// row "external_ids"). Polymorphic catch-all for the long tail of
// id providers (wikidata, facebook, instagram, twitter, etc.). Hot
// ids (tmdb/tvdb/imdb) are denormalised onto canon entities for
// join-performance; this table handles everything else.
type ExternalIDsRepository struct {
	db *gorm.DB
}

func NewExternalIDsRepository(db *gorm.DB) *ExternalIDsRepository {
	return &ExternalIDsRepository{db: db}
}

// Get fetches the row for (entity_type, entity_id, provider).
// Returns ports.ErrNotFound on miss.
func (r *ExternalIDsRepository) Get(ctx context.Context, entityType enrichment.EntityType, entityID int64, provider string) (ExternalID, error) {
	if !entityType.IsValid() {
		return ExternalID{}, fmt.Errorf("get external_id: invalid entity_type %q", entityType)
	}
	if provider == "" {
		return ExternalID{}, fmt.Errorf("get external_id: provider must be non-empty")
	}
	var m database.ExternalIDModel
	err := dbFromContext(ctx, r.db).WithContext(ctx).
		Where("entity_type = ? AND entity_id = ? AND provider = ?",
			string(entityType), entityID, provider).
		First(&m).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ExternalID{}, ports.ErrNotFound
		}
		return ExternalID{}, fmt.Errorf("get external_id: %w", err)
	}
	return m, nil
}

// ListByEntity returns every external_id row for an entity, ordered
// by provider ASC.
func (r *ExternalIDsRepository) ListByEntity(ctx context.Context, entityType enrichment.EntityType, entityID int64) ([]ExternalID, error) {
	if !entityType.IsValid() {
		return nil, fmt.Errorf("list external_ids: invalid entity_type %q", entityType)
	}
	var models []database.ExternalIDModel
	err := dbFromContext(ctx, r.db).WithContext(ctx).
		Where("entity_type = ? AND entity_id = ?", string(entityType), entityID).
		Order("provider ASC").
		Find(&models).Error
	if err != nil {
		return nil, fmt.Errorf("list external_ids: %w", err)
	}
	return models, nil
}

// Upsert writes a polymorphic row by composite PK
// (entity_type, entity_id, provider). Idempotent across all three
// entity types.
func (r *ExternalIDsRepository) Upsert(ctx context.Context, entityType enrichment.EntityType, entityID int64, provider, value string) error {
	if !entityType.IsValid() {
		return fmt.Errorf("upsert external_id: invalid entity_type %q", entityType)
	}
	if entityID == 0 {
		return fmt.Errorf("upsert external_id: entity_id must be non-zero")
	}
	if provider == "" {
		return fmt.Errorf("upsert external_id: provider must be non-empty")
	}
	if value == "" {
		return fmt.Errorf("upsert external_id: value must be non-empty")
	}
	m := database.ExternalIDModel{
		EntityType: string(entityType),
		EntityID:   entityID,
		Provider:   provider,
		Value:      value,
		UpdatedAt:  time.Now().UTC(),
	}
	err := dbFromContext(ctx, r.db).WithContext(ctx).Clauses(clause.OnConflict{
		Columns: []clause.Column{
			{Name: "entity_type"},
			{Name: "entity_id"},
			{Name: "provider"},
		},
		DoUpdates: clause.AssignmentColumns([]string{"value", "updated_at"}),
	}).Create(&m).Error
	if err != nil {
		return fmt.Errorf("upsert external_id: %w", err)
	}
	return nil
}
