package persistence

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	admin "github.com/alexmorbo/seasonfill/internal/admin/domain"
	ports "github.com/alexmorbo/seasonfill/internal/shared/dataports"
	sharedErrors "github.com/alexmorbo/seasonfill/internal/shared/errors"
	"github.com/alexmorbo/seasonfill/internal/shared/testhelpers"
)

// Ф8-U-6b repo surface: List / GetByID / CountAdmins / UpdateRole /
// UpdatePermissions / Delete / InTx. D-0 bar: AllBackends + NULL/error pairs.

func TestAdminUserRepo_List_EmptyAndOrdered(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			repo := NewUserRepository(backend.NewDB(t))
			ctx := context.Background()

			empty, err := repo.List(ctx)
			require.NoError(t, err)
			assert.Empty(t, empty)

			require.NoError(t, repo.Create(ctx, admin.User{Username: "admin", PasswordHash: "h", Role: admin.RoleAdmin}))
			require.NoError(t, repo.Create(ctx, admin.User{Username: "bob", Role: admin.RoleUser, Request: true}))

			all, err := repo.List(ctx)
			require.NoError(t, err)
			require.Len(t, all, 2)
			// id ascending — admin inserted first.
			assert.Equal(t, "admin", all[0].Username)
			assert.Equal(t, "bob", all[1].Username)
			assert.True(t, all[0].ID < all[1].ID)
			// perms round-trip.
			assert.True(t, all[1].Request)
			assert.False(t, all[1].ManageUsers)
		})
	}
}

func TestAdminUserRepo_GetByID_FoundAndNotFound(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			repo := NewUserRepository(backend.NewDB(t))
			ctx := context.Background()

			require.NoError(t, repo.Create(ctx, admin.User{Username: "admin", PasswordHash: "h"}))
			row, err := repo.Get(ctx)
			require.NoError(t, err)

			got, err := repo.GetByID(ctx, row.ID)
			require.NoError(t, err)
			assert.Equal(t, "admin", got.Username)

			_, err = repo.GetByID(ctx, 999999)
			require.Error(t, err)
			var typed *sharedErrors.UserNotFoundError
			require.True(t, errors.As(err, &typed))
			require.True(t, errors.Is(err, ports.ErrNotFound))
		})
	}
}

func TestAdminUserRepo_CountAdmins(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			repo := NewUserRepository(backend.NewDB(t))
			ctx := context.Background()

			n, err := repo.CountAdmins(ctx)
			require.NoError(t, err)
			assert.Equal(t, int64(0), n)

			require.NoError(t, repo.Create(ctx, admin.User{Username: "a1", Role: admin.RoleAdmin}))
			require.NoError(t, repo.Create(ctx, admin.User{Username: "a2", Role: admin.RoleAdmin}))
			require.NoError(t, repo.Create(ctx, admin.User{Username: "u1", Role: admin.RoleUser}))

			n, err = repo.CountAdmins(ctx)
			require.NoError(t, err)
			assert.Equal(t, int64(2), n)
		})
	}
}

func TestAdminUserRepo_UpdateRole(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			repo := NewUserRepository(backend.NewDB(t))
			ctx := context.Background()

			require.NoError(t, repo.Create(ctx, admin.User{Username: "admin", Role: admin.RoleAdmin}))
			row, err := repo.Get(ctx)
			require.NoError(t, err)

			require.NoError(t, repo.UpdateRole(ctx, row.ID, admin.RoleUser))
			got, err := repo.GetByID(ctx, row.ID)
			require.NoError(t, err)
			assert.Equal(t, admin.RoleUser, got.Role)

			// NULL/error pair.
			err = repo.UpdateRole(ctx, 999999, admin.RoleUser)
			require.Error(t, err)
			require.True(t, errors.Is(err, ports.ErrNotFound))
		})
	}
}

func TestAdminUserRepo_UpdatePermissions_PartialAndNoop(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			repo := NewUserRepository(backend.NewDB(t))
			ctx := context.Background()

			require.NoError(t, repo.Create(ctx, admin.User{
				Username: "u1", Role: admin.RoleUser, Request: true,
			}))
			row, err := repo.Get(ctx)
			require.NoError(t, err)

			// Partial: only flip manage_requests + request_4k, leave request alone.
			require.NoError(t, repo.UpdatePermissions(ctx, row.ID, ports.UserPermissionsPatch{
				ManageRequests: new(true),
				Request4K:      new(true),
			}))
			got, err := repo.GetByID(ctx, row.ID)
			require.NoError(t, err)
			assert.True(t, got.ManageRequests)
			assert.True(t, got.Request4K)
			assert.True(t, got.Request, "untouched flag must be preserved")
			assert.False(t, got.ManageUsers)

			// Explicit false is written.
			require.NoError(t, repo.UpdatePermissions(ctx, row.ID, ports.UserPermissionsPatch{
				Request: new(false),
			}))
			got, err = repo.GetByID(ctx, row.ID)
			require.NoError(t, err)
			assert.False(t, got.Request)

			// No-op patch: nil, no error, no row required.
			require.NoError(t, repo.UpdatePermissions(ctx, row.ID, ports.UserPermissionsPatch{}))

			// NULL/error pair (non-empty patch on missing row).
			err = repo.UpdatePermissions(ctx, 999999, ports.UserPermissionsPatch{Request: new(true)})
			require.Error(t, err)
			require.True(t, errors.Is(err, ports.ErrNotFound))
		})
	}
}

func TestAdminUserRepo_Delete(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			repo := NewUserRepository(backend.NewDB(t))
			ctx := context.Background()

			require.NoError(t, repo.Create(ctx, admin.User{Username: "victim", Role: admin.RoleUser}))
			row, err := repo.Get(ctx)
			require.NoError(t, err)

			require.NoError(t, repo.Delete(ctx, row.ID))
			_, err = repo.GetByID(ctx, row.ID)
			require.True(t, errors.Is(err, ports.ErrNotFound))

			// NULL/error pair — deleting a gone row.
			err = repo.Delete(ctx, row.ID)
			require.Error(t, err)
			require.True(t, errors.Is(err, ports.ErrNotFound))
		})
	}
}

func TestAdminUserRepo_InTx_CommitAndRollback(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			repo := NewUserRepository(backend.NewDB(t))
			ctx := context.Background()

			require.NoError(t, repo.Create(ctx, admin.User{Username: "admin", Role: admin.RoleAdmin}))
			row, err := repo.Get(ctx)
			require.NoError(t, err)

			// Rollback: the fn error must undo the role write.
			sentinel := errors.New("boom")
			err = repo.InTx(ctx, func(ctx context.Context) error {
				if err := repo.UpdateRole(ctx, row.ID, admin.RoleUser); err != nil {
					return err
				}
				return sentinel
			})
			require.ErrorIs(t, err, sentinel)
			got, err := repo.GetByID(ctx, row.ID)
			require.NoError(t, err)
			assert.Equal(t, admin.RoleAdmin, got.Role, "rollback must preserve the original role")

			// Commit: no error → write persists.
			require.NoError(t, repo.InTx(ctx, func(ctx context.Context) error {
				return repo.UpdateRole(ctx, row.ID, admin.RoleUser)
			}))
			got, err = repo.GetByID(ctx, row.ID)
			require.NoError(t, err)
			assert.Equal(t, admin.RoleUser, got.Role)
		})
	}
}
