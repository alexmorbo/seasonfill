package dataports

import (
	"context"
	"time"

	admin "github.com/alexmorbo/seasonfill/internal/admin/domain"
)

// UserRepository persists the `users` table. Methods return
// ports.ErrNotFound joined with a typed sharedErrors.UserNotFoundError
// when no row matches. Greenfield rename of AdminUserRepository.
type UserRepository interface {
	Get(ctx context.Context) (admin.User, error)
	GetByUsername(ctx context.Context, username string) (admin.User, error)
	GetByOIDCSubject(ctx context.Context, subject string) (admin.User, error)
	Create(ctx context.Context, u admin.User) error
	CreateFromOIDC(ctx context.Context, subject, username, email string) (admin.User, error)
	UpdatePassword(ctx context.Context, userID uint, hash string) error
	UpdateSettings(ctx context.Context, userID uint, settings UserSettingsPatch) error
	UpdateLastLoginAt(ctx context.Context, userID uint, when time.Time) error
}

// UserSettingsPatch carries the optional updatable user-scope fields for
// PATCH /api/v1/me/settings (story 485, N-7a). Each pointer is nil when
// the caller did not include the key in the request body; the
// repository only writes the columns whose pointers are non-nil.
//
// AvatarMode is validated by the handler against the canonical
// allowlist {auto, monogram, gravatar} before reaching this struct.
// PreferredLanguage is validated against the BCP-47 regex
// `^[a-z]{2}(-[A-Z]{2})?$` (also handler-side).
type UserSettingsPatch struct {
	AvatarMode        *string
	PreferredLanguage *string
}
