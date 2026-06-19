package repositories

import (
	"context"
	"errors"
	"fmt"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/alexmorbo/seasonfill/application/ports"
	"github.com/alexmorbo/seasonfill/domain/people"
	"github.com/alexmorbo/seasonfill/infrastructure/database"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
	sharedErrors "github.com/alexmorbo/seasonfill/internal/shared/errors"
)

// EpisodePeopleRepository persists the `episode_people` table.
// Natural key (episode_id, tmdb_credit_id). BatchUpsert is the
// primary write path (per-season TMDB enrichment yields tens of
// rows per episode).
type EpisodePeopleRepository struct {
	db *gorm.DB
}

func NewEpisodePeopleRepository(db *gorm.DB) *EpisodePeopleRepository {
	return &EpisodePeopleRepository{db: db}
}

// Get fetches by primary key. Returns ports.ErrNotFound on miss.
func (r *EpisodePeopleRepository) Get(ctx context.Context, id int64) (people.EpisodeCredit, error) {
	var m database.EpisodePersonModel
	err := dbFromContext(ctx, r.db).WithContext(ctx).
		Where("id = ?", id).First(&m).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return people.EpisodeCredit{}, errors.Join(
				&sharedErrors.EpisodeNotFoundError{},
				ports.ErrNotFound,
			)
		}
		return people.EpisodeCredit{}, fmt.Errorf("get episode_people: %w", err)
	}
	return toEpisodeCredit(m), nil
}

// ListByEpisode returns every episode_people row for episodeID,
// ordered by (kind ASC, credit_order ASC NULLS LAST). Pass kind =
// "" to return both guest_star + crew; pass an EpisodeCreditKind to
// filter.
func (r *EpisodePeopleRepository) ListByEpisode(ctx context.Context, episodeID domain.EpisodeID, kind people.EpisodeCreditKind) ([]people.EpisodeCredit, error) {
	q := dbFromContext(ctx, r.db).WithContext(ctx).
		Where("episode_id = ?", episodeID)
	if kind != "" {
		if !kind.IsValid() {
			return nil, fmt.Errorf("list episode_people: invalid kind %q", kind)
		}
		q = q.Where("kind = ?", string(kind))
	}
	var models []database.EpisodePersonModel
	if err := q.Order("kind ASC, credit_order ASC").Find(&models).Error; err != nil {
		return nil, fmt.Errorf("list episode_people: %w", err)
	}
	out := make([]people.EpisodeCredit, 0, len(models))
	for _, m := range models {
		out = append(out, toEpisodeCredit(m))
	}
	return out, nil
}

// Upsert writes one credit row by natural key. Idempotent.
func (r *EpisodePeopleRepository) Upsert(ctx context.Context, c people.EpisodeCredit) (int64, error) {
	ids, err := r.batchUpsert(ctx, []people.EpisodeCredit{c})
	if err != nil {
		return 0, err
	}
	if len(ids) != 1 {
		return 0, fmt.Errorf("upsert episode_people: expected 1 id, got %d", len(ids))
	}
	return ids[0], nil
}

// BatchUpsert writes N credit rows in ONE INSERT … ON CONFLICT
// round-trip. Conflict target (episode_id, tmdb_credit_id) matches
// UQ `episode_people_credit` exactly. Returned slice mirrors input
// order.
func (r *EpisodePeopleRepository) BatchUpsert(ctx context.Context, credits []people.EpisodeCredit) ([]int64, error) {
	return r.batchUpsert(ctx, credits)
}

func (r *EpisodePeopleRepository) batchUpsert(ctx context.Context, credits []people.EpisodeCredit) ([]int64, error) {
	if len(credits) == 0 {
		return nil, nil
	}
	now := time.Now().UTC()
	models := make([]database.EpisodePersonModel, 0, len(credits))
	for _, c := range credits {
		if c.EpisodeID == 0 {
			return nil, fmt.Errorf("upsert episode_people: episode_id must be non-zero")
		}
		if c.PersonID == 0 {
			return nil, fmt.Errorf("upsert episode_people: person_id must be non-zero")
		}
		if c.TMDBCreditID == "" {
			return nil, fmt.Errorf("upsert episode_people: tmdb_credit_id must be non-empty")
		}
		if !c.Kind.IsValid() {
			return nil, fmt.Errorf("upsert episode_people: invalid kind %q", c.Kind)
		}
		if c.CreatedAt.IsZero() {
			c.CreatedAt = now
		}
		c.UpdatedAt = now
		models = append(models, fromEpisodeCredit(c))
	}
	err := dbFromContext(ctx, r.db).WithContext(ctx).Clauses(clause.OnConflict{
		Columns: []clause.Column{
			{Name: "episode_id"},
			{Name: "tmdb_credit_id"},
		},
		DoUpdates: clause.AssignmentColumns([]string{
			"person_id", "kind",
			"character_name", "department", "job",
			"credit_order",
			"updated_at",
		}),
	}).Create(&models).Error
	if err != nil {
		return nil, fmt.Errorf("batch upsert episode_people: %w", err)
	}
	ids := make([]int64, len(models))
	for i, m := range models {
		ids[i] = m.ID
	}
	return ids, nil
}

func toEpisodeCredit(m database.EpisodePersonModel) people.EpisodeCredit {
	return people.EpisodeCredit{
		ID:            m.ID,
		EpisodeID:     m.EpisodeID,
		PersonID:      m.PersonID,
		Kind:          people.EpisodeCreditKind(m.Kind),
		TMDBCreditID:  m.TMDBCreditID,
		CharacterName: m.CharacterName,
		Department:    m.Department,
		Job:           m.Job,
		CreditOrder:   m.CreditOrder,
		CreatedAt:     m.CreatedAt,
		UpdatedAt:     m.UpdatedAt,
	}
}

func fromEpisodeCredit(c people.EpisodeCredit) database.EpisodePersonModel {
	return database.EpisodePersonModel{
		ID:            c.ID,
		EpisodeID:     c.EpisodeID,
		PersonID:      c.PersonID,
		Kind:          string(c.Kind),
		TMDBCreditID:  c.TMDBCreditID,
		CharacterName: c.CharacterName,
		Department:    c.Department,
		Job:           c.Job,
		CreditOrder:   c.CreditOrder,
		CreatedAt:     c.CreatedAt,
		UpdatedAt:     c.UpdatedAt,
	}
}
