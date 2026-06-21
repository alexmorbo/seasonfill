package persistence

import (
	"context"
	"errors"
	"fmt"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	admin "github.com/alexmorbo/seasonfill/internal/admin/domain"
	ports "github.com/alexmorbo/seasonfill/internal/shared/dataports"
	database "github.com/alexmorbo/seasonfill/internal/shared/db"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
	sharedErrors "github.com/alexmorbo/seasonfill/internal/shared/errors"
)

// UserInstanceTagRepository is the GORM-backed CRUD surface for the
// `user_instance_tags` table. D-5 (466a) ships full CRUD with no
// production caller yet — the N-4 sf-<user> discovery TagResolver
// will wire the consumer in a later story.
type UserInstanceTagRepository struct{ db *gorm.DB }

// NewUserInstanceTagRepository constructs a UserInstanceTagRepository
// bound to db.
func NewUserInstanceTagRepository(db *gorm.DB) *UserInstanceTagRepository {
	return &UserInstanceTagRepository{db: db}
}

// Get returns the (userID, instanceName) row. Returns
// ports.ErrNotFound (joined with UserNotFoundError) when no row
// matches — the dedicated UserInstanceTagNotFoundError is deferred
// until N-4 picks the wire shape.
func (r *UserInstanceTagRepository) Get(ctx context.Context, userID uint, instanceName domain.InstanceName) (admin.UserInstanceTag, error) {
	var m database.UserInstanceTagModel
	err := dbFromContext(ctx, r.db).WithContext(ctx).
		Where("user_id = ? AND instance_name = ?", userID, instanceName).
		First(&m).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return admin.UserInstanceTag{}, errors.Join(
				&sharedErrors.UserNotFoundError{},
				ports.ErrNotFound,
			)
		}
		return admin.UserInstanceTag{}, fmt.Errorf("get user_instance_tag: %w", err)
	}
	return userInstanceTagModelToDomain(m), nil
}

// Upsert inserts or replaces the (user_id, instance_name) row.
// Idempotent: re-calling with the same key updates sonarr_tag_id and
// sonarr_tag_label in place.
func (r *UserInstanceTagRepository) Upsert(ctx context.Context, t admin.UserInstanceTag) error {
	now := time.Now().UTC()
	if t.CreatedAt.IsZero() {
		t.CreatedAt = now
	}
	t.UpdatedAt = now
	m := userInstanceTagDomainToModel(t)
	err := dbFromContext(ctx, r.db).WithContext(ctx).
		Clauses(clause.OnConflict{
			Columns: []clause.Column{
				{Name: "user_id"},
				{Name: "instance_name"},
			},
			DoUpdates: clause.AssignmentColumns([]string{
				"sonarr_tag_id",
				"sonarr_tag_label",
				"updated_at",
			}),
		}).
		Create(&m).Error
	if err != nil {
		return fmt.Errorf("upsert user_instance_tag: %w", err)
	}
	return nil
}

// DeleteByUser removes every row for userID across all instances. Used
// by user-deletion flows (future N-1 admin UI). Returns nil even when
// zero rows match (idempotent — deleting a user with no tag rows is
// not an error).
func (r *UserInstanceTagRepository) DeleteByUser(ctx context.Context, userID uint) error {
	err := dbFromContext(ctx, r.db).WithContext(ctx).
		Where("user_id = ?", userID).
		Delete(&database.UserInstanceTagModel{}).Error
	if err != nil {
		return fmt.Errorf("delete user_instance_tag by user: %w", err)
	}
	return nil
}

func userInstanceTagDomainToModel(t admin.UserInstanceTag) database.UserInstanceTagModel {
	return database.UserInstanceTagModel{
		UserID:         t.UserID,
		InstanceName:   t.InstanceName,
		SonarrTagID:    t.SonarrTagID,
		SonarrTagLabel: t.SonarrTagLabel,
		CreatedAt:      t.CreatedAt,
		UpdatedAt:      t.UpdatedAt,
	}
}

func userInstanceTagModelToDomain(m database.UserInstanceTagModel) admin.UserInstanceTag {
	return admin.UserInstanceTag{
		UserID:         m.UserID,
		InstanceName:   m.InstanceName,
		SonarrTagID:    m.SonarrTagID,
		SonarrTagLabel: m.SonarrTagLabel,
		CreatedAt:      m.CreatedAt,
		UpdatedAt:      m.UpdatedAt,
	}
}

var _ ports.UserInstanceTagRepository = (*UserInstanceTagRepository)(nil)
