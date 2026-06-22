package auth

import (
	"context"
	"errors"
	"fmt"

	admin "github.com/alexmorbo/seasonfill/internal/admin/domain"
	ports "github.com/alexmorbo/seasonfill/internal/shared/dataports"
)

// MeUseCase exposes per-user reads + the change-password +
// settings-patch writes that back /api/v1/me. Story 485 (N-7a).
type MeUseCase struct {
	repo ports.UserRepository
}

// NewMeUseCase panics on nil repo (matches the constructor style of the
// other auth-domain use cases — failure to wire is an init-time bug).
func NewMeUseCase(repo ports.UserRepository) *MeUseCase {
	if repo == nil {
		panic("auth.NewMeUseCase: repo must not be nil")
	}
	return &MeUseCase{repo: repo}
}

// ErrInvalidCurrentPassword is returned by ChangePassword when the
// supplied current password does not match the stored hash. Callers
// translate this to HTTP 401 with the canonical envelope.
var ErrInvalidCurrentPassword = errors.New("invalid current password")

// ErrNewPasswordTooShort is returned when the new password is shorter
// than MinPasswordLen. Handlers translate to HTTP 400.
var ErrNewPasswordTooShort = errors.New("new password too short")

// ErrNewPasswordSameAsCurrent guards against a no-op rehash. Handlers
// translate to HTTP 400.
var ErrNewPasswordSameAsCurrent = errors.New("new password must differ from current")

// GetByUsername loads the user row whose username column matches name.
// Surfaces ports.ErrNotFound on miss so the handler can map to a 401.
func (uc *MeUseCase) GetByUsername(ctx context.Context, name string) (admin.User, error) {
	user, err := uc.repo.GetByUsername(ctx, name)
	if err != nil {
		return admin.User{}, fmt.Errorf("me usecase: get user: %w", err)
	}
	return user, nil
}

// UpdateSettings applies the partial settings patch on behalf of userID.
// Validation is the handler's job; the use case only forwards.
func (uc *MeUseCase) UpdateSettings(ctx context.Context, userID uint, patch ports.UserSettingsPatch) error {
	if err := uc.repo.UpdateSettings(ctx, userID, patch); err != nil {
		return fmt.Errorf("me usecase: update settings: %w", err)
	}
	return nil
}

// ChangePassword verifies current against user.PasswordHash and
// rehashes new. The handler is responsible for gating on auth_mode ==
// forms before reaching this method, and for loading user via
// GetByUsername first.
//
// Errors: ErrInvalidCurrentPassword, ErrNewPasswordTooShort,
// ErrNewPasswordSameAsCurrent, or a wrapped repo error.
func (uc *MeUseCase) ChangePassword(ctx context.Context, user admin.User, current, newPwd string) error {
	if len(newPwd) < MinPasswordLen {
		return ErrNewPasswordTooShort
	}
	if !VerifyPassword(user.PasswordHash, current) {
		return ErrInvalidCurrentPassword
	}
	if newPwd == current {
		return ErrNewPasswordSameAsCurrent
	}
	hash, err := HashPassword(newPwd)
	if err != nil {
		return fmt.Errorf("me usecase: hash password: %w", err)
	}
	if err := uc.repo.UpdatePassword(ctx, user.ID, hash); err != nil {
		return fmt.Errorf("me usecase: persist password: %w", err)
	}
	return nil
}
