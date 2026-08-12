package auth

import (
	"context"
	"errors"
	"fmt"

	admin "github.com/alexmorbo/seasonfill/internal/admin/domain"
	ports "github.com/alexmorbo/seasonfill/internal/shared/dataports"
)

// Ф8-U-6b admin user-management sentinels. Handlers translate each to the
// documented HTTP envelope:
//
//	ErrUserNotFound  → 404
//	ErrInvalidRole   → 400 INVALID_ROLE
//	ErrLastAdmin     → 409 LAST_ADMIN
//	ErrSelfLockout   → 409 SELF_LOCKOUT
var (
	// ErrUserNotFound — target id matches no user row.
	ErrUserNotFound = errors.New("users: not found")
	// ErrInvalidRole — role patch value is neither "admin" nor "user".
	ErrInvalidRole = errors.New("users: role must be admin or user")
	// ErrLastAdmin — demoting or deleting the target would leave zero
	// role='admin' rows. Enforced inside the mutate transaction against a
	// fresh CountAdmins so the invariant holds even under concurrent edits.
	ErrLastAdmin = errors.New("users: cannot remove the last administrator")
	// ErrSelfLockout — the caller tried to demote their OWN admin role or
	// delete themselves. The api-key automation principal (caller.ID == 0)
	// has no self row and is exempt.
	ErrSelfLockout = errors.New("users: cannot lock yourself out")
)

// UsersRepository is the narrow persistence surface the UsersUseCase needs.
// *admin/persistence.UserRepository satisfies it structurally. Kept off the
// shared ports.UserRepository (mirrors the UsernamesByIDs decision) so the
// admin-only write surface never forces a stub onto the port's many test
// fakes.
type UsersRepository interface {
	List(ctx context.Context) ([]admin.User, error)
	GetByID(ctx context.Context, id uint) (admin.User, error)
	CountAdmins(ctx context.Context) (int64, error)
	UpdateRole(ctx context.Context, id uint, role string) error
	UpdatePermissions(ctx context.Context, id uint, patch ports.UserPermissionsPatch) error
	Delete(ctx context.Context, id uint) error
	InTx(ctx context.Context, fn func(ctx context.Context) error) error
}

// UsersPatch is the parsed PATCH /api/v1/admin/users/:id body. Every field is
// a pointer: nil means "omitted, do not write". Role is validated against
// {admin,user}; the five permission pointers map straight onto the RBAC bool
// columns.
type UsersPatch struct {
	Role           *string
	AutoApprove    *bool
	Request        *bool
	ManageRequests *bool
	ManageUsers    *bool
	Request4K      *bool
}

func (p UsersPatch) hasPermissions() bool {
	return p.AutoApprove != nil || p.Request != nil || p.ManageRequests != nil ||
		p.ManageUsers != nil || p.Request4K != nil
}

func (p UsersPatch) permissions() ports.UserPermissionsPatch {
	return ports.UserPermissionsPatch{
		AutoApprove:    p.AutoApprove,
		Request:        p.Request,
		ManageRequests: p.ManageRequests,
		ManageUsers:    p.ManageUsers,
		Request4K:      p.Request4K,
	}
}

// UsersUseCase serves the admin user-management routes (Ф8-U-6b): list users,
// patch role/permissions, delete users. It enforces the two hard invariants —
// at least one admin always remains, and a caller can never lock themselves
// out — regardless of which handler path invokes it.
type UsersUseCase struct {
	repo UsersRepository
}

// NewUsersUseCase panics on nil repo (init-time wiring bug).
func NewUsersUseCase(repo UsersRepository) *UsersUseCase {
	if repo == nil {
		panic("auth.NewUsersUseCase: repo must not be nil")
	}
	return &UsersUseCase{repo: repo}
}

// List returns every user row (id ascending).
func (uc *UsersUseCase) List(ctx context.Context) ([]admin.User, error) {
	users, err := uc.repo.List(ctx)
	if err != nil {
		return nil, fmt.Errorf("users usecase: list: %w", err)
	}
	return users, nil
}

// Patch applies a role/permission patch to targetID on behalf of caller and
// returns the refreshed row. Guards:
//   - ErrInvalidRole when the role value is not admin|user.
//   - ErrUserNotFound when targetID matches no row.
//   - ErrSelfLockout when caller demotes their own admin role.
//   - ErrLastAdmin when the demotion would leave zero admins.
//
// Stripping manage_users from an admin is intentionally NOT guarded: role
// short-circuits RBAC, so the last-admin invariant binds to role demotion and
// deletion only.
func (uc *UsersUseCase) Patch(ctx context.Context, caller admin.User, targetID uint, patch UsersPatch) (admin.User, error) {
	var newRole *string
	if patch.Role != nil {
		role := *patch.Role
		if role != admin.RoleAdmin && role != admin.RoleUser {
			return admin.User{}, ErrInvalidRole
		}
		newRole = &role
	}

	target, err := uc.repo.GetByID(ctx, targetID)
	if err != nil {
		if errors.Is(err, ports.ErrNotFound) {
			return admin.User{}, ErrUserNotFound
		}
		return admin.User{}, fmt.Errorf("users usecase: load target: %w", err)
	}

	demoting := newRole != nil && target.Role == admin.RoleAdmin && *newRole != admin.RoleAdmin
	if demoting && isSelf(caller, target) {
		return admin.User{}, ErrSelfLockout
	}

	hasPerm := patch.hasPermissions()
	if newRole == nil && !hasPerm {
		// No-op patch: nothing to write, return the current row unchanged.
		return target, nil
	}

	err = uc.repo.InTx(ctx, func(ctx context.Context) error {
		if demoting {
			n, err := uc.repo.CountAdmins(ctx)
			if err != nil {
				return fmt.Errorf("count admins: %w", err)
			}
			if n <= 1 {
				return ErrLastAdmin
			}
		}
		if newRole != nil {
			if err := uc.repo.UpdateRole(ctx, targetID, *newRole); err != nil {
				return fmt.Errorf("update role: %w", err)
			}
		}
		if hasPerm {
			if err := uc.repo.UpdatePermissions(ctx, targetID, patch.permissions()); err != nil {
				return fmt.Errorf("update permissions: %w", err)
			}
		}
		return nil
	})
	if err != nil {
		if errors.Is(err, ErrLastAdmin) {
			return admin.User{}, ErrLastAdmin
		}
		return admin.User{}, fmt.Errorf("users usecase: patch: %w", err)
	}

	updated, err := uc.repo.GetByID(ctx, targetID)
	if err != nil {
		return admin.User{}, fmt.Errorf("users usecase: reload target: %w", err)
	}
	return updated, nil
}

// Delete removes targetID on behalf of caller. Guards: ErrUserNotFound,
// ErrSelfLockout (deleting self), ErrLastAdmin (deleting the last admin).
func (uc *UsersUseCase) Delete(ctx context.Context, caller admin.User, targetID uint) error {
	target, err := uc.repo.GetByID(ctx, targetID)
	if err != nil {
		if errors.Is(err, ports.ErrNotFound) {
			return ErrUserNotFound
		}
		return fmt.Errorf("users usecase: load target: %w", err)
	}
	if isSelf(caller, target) {
		return ErrSelfLockout
	}

	err = uc.repo.InTx(ctx, func(ctx context.Context) error {
		if target.Role == admin.RoleAdmin {
			n, err := uc.repo.CountAdmins(ctx)
			if err != nil {
				return fmt.Errorf("count admins: %w", err)
			}
			if n <= 1 {
				return ErrLastAdmin
			}
		}
		if err := uc.repo.Delete(ctx, targetID); err != nil {
			return fmt.Errorf("delete: %w", err)
		}
		return nil
	})
	if err != nil {
		if errors.Is(err, ErrLastAdmin) {
			return ErrLastAdmin
		}
		return fmt.Errorf("users usecase: delete: %w", err)
	}
	return nil
}

// isSelf reports whether caller is the same stored row as target. The api-key
// automation principal is synthesized with ID==0 (no stored row) and is thus
// never "self" — it is exempt from self-lockout (the last-admin guard still
// applies to it).
func isSelf(caller, target admin.User) bool {
	return caller.ID != 0 && caller.ID == target.ID
}
