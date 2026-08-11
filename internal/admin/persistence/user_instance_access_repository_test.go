package persistence

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	admin "github.com/alexmorbo/seasonfill/internal/admin/domain"
	ports "github.com/alexmorbo/seasonfill/internal/shared/dataports"
	database "github.com/alexmorbo/seasonfill/internal/shared/db"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
	sharedErrors "github.com/alexmorbo/seasonfill/internal/shared/errors"
	"github.com/alexmorbo/seasonfill/internal/shared/testhelpers"
)

// seedAccessUser inserts the single parent row the user_instance_access
// FK requires (users.id=1). Unlike user_instance_tags there is NO
// arr_instance FK — instance_name is a plain logical TEXT.
func seedAccessUser(t *testing.T, db *gorm.DB) {
	t.Helper()
	now := time.Now().UTC()
	require.NoError(t, db.Create(&database.UserModel{
		ID:         1,
		Username:   "alice",
		Role:       admin.RoleAdmin,
		AvatarMode: admin.AvatarModeAuto,
		CreatedAt:  now,
		UpdatedAt:  now,
	}).Error)
}

func TestUserInstanceAccessRepository_Get_NoRow_ErrNotFound(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			seedAccessUser(t, db)
			repo := NewUserInstanceAccessRepository(db)

			_, err := repo.Get(context.Background(), 1, domain.InstanceName("main"))
			require.Error(t, err)
			var typedErr *sharedErrors.UserNotFoundError
			require.True(t, errors.As(err, &typedErr))
			require.True(t, errors.Is(err, ports.ErrNotFound))
		})
	}
}

func TestUserInstanceAccessRepository_Upsert_Then_Get(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			seedAccessUser(t, db)
			repo := NewUserInstanceAccessRepository(db)
			ctx := context.Background()

			require.NoError(t, repo.Upsert(ctx, admin.UserInstanceAccess{
				UserID: 1, InstanceName: domain.InstanceName("main"), CanRequest: true,
			}))

			got, err := repo.Get(ctx, 1, domain.InstanceName("main"))
			require.NoError(t, err)
			assert.Equal(t, uint(1), got.UserID)
			assert.Equal(t, domain.InstanceName("main"), got.InstanceName)
			assert.True(t, got.CanRequest)
		})
	}
}

// TestUserInstanceAccessRepository_Upsert_Idempotent — re-Upsert flips
// can_request in place (OnConflict DoUpdates).
func TestUserInstanceAccessRepository_Upsert_Idempotent(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			seedAccessUser(t, db)
			repo := NewUserInstanceAccessRepository(db)
			ctx := context.Background()

			require.NoError(t, repo.Upsert(ctx, admin.UserInstanceAccess{
				UserID: 1, InstanceName: domain.InstanceName("main"), CanRequest: true,
			}))
			require.NoError(t, repo.Upsert(ctx, admin.UserInstanceAccess{
				UserID: 1, InstanceName: domain.InstanceName("main"), CanRequest: false,
			}))

			got, err := repo.Get(ctx, 1, domain.InstanceName("main"))
			require.NoError(t, err)
			assert.False(t, got.CanRequest, "Upsert must replace can_request in place")
		})
	}
}

func TestUserInstanceAccessRepository_ListByUser(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			seedAccessUser(t, db)
			repo := NewUserInstanceAccessRepository(db)
			ctx := context.Background()

			// Empty first.
			list, err := repo.ListByUser(ctx, 1)
			require.NoError(t, err)
			assert.Empty(t, list)

			require.NoError(t, repo.Upsert(ctx, admin.UserInstanceAccess{
				UserID: 1, InstanceName: domain.InstanceName("beta"), CanRequest: true,
			}))
			require.NoError(t, repo.Upsert(ctx, admin.UserInstanceAccess{
				UserID: 1, InstanceName: domain.InstanceName("alpha"), CanRequest: false,
			}))

			list, err = repo.ListByUser(ctx, 1)
			require.NoError(t, err)
			require.Len(t, list, 2)
			// ORDER BY instance_name ASC.
			assert.Equal(t, domain.InstanceName("alpha"), list[0].InstanceName)
			assert.Equal(t, domain.InstanceName("beta"), list[1].InstanceName)
		})
	}
}

func TestUserInstanceAccessRepository_DeleteByUser_Idempotent(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			seedAccessUser(t, db)
			repo := NewUserInstanceAccessRepository(db)
			ctx := context.Background()

			require.NoError(t, repo.Upsert(ctx, admin.UserInstanceAccess{
				UserID: 1, InstanceName: domain.InstanceName("main"), CanRequest: true,
			}))
			require.NoError(t, repo.DeleteByUser(ctx, 1))

			_, err := repo.Get(ctx, 1, domain.InstanceName("main"))
			require.Error(t, err)
			require.True(t, errors.Is(err, ports.ErrNotFound))

			// Idempotent: delete again + unknown user are no-ops.
			require.NoError(t, repo.DeleteByUser(ctx, 1))
			require.NoError(t, repo.DeleteByUser(ctx, 9999))
		})
	}
}
