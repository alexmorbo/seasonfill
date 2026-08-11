package persistence

import (
	"context"
	"errors"
	"fmt"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	admin "github.com/alexmorbo/seasonfill/internal/admin/domain"
	ports "github.com/alexmorbo/seasonfill/internal/shared/dataports"
	database "github.com/alexmorbo/seasonfill/internal/shared/db"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
	sharedErrors "github.com/alexmorbo/seasonfill/internal/shared/errors"
)

// UserInstanceAccessRepository is the GORM-backed CRUD surface for the
// `user_instance_access` table. Ф8-U-1 ships full CRUD + ListByUser with
// no production caller yet — U-2/U-5 wire consumers.
type UserInstanceAccessRepository struct{ db *gorm.DB }

// NewUserInstanceAccessRepository constructs a UserInstanceAccessRepository
// bound to db.
func NewUserInstanceAccessRepository(db *gorm.DB) *UserInstanceAccessRepository {
	return &UserInstanceAccessRepository{db: db}
}

// Get returns the (userID, instanceName) row. Returns ports.ErrNotFound
// (joined with UserNotFoundError) when no row matches.
func (r *UserInstanceAccessRepository) Get(ctx context.Context, userID uint, instanceName domain.InstanceName) (admin.UserInstanceAccess, error) {
	var m database.UserInstanceAccessModel
	err := dbFromContext(ctx, r.db).WithContext(ctx).
		Where("user_id = ? AND instance_name = ?", userID, instanceName).
		First(&m).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return admin.UserInstanceAccess{}, errors.Join(
				&sharedErrors.UserNotFoundError{},
				ports.ErrNotFound,
			)
		}
		return admin.UserInstanceAccess{}, fmt.Errorf("get user_instance_access: %w", err)
	}
	return userInstanceAccessModelToDomain(m), nil
}

// Upsert inserts or replaces the (user_id, instance_name) row.
// Idempotent: re-calling with the same key updates can_request in place.
func (r *UserInstanceAccessRepository) Upsert(ctx context.Context, a admin.UserInstanceAccess) error {
	m := userInstanceAccessDomainToModel(a)
	err := dbFromContext(ctx, r.db).WithContext(ctx).
		Clauses(clause.OnConflict{
			Columns: []clause.Column{
				{Name: "user_id"},
				{Name: "instance_name"},
			},
			DoUpdates: clause.AssignmentColumns([]string{"can_request"}),
		}).
		Create(&m).Error
	if err != nil {
		return fmt.Errorf("upsert user_instance_access: %w", err)
	}
	return nil
}

// ListByUser returns every access row for userID across all instances.
// Empty slice (not an error) when the user has no rows.
func (r *UserInstanceAccessRepository) ListByUser(ctx context.Context, userID uint) ([]admin.UserInstanceAccess, error) {
	var ms []database.UserInstanceAccessModel
	err := dbFromContext(ctx, r.db).WithContext(ctx).
		Where("user_id = ?", userID).
		Order("instance_name ASC").
		Find(&ms).Error
	if err != nil {
		return nil, fmt.Errorf("list user_instance_access by user: %w", err)
	}
	out := make([]admin.UserInstanceAccess, 0, len(ms))
	for _, m := range ms {
		out = append(out, userInstanceAccessModelToDomain(m))
	}
	return out, nil
}

// DeleteByUser removes every row for userID. Idempotent — deleting a user
// with no access rows is not an error.
func (r *UserInstanceAccessRepository) DeleteByUser(ctx context.Context, userID uint) error {
	err := dbFromContext(ctx, r.db).WithContext(ctx).
		Where("user_id = ?", userID).
		Delete(&database.UserInstanceAccessModel{}).Error
	if err != nil {
		return fmt.Errorf("delete user_instance_access by user: %w", err)
	}
	return nil
}

func userInstanceAccessDomainToModel(a admin.UserInstanceAccess) database.UserInstanceAccessModel {
	return database.UserInstanceAccessModel{
		UserID:       a.UserID,
		InstanceName: a.InstanceName,
		CanRequest:   a.CanRequest,
	}
}

func userInstanceAccessModelToDomain(m database.UserInstanceAccessModel) admin.UserInstanceAccess {
	return admin.UserInstanceAccess{
		UserID:       m.UserID,
		InstanceName: m.InstanceName,
		CanRequest:   m.CanRequest,
	}
}

var _ ports.UserInstanceAccessRepository = (*UserInstanceAccessRepository)(nil)
