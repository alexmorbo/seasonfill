package auth

import (
	"context"
	"errors"
	"sort"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	admin "github.com/alexmorbo/seasonfill/internal/admin/domain"
	ports "github.com/alexmorbo/seasonfill/internal/shared/dataports"
)

// fakeUsersRepo is an in-memory UsersRepository for the guard-branch tests.
type fakeUsersRepo struct {
	users map[uint]admin.User
}

func newFakeUsersRepo(seed ...admin.User) *fakeUsersRepo {
	m := make(map[uint]admin.User, len(seed))
	for _, u := range seed {
		m[u.ID] = u
	}
	return &fakeUsersRepo{users: m}
}

func (f *fakeUsersRepo) List(_ context.Context) ([]admin.User, error) {
	out := make([]admin.User, 0, len(f.users))
	for _, u := range f.users {
		out = append(out, u)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

func (f *fakeUsersRepo) GetByID(_ context.Context, id uint) (admin.User, error) {
	u, ok := f.users[id]
	if !ok {
		return admin.User{}, ports.ErrNotFound
	}
	return u, nil
}

func (f *fakeUsersRepo) CountAdmins(_ context.Context) (int64, error) {
	var n int64
	for _, u := range f.users {
		if u.Role == admin.RoleAdmin {
			n++
		}
	}
	return n, nil
}

func (f *fakeUsersRepo) UpdateRole(_ context.Context, id uint, role string) error {
	u, ok := f.users[id]
	if !ok {
		return ports.ErrNotFound
	}
	u.Role = role
	f.users[id] = u
	return nil
}

func (f *fakeUsersRepo) UpdatePermissions(_ context.Context, id uint, patch ports.UserPermissionsPatch) error {
	u, ok := f.users[id]
	if !ok {
		return ports.ErrNotFound
	}
	if patch.AutoApprove != nil {
		u.AutoApprove = *patch.AutoApprove
	}
	if patch.Request != nil {
		u.Request = *patch.Request
	}
	if patch.ManageRequests != nil {
		u.ManageRequests = *patch.ManageRequests
	}
	if patch.ManageUsers != nil {
		u.ManageUsers = *patch.ManageUsers
	}
	if patch.Request4K != nil {
		u.Request4K = *patch.Request4K
	}
	f.users[id] = u
	return nil
}

func (f *fakeUsersRepo) Delete(_ context.Context, id uint) error {
	if _, ok := f.users[id]; !ok {
		return ports.ErrNotFound
	}
	delete(f.users, id)
	return nil
}

func (f *fakeUsersRepo) InTx(ctx context.Context, fn func(ctx context.Context) error) error {
	return fn(ctx)
}

func adminUser(id uint) admin.User { return admin.User{ID: id, Role: admin.RoleAdmin} }
func plainUser(id uint) admin.User { return admin.User{ID: id, Role: admin.RoleUser, Request: true} }

func TestUsersUseCase_Patch_InvalidRole(t *testing.T) {
	t.Parallel()
	uc := NewUsersUseCase(newFakeUsersRepo(adminUser(1), plainUser(2)))
	_, err := uc.Patch(context.Background(), adminUser(1), 2, UsersPatch{Role: new("superuser")})
	require.ErrorIs(t, err, ErrInvalidRole)
}

func TestUsersUseCase_Patch_TargetNotFound(t *testing.T) {
	t.Parallel()
	uc := NewUsersUseCase(newFakeUsersRepo(adminUser(1)))
	_, err := uc.Patch(context.Background(), adminUser(1), 42, UsersPatch{Role: new(admin.RoleUser)})
	require.ErrorIs(t, err, ErrUserNotFound)
}

func TestUsersUseCase_Patch_SelfDemoteRejected(t *testing.T) {
	t.Parallel()
	// Two admins so it is NOT the last-admin case — the rejection must come
	// from the self-lockout guard, not last-admin.
	repo := newFakeUsersRepo(adminUser(1), adminUser(2))
	uc := NewUsersUseCase(repo)
	_, err := uc.Patch(context.Background(), adminUser(1), 1, UsersPatch{Role: new(admin.RoleUser)})
	require.ErrorIs(t, err, ErrSelfLockout)
	// Unchanged.
	got, _ := repo.GetByID(context.Background(), 1)
	assert.Equal(t, admin.RoleAdmin, got.Role)
}

func TestUsersUseCase_Patch_LastAdminDemoteRejected(t *testing.T) {
	t.Parallel()
	// api-key caller (ID 0) demoting the SOLE admin — self-lockout is skipped
	// (no self row), last-admin must still fire.
	repo := newFakeUsersRepo(adminUser(5))
	uc := NewUsersUseCase(repo)
	apiKey := admin.User{Role: admin.RoleAdmin} // ID 0
	_, err := uc.Patch(context.Background(), apiKey, 5, UsersPatch{Role: new(admin.RoleUser)})
	require.ErrorIs(t, err, ErrLastAdmin)
	got, _ := repo.GetByID(context.Background(), 5)
	assert.Equal(t, admin.RoleAdmin, got.Role, "role must be untouched after last-admin reject")
}

func TestUsersUseCase_Patch_DemoteOtherAdminOK(t *testing.T) {
	t.Parallel()
	repo := newFakeUsersRepo(adminUser(1), adminUser(2))
	uc := NewUsersUseCase(repo)
	updated, err := uc.Patch(context.Background(), adminUser(1), 2, UsersPatch{Role: new(admin.RoleUser)})
	require.NoError(t, err)
	assert.Equal(t, admin.RoleUser, updated.Role)
}

func TestUsersUseCase_Patch_ApiKeyDemoteWithSpareAdminOK(t *testing.T) {
	t.Parallel()
	repo := newFakeUsersRepo(adminUser(1), adminUser(2))
	uc := NewUsersUseCase(repo)
	apiKey := admin.User{Role: admin.RoleAdmin} // ID 0, exempt from self-lockout
	updated, err := uc.Patch(context.Background(), apiKey, 1, UsersPatch{Role: new(admin.RoleUser)})
	require.NoError(t, err)
	assert.Equal(t, admin.RoleUser, updated.Role)
}

func TestUsersUseCase_Patch_PermissionsOnly(t *testing.T) {
	t.Parallel()
	repo := newFakeUsersRepo(adminUser(1), plainUser(2))
	uc := NewUsersUseCase(repo)
	tru := true
	updated, err := uc.Patch(context.Background(), adminUser(1), 2, UsersPatch{
		ManageRequests: &tru,
		Request4K:      &tru,
	})
	require.NoError(t, err)
	assert.True(t, updated.ManageRequests)
	assert.True(t, updated.Request4K)
	assert.Equal(t, admin.RoleUser, updated.Role, "role untouched")
}

func TestUsersUseCase_Patch_StripManageUsersFromAdminAllowed(t *testing.T) {
	t.Parallel()
	// Stripping manage_users from the sole admin is NOT last-admin-guarded
	// (role short-circuits RBAC) — must succeed.
	repo := newFakeUsersRepo(admin.User{ID: 1, Role: admin.RoleAdmin, ManageUsers: true})
	uc := NewUsersUseCase(repo)
	fls := false
	updated, err := uc.Patch(context.Background(), adminUser(9), 1, UsersPatch{ManageUsers: &fls})
	require.NoError(t, err)
	assert.False(t, updated.ManageUsers)
	assert.Equal(t, admin.RoleAdmin, updated.Role)
}

func TestUsersUseCase_Patch_NoopReturnsUnchanged(t *testing.T) {
	t.Parallel()
	repo := newFakeUsersRepo(adminUser(1), plainUser(2))
	uc := NewUsersUseCase(repo)
	got, err := uc.Patch(context.Background(), adminUser(1), 2, UsersPatch{})
	require.NoError(t, err)
	assert.Equal(t, uint(2), got.ID)
	assert.Equal(t, admin.RoleUser, got.Role)
}

func TestUsersUseCase_Delete_SelfRejected(t *testing.T) {
	t.Parallel()
	repo := newFakeUsersRepo(adminUser(1), adminUser(2))
	uc := NewUsersUseCase(repo)
	err := uc.Delete(context.Background(), adminUser(1), 1)
	require.ErrorIs(t, err, ErrSelfLockout)
	_, gErr := repo.GetByID(context.Background(), 1)
	require.NoError(t, gErr, "self must not be deleted")
}

func TestUsersUseCase_Delete_LastAdminRejected(t *testing.T) {
	t.Parallel()
	repo := newFakeUsersRepo(adminUser(5))
	uc := NewUsersUseCase(repo)
	apiKey := admin.User{Role: admin.RoleAdmin} // ID 0
	err := uc.Delete(context.Background(), apiKey, 5)
	require.ErrorIs(t, err, ErrLastAdmin)
	_, gErr := repo.GetByID(context.Background(), 5)
	require.NoError(t, gErr, "sole admin must survive")
}

func TestUsersUseCase_Delete_NotFound(t *testing.T) {
	t.Parallel()
	uc := NewUsersUseCase(newFakeUsersRepo(adminUser(1)))
	err := uc.Delete(context.Background(), adminUser(1), 77)
	require.ErrorIs(t, err, ErrUserNotFound)
}

func TestUsersUseCase_Delete_PlainUserOK(t *testing.T) {
	t.Parallel()
	repo := newFakeUsersRepo(adminUser(1), plainUser(2))
	uc := NewUsersUseCase(repo)
	require.NoError(t, uc.Delete(context.Background(), adminUser(1), 2))
	_, err := repo.GetByID(context.Background(), 2)
	require.ErrorIs(t, err, ports.ErrNotFound)
}

func TestUsersUseCase_Delete_OtherAdminOK(t *testing.T) {
	t.Parallel()
	repo := newFakeUsersRepo(adminUser(1), adminUser(2))
	uc := NewUsersUseCase(repo)
	require.NoError(t, uc.Delete(context.Background(), adminUser(1), 2))
}

func TestUsersUseCase_List(t *testing.T) {
	t.Parallel()
	uc := NewUsersUseCase(newFakeUsersRepo(adminUser(2), plainUser(1)))
	users, err := uc.List(context.Background())
	require.NoError(t, err)
	require.Len(t, users, 2)
	assert.Equal(t, uint(1), users[0].ID, "sorted ascending")
}

func TestNewUsersUseCase_NilPanics(t *testing.T) {
	t.Parallel()
	assert.Panics(t, func() { NewUsersUseCase(nil) })
}

// guard against accidental sentinel aliasing.
func TestUsersSentinels_Distinct(t *testing.T) {
	t.Parallel()
	all := []error{ErrUserNotFound, ErrInvalidRole, ErrLastAdmin, ErrSelfLockout}
	for i := range all {
		for j := range all {
			if i != j {
				require.False(t, errors.Is(all[i], all[j]))
			}
		}
	}
}
