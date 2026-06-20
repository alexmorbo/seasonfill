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

// PersonCredit is the read-shape returned by PersonCreditsRepository.
// PosterPath is an upstream TMDB image path string in v1 — the media
// downloader picks it up lazily on person-page open.
type PersonCredit = database.PersonCreditModel

// PersonCreditsRepository persists the `person_credits` table (PRD
// §5.3 row "person_credits"). Natural key
// (person_id, tmdb_credit_id) — TMDB
// /person/{id}/tv_credits + /movie_credits is the dominant write
// source, so BatchUpsert is the primary write path (one INSERT … ON
// CONFLICT round-trip for N rows).
//
// ListByPerson covers the person-page full filmography list;
// ListByMedia covers the reverse lookup "who from my library
// appears in this TMDB title?" on the person page's "More library
// credits" list.
type PersonCreditsRepository struct {
	db *gorm.DB
}

func NewPersonCreditsRepository(db *gorm.DB) *PersonCreditsRepository {
	return &PersonCreditsRepository{db: db}
}

// Get fetches by primary key. Returns ports.ErrNotFound on miss.
func (r *PersonCreditsRepository) Get(ctx context.Context, id int64) (PersonCredit, error) {
	var m database.PersonCreditModel
	err := dbFromContext(ctx, r.db).WithContext(ctx).
		Where("id = ?", id).First(&m).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return PersonCredit{}, ports.ErrNotFound
		}
		return PersonCredit{}, fmt.Errorf("get person_credit: %w", err)
	}
	return m, nil
}

// ListByPerson returns every credit row for personID, ordered by
// (year DESC NULLS LAST, title ASC). PRD §5.6 read path: the H-1
// person page sorts library credits by "recent" (last_aired_at) and
// other_credits by year/title; the year DESC ordering is the
// closest cheap approximation against TMDB-only data.
func (r *PersonCreditsRepository) ListByPerson(ctx context.Context, personID int64) ([]PersonCredit, error) {
	var models []database.PersonCreditModel
	err := dbFromContext(ctx, r.db).WithContext(ctx).
		Where("person_id = ?", personID).
		Order("year DESC, title ASC").
		Find(&models).Error
	if err != nil {
		return nil, fmt.Errorf("list person_credits by person: %w", err)
	}
	return models, nil
}

// ListByMedia returns every credit row for the given media
// (media_type, tmdb_media_id) — the reverse lookup that powers
// "who from my library appears in this TMDB title?". Hits the
// `person_credits_media` index.
func (r *PersonCreditsRepository) ListByMedia(ctx context.Context, mediaType string, tmdbMediaID int) ([]PersonCredit, error) {
	if mediaType == "" {
		return nil, fmt.Errorf("list person_credits by media: media_type must be non-empty")
	}
	var models []database.PersonCreditModel
	err := dbFromContext(ctx, r.db).WithContext(ctx).
		Where("media_type = ? AND tmdb_media_id = ?", mediaType, tmdbMediaID).
		Order("person_id ASC").
		Find(&models).Error
	if err != nil {
		return nil, fmt.Errorf("list person_credits by media: %w", err)
	}
	return models, nil
}

// Upsert writes one credit row by natural key. Idempotent.
func (r *PersonCreditsRepository) Upsert(ctx context.Context, pc PersonCredit) (int64, error) {
	ids, err := r.batchUpsert(ctx, []PersonCredit{pc})
	if err != nil {
		return 0, err
	}
	if len(ids) != 1 {
		return 0, fmt.Errorf("upsert person_credit: expected 1 id, got %d", len(ids))
	}
	return ids[0], nil
}

// BatchUpsert writes N credit rows in ONE INSERT … ON CONFLICT
// round-trip. Returned slice mirrors input order; index i carries
// the assigned id for input i. Empty input returns empty slice + nil.
// The conflict target (person_id, tmdb_credit_id) MUST match the UQ
// index `person_credits_credit` exactly — re-running TMDB
// /person/{id}/tv_credits never duplicates.
func (r *PersonCreditsRepository) BatchUpsert(ctx context.Context, credits []PersonCredit) ([]int64, error) {
	return r.batchUpsert(ctx, credits)
}

func (r *PersonCreditsRepository) batchUpsert(ctx context.Context, credits []PersonCredit) ([]int64, error) {
	if len(credits) == 0 {
		return nil, nil
	}
	now := time.Now().UTC()
	models := make([]database.PersonCreditModel, 0, len(credits))
	for _, c := range credits {
		if c.PersonID == 0 {
			return nil, fmt.Errorf("upsert person_credit: person_id must be non-zero")
		}
		if c.TMDBCreditID == "" {
			return nil, fmt.Errorf("upsert person_credit: tmdb_credit_id must be non-empty")
		}
		if c.MediaType == "" {
			return nil, fmt.Errorf("upsert person_credit: media_type must be non-empty")
		}
		if c.TMDBMediaID == 0 {
			return nil, fmt.Errorf("upsert person_credit: tmdb_media_id must be non-zero")
		}
		if c.Title == "" {
			return nil, fmt.Errorf("upsert person_credit: title must be non-empty")
		}
		if c.Kind == "" {
			return nil, fmt.Errorf("upsert person_credit: kind must be non-empty")
		}
		if c.CreatedAt.IsZero() {
			c.CreatedAt = now
		}
		c.UpdatedAt = now
		models = append(models, c)
	}
	err := dbFromContext(ctx, r.db).WithContext(ctx).Clauses(clause.OnConflict{
		Columns: []clause.Column{
			{Name: "person_id"},
			{Name: "tmdb_credit_id"},
		},
		DoUpdates: clause.AssignmentColumns([]string{
			"media_type", "tmdb_media_id",
			"title", "original_title", "year",
			"character_name", "kind", "department", "job",
			"poster_path", "vote_average", "tmdb_votes", "episode_count",
			"updated_at",
		}),
	}).Create(&models).Error
	if err != nil {
		return nil, fmt.Errorf("batch upsert person_credits: %w", err)
	}
	ids := make([]int64, len(models))
	for i, m := range models {
		ids[i] = m.ID
	}
	return ids, nil
}
