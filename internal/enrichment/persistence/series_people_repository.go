package persistence

import (
	"context"
	"errors"
	"fmt"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/alexmorbo/seasonfill/application/ports"
	"github.com/alexmorbo/seasonfill/internal/enrichment/domain/people"
	database "github.com/alexmorbo/seasonfill/internal/shared/db"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
)

// SeriesPeopleRepository persists the `series_people` table.
// Natural key (series_id, tmdb_credit_id) — TMDB aggregate_credits
// is the dominant write source, so BatchUpsert is the primary write
// path (one INSERT … ON CONFLICT round-trip for N rows).
//
// ListByPerson covers the H-2 "Also in your library" reverse lookup
// — the single-column `series_people_person` index is what makes
// that query an index range rather than a full scan.
type SeriesPeopleRepository struct {
	db *gorm.DB
}

func NewSeriesPeopleRepository(db *gorm.DB) *SeriesPeopleRepository {
	return &SeriesPeopleRepository{db: db}
}

// Get fetches by primary key. Returns ports.ErrNotFound on miss.
func (r *SeriesPeopleRepository) Get(ctx context.Context, id int64) (people.SeriesCredit, error) {
	var m database.SeriesPersonModel
	err := dbFromContext(ctx, r.db).WithContext(ctx).
		Where("id = ?", id).First(&m).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return people.SeriesCredit{}, ports.ErrNotFound
		}
		return people.SeriesCredit{}, fmt.Errorf("get series_people: %w", err)
	}
	return toSeriesCredit(m), nil
}

// ListBySeries returns every series_people row for seriesID,
// ordered by (kind ASC, credit_order ASC NULLS LAST). Pass kind =
// "" to return both cast + crew; pass a SeriesCreditKind value to
// filter. Order matches the composite index `series_people_top` so
// the read is an index scan, not a sort.
func (r *SeriesPeopleRepository) ListBySeries(ctx context.Context, seriesID domain.SeriesID, kind people.SeriesCreditKind) ([]people.SeriesCredit, error) {
	q := dbFromContext(ctx, r.db).WithContext(ctx).
		Where("series_id = ?", seriesID)
	if kind != "" {
		if !kind.IsValid() {
			return nil, fmt.Errorf("list series_people: invalid kind %q", kind)
		}
		q = q.Where("kind = ?", string(kind))
	}
	var models []database.SeriesPersonModel
	if err := q.Order("kind ASC, credit_order ASC").Find(&models).Error; err != nil {
		return nil, fmt.Errorf("list series_people: %w", err)
	}
	out := make([]people.SeriesCredit, 0, len(models))
	for _, m := range models {
		out = append(out, toSeriesCredit(m))
	}
	return out, nil
}

// ListByPerson returns every series-level credit row for personID.
// Hits the dedicated `series_people_person` index. Used by H-2 to
// build the "Also in your library" list — composer JOINs the
// returned series_ids against series + series_cache for the
// per-instance presence check.
func (r *SeriesPeopleRepository) ListByPerson(ctx context.Context, personID int64) ([]people.SeriesCredit, error) {
	var models []database.SeriesPersonModel
	err := dbFromContext(ctx, r.db).WithContext(ctx).
		Where("person_id = ?", personID).
		Order("series_id ASC, kind ASC, credit_order ASC").
		Find(&models).Error
	if err != nil {
		return nil, fmt.Errorf("list series_people by person: %w", err)
	}
	out := make([]people.SeriesCredit, 0, len(models))
	for _, m := range models {
		out = append(out, toSeriesCredit(m))
	}
	return out, nil
}

// Upsert writes one credit row by natural key. Idempotent.
func (r *SeriesPeopleRepository) Upsert(ctx context.Context, c people.SeriesCredit) (int64, error) {
	ids, err := r.batchUpsert(ctx, []people.SeriesCredit{c})
	if err != nil {
		return 0, err
	}
	if len(ids) != 1 {
		return 0, fmt.Errorf("upsert series_people: expected 1 id, got %d", len(ids))
	}
	return ids[0], nil
}

// BatchUpsert writes N credit rows in ONE INSERT … ON CONFLICT
// round-trip. Returned slice mirrors input order; index i carries
// the assigned id for input i. Empty input returns empty slice +
// nil. The conflict target (series_id, tmdb_credit_id) MUST match
// the UQ index `series_people_credit` exactly — re-running TMDB
// aggregate_credits never duplicates.
func (r *SeriesPeopleRepository) BatchUpsert(ctx context.Context, credits []people.SeriesCredit) ([]int64, error) {
	return r.batchUpsert(ctx, credits)
}

func (r *SeriesPeopleRepository) batchUpsert(ctx context.Context, credits []people.SeriesCredit) ([]int64, error) {
	if len(credits) == 0 {
		return nil, nil
	}
	now := time.Now().UTC()
	models := make([]database.SeriesPersonModel, 0, len(credits))
	for _, c := range credits {
		if c.SeriesID == 0 {
			return nil, fmt.Errorf("upsert series_people: series_id must be non-zero")
		}
		if c.PersonID == 0 {
			return nil, fmt.Errorf("upsert series_people: person_id must be non-zero")
		}
		if c.TMDBCreditID == "" {
			return nil, fmt.Errorf("upsert series_people: tmdb_credit_id must be non-empty")
		}
		if !c.Kind.IsValid() {
			return nil, fmt.Errorf("upsert series_people: invalid kind %q", c.Kind)
		}
		if c.CreatedAt.IsZero() {
			c.CreatedAt = now
		}
		c.UpdatedAt = now
		models = append(models, fromSeriesCredit(c))
	}
	err := dbFromContext(ctx, r.db).WithContext(ctx).Clauses(clause.OnConflict{
		Columns: []clause.Column{
			{Name: "series_id"},
			{Name: "tmdb_credit_id"},
		},
		DoUpdates: clause.AssignmentColumns([]string{
			"person_id", "kind",
			"character_name", "department", "job",
			"credit_order", "episode_count",
			"updated_at",
		}),
	}).Create(&models).Error
	if err != nil {
		return nil, fmt.Errorf("batch upsert series_people: %w", err)
	}
	ids := make([]int64, len(models))
	for i, m := range models {
		ids[i] = m.ID
	}
	return ids, nil
}

func toSeriesCredit(m database.SeriesPersonModel) people.SeriesCredit {
	return people.SeriesCredit{
		ID:            m.ID,
		SeriesID:      m.SeriesID,
		PersonID:      m.PersonID,
		Kind:          people.SeriesCreditKind(m.Kind),
		TMDBCreditID:  m.TMDBCreditID,
		CharacterName: m.CharacterName,
		Department:    m.Department,
		Job:           m.Job,
		CreditOrder:   m.CreditOrder,
		EpisodeCount:  m.EpisodeCount,
		CreatedAt:     m.CreatedAt,
		UpdatedAt:     m.UpdatedAt,
	}
}

func fromSeriesCredit(c people.SeriesCredit) database.SeriesPersonModel {
	return database.SeriesPersonModel{
		ID:            c.ID,
		SeriesID:      c.SeriesID,
		PersonID:      c.PersonID,
		Kind:          string(c.Kind),
		TMDBCreditID:  c.TMDBCreditID,
		CharacterName: c.CharacterName,
		Department:    c.Department,
		Job:           c.Job,
		CreditOrder:   c.CreditOrder,
		EpisodeCount:  c.EpisodeCount,
		CreatedAt:     c.CreatedAt,
		UpdatedAt:     c.UpdatedAt,
	}
}
