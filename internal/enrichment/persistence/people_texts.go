package persistence

import (
	"context"
	"fmt"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/alexmorbo/seasonfill/internal/enrichment/domain/people"
	database "github.com/alexmorbo/seasonfill/internal/shared/db"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
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

// CastNameCoverage returns (covered, total) for Story 1084's SectionCast
// probe. total = distinct person_id credited to seriesID; covered =
// distinct person_id among those with a people_texts row (language == lang
// AND name IS NOT NULL). Mirrors SeriesTextsRepository.RecommendationsCoverage.
// Returns (0,0,nil) when the series has no cast/crew credits (incl. a
// Sonarr-orphan series with no TMDB id — the JOIN yields no rows).
//
// D-7 (468a) dropped the series_people table: the canonical cast/crew credit
// surface is now person_credits(media_type='tv', tmdb_media_id=series.tmdb_id),
// exactly as SeriesPeopleFromPersonCredits resolves it. The coverage query
// JOINs person_credits back to series on tmdb_id so it accepts the internal
// seriesID the probe already holds.
func (r *PeopleTextsRepository) CastNameCoverage(
	ctx context.Context,
	seriesID domain.SeriesID,
	language string,
) (covered, total int, err error) {
	db := dbFromContext(ctx, r.db).WithContext(ctx)

	// "tv" mirrors tmdb.MediaTypeTV — the media_type PersonWorker stamps for
	// series credits and the value SeriesPeopleFromPersonCredits filters on.
	const mediaTypeTV = "tv"
	base := func() *gorm.DB {
		return db.
			Table("person_credits AS pc").
			Joins("JOIN series s ON s.tmdb_id = pc.tmdb_media_id").
			Where("s.id = ? AND pc.media_type = ?", seriesID, mediaTypeTV)
	}

	var totalCnt int64
	if e := base().Distinct("pc.person_id").Count(&totalCnt).Error; e != nil {
		return 0, 0, fmt.Errorf("count cast persons: %w", e)
	}
	if totalCnt == 0 {
		return 0, 0, nil
	}

	var coveredCnt int64
	if e := base().
		Joins("JOIN people_texts pt ON pt.person_id = pc.person_id AND pt.language = ? AND pt.name IS NOT NULL", language).
		Distinct("pc.person_id").
		Count(&coveredCnt).Error; e != nil {
		return 0, 0, fmt.Errorf("count people_texts for cast: %w", e)
	}
	return int(coveredCnt), int(totalCnt), nil
}
