package persistence

import (
	"context"
	"errors"
	"fmt"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	ports "github.com/alexmorbo/seasonfill/internal/shared/dataports"
	database "github.com/alexmorbo/seasonfill/internal/shared/db"
)

// collection_texts_repository.go — F-08 S2 writer for the per-collection
// per-language localized side-table (collection_texts, migration 000068).
// SEPARATE from MovieCollectionsRepository (which owns the collections canon)
// exactly as MovieI18nSeeder is separate from MovieRepository: the enrichment
// path OWNS the (collection_id, language) row and COALESCE-guards name/overview
// so a language-poor refetch never blanks a richer stored value.
type CollectionTextsRepository struct{ db *gorm.DB }

func NewCollectionTextsRepository(db *gorm.DB) *CollectionTextsRepository {
	return &CollectionTextsRepository{db: db}
}

// IDByTMDBCollectionID resolves the collections LOCAL PK (collections.id) — the
// FK target of collection_texts.collection_id — from the raw TMDB collection id.
// ports.ErrNotFound when no collections row exists yet (the caller skips the
// i18n write; the row is minted by the canon UpsertCollection that runs first).
func (r *CollectionTextsRepository) IDByTMDBCollectionID(ctx context.Context, tmdbCollectionID int) (int64, error) {
	if tmdbCollectionID == 0 {
		return 0, fmt.Errorf("collection pk lookup: tmdb_collection_id must be non-zero")
	}
	var m database.CollectionModel
	err := dbFromContext(ctx, r.db).WithContext(ctx).
		Select("id").
		Where("tmdb_collection_id = ?", tmdbCollectionID).
		First(&m).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return 0, ports.ErrNotFound
		}
		return 0, fmt.Errorf("collection pk lookup: %w", err)
	}
	return m.ID, nil
}

// UpsertCollectionTexts inserts or COALESCE-updates the (collection_id, language)
// row. name/overview are passed as strings; empty → SQL NULL via
// nilIfEmptyMovieText, and the COALESCE then PRESERVES any prior non-NULL value
// (mirror of MovieI18nSeeder.UpsertEnriched). enriched_at + updated_at are
// always stamped fresh from enrichedAt.
func (r *CollectionTextsRepository) UpsertCollectionTexts(
	ctx context.Context,
	collectionID int64,
	language, name, overview string,
	posterAsset *string,
	enrichedAt time.Time,
) error {
	if collectionID == 0 {
		return fmt.Errorf("upsert collection_texts: collection_id must be non-zero")
	}
	if language == "" {
		return fmt.Errorf("upsert collection_texts: language required")
	}
	now := enrichedAt.UTC()
	m := database.CollectionTextModel{
		CollectionID: collectionID,
		Language:     language,
		Name:         nilIfEmptyMovieText(name),
		Overview:     nilIfEmptyMovieText(overview),
		PosterAsset:  posterAsset, // nil → NULL; COALESCE preserves a prior non-NULL poster
		EnrichedAt:   &now,
		UpdatedAt:    now,
	}
	err := dbFromContext(ctx, r.db).WithContext(ctx).
		Clauses(clause.OnConflict{
			Columns: []clause.Column{{Name: "collection_id"}, {Name: "language"}},
			DoUpdates: clause.Assignments(map[string]any{
				"name":         gorm.Expr("COALESCE(excluded.name, collection_texts.name)"),
				"overview":     gorm.Expr("COALESCE(excluded.overview, collection_texts.overview)"),
				"poster_asset": gorm.Expr("COALESCE(excluded.poster_asset, collection_texts.poster_asset)"),
				"enriched_at":  now,
				"updated_at":   now,
			}),
		}).
		Create(&m).Error
	if err != nil {
		return fmt.Errorf("upsert collection_texts: %w", err)
	}
	return nil
}
