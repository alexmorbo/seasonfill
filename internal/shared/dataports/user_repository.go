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
	GetByOIDCSubject(ctx context.Context, subject string) (admin.User, error)
	Create(ctx context.Context, u admin.User) error
	CreateFromOIDC(ctx context.Context, subject, username, email string) (admin.User, error)
	UpdatePassword(ctx context.Context, userID uint, hash string) error
	UpdateLastLoginAt(ctx context.Context, userID uint, when time.Time) error
}
