package auth

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	admin "github.com/alexmorbo/seasonfill/internal/admin/domain"
	jellyfin "github.com/alexmorbo/seasonfill/internal/shared/clients/jellyfin"
	ports "github.com/alexmorbo/seasonfill/internal/shared/dataports"
	sharedErrors "github.com/alexmorbo/seasonfill/internal/shared/errors"
	sharedports "github.com/alexmorbo/seasonfill/internal/shared/ports"
)

// ErrJellyfinLoginFailed is the usecase-level sentinel the handler maps to a
// 401. Wraps the client's ErrJellyfinAuthFailed at the boundary so callers
// pattern-match one type.
var ErrJellyfinLoginFailed = errors.New("jellyfin: login failed")

// JellyfinAuthenticator is the slice of the jellyfin.Client the usecase
// depends on. *jellyfin.Client satisfies it; tests inject a fake. The base
// URL lives in the concrete client (built per request by the handler from the
// live AuthRuntime), keeping the usecase stateless w.r.t. the URL — the same
// shape as OIDCLoginUseCase taking the per-request OIDCConfig.
type JellyfinAuthenticator interface {
	AuthenticateByName(ctx context.Context, username, password string) (jellyfin.JellyfinUser, error)
}

// JellyfinLoginUseCase validates a username+password against a Jellyfin
// server and maps the identity to a seasonfill user row, lazily creating one
// on first login. It never stores the password or the Jellyfin AccessToken.
type JellyfinLoginUseCase struct {
	users  ports.UserRepository
	logger *slog.Logger
}

func NewJellyfinLoginUseCase(users ports.UserRepository) *JellyfinLoginUseCase {
	return &JellyfinLoginUseCase{
		users:  users,
		logger: sharedports.DomainLogger(slog.Default(), "auth"),
	}
}

// WithLogger swaps the audit logger (fluent, mirrors OIDCLoginUseCase).
func (u *JellyfinLoginUseCase) WithLogger(l *slog.Logger) *JellyfinLoginUseCase {
	if l != nil {
		u.logger = l
	}
	return u
}

// Login authenticates (username,password) against authr, then resolves the
// seasonfill row by Jellyfin id — lazily creating a role=user requester on
// first login. Returns the row for the handler to mint a session around.
// The password is validated on EVERY call and is NEVER passed to the repo.
func (u *JellyfinLoginUseCase) Login(ctx context.Context, authr JellyfinAuthenticator, username, password string) (admin.User, error) {
	jfUser, err := authr.AuthenticateByName(ctx, username, password)
	if err != nil {
		if errors.Is(err, jellyfin.ErrJellyfinAuthFailed) {
			return admin.User{}, ErrJellyfinLoginFailed
		}
		return admin.User{}, fmt.Errorf("jellyfin: authenticate: %w", err)
	}

	row, err := u.users.GetByJellyfinUserID(ctx, jfUser.ID)
	if err != nil {
		var userNF *sharedErrors.UserNotFoundError
		if !errors.As(err, &userNF) {
			return admin.User{}, fmt.Errorf("jellyfin: lookup user: %w", err)
		}
		u.logger.InfoContext(ctx, "jellyfin.login.user_first_seen",
			slog.String("code", "user_not_found"),
			slog.String("jellyfin_user_id", jfUser.ID),
			slog.String("username", jfUser.Name))
		row, err = u.users.CreateFromJellyfin(ctx, jfUser.ID, jfUser.Name, "")
		if err != nil {
			return admin.User{}, fmt.Errorf("jellyfin: create user row: %w", err)
		}
	}

	// Best-effort last_login_at observability stamp — the login has already
	// succeeded; a slow primary must not fail it.
	if err := u.users.UpdateLastLoginAt(ctx, row.ID, time.Now().UTC()); err != nil {
		u.logger.InfoContext(ctx, "jellyfin.login.last_login_stamp_failed",
			slog.String("username", row.Username),
			slog.String("error", err.Error()))
	}
	return row, nil
}
