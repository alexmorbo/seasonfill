package persistence

import (
	"context"
	"errors"
	"fmt"
	"time"

	"gorm.io/gorm"

	admin "github.com/alexmorbo/seasonfill/internal/admin/domain"
	ports "github.com/alexmorbo/seasonfill/internal/shared/dataports"
	database "github.com/alexmorbo/seasonfill/internal/shared/db"
	sharedErrors "github.com/alexmorbo/seasonfill/internal/shared/errors"
)

// UserRepository is the GORM-backed CRUD surface for the `users` table.
// Greenfield D-5 rewrite of the legacy AdminUserRepository.
type UserRepository struct{ db *gorm.DB }

// NewUserRepository constructs a UserRepository bound to db.
func NewUserRepository(db *gorm.DB) *UserRepository {
	return &UserRepository{db: db}
}

// Get returns the first user row by id (single-user invariant kept from
// Phase 7; the table is multi-row capable for the future N-1 multi-user
// UI but D-5 reads only the first row).
func (r *UserRepository) Get(ctx context.Context) (admin.User, error) {
	var m database.UserModel
	err := dbFromContext(ctx, r.db).WithContext(ctx).
		Order("id ASC").First(&m).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return admin.User{}, errors.Join(
				&sharedErrors.UserNotFoundError{},
				ports.ErrNotFound,
			)
		}
		return admin.User{}, fmt.Errorf("get user: %w", err)
	}
	return modelToUser(m), nil
}

// FirstAdminID returns the lowest-id role='admin' user id (the seed admin —
// the same row mig-058 backfills user_id to). The api-key principal and other
// no-user-row callers resolve their owner id through this. ports.ErrNotFound
// when the users table has no admin (fresh DB before seed).
func (r *UserRepository) FirstAdminID(ctx context.Context) (int64, error) {
	var m database.UserModel
	err := dbFromContext(ctx, r.db).WithContext(ctx).
		Where("role = ?", "admin").Order("id ASC").First(&m).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return 0, errors.Join(&sharedErrors.UserNotFoundError{}, ports.ErrNotFound)
		}
		return 0, fmt.Errorf("first admin id: %w", err)
	}
	return int64(m.ID), nil
}

// GetByOIDCSubject looks up a user row by OIDC subject claim. Returns
// ports.ErrNotFound (joined with UserNotFoundError) when no row
// matches — the OIDC callback handler falls through to CreateFromOIDC.
func (r *UserRepository) GetByOIDCSubject(ctx context.Context, subject string) (admin.User, error) {
	var m database.UserModel
	err := dbFromContext(ctx, r.db).WithContext(ctx).
		Where("oidc_subject = ?", subject).First(&m).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return admin.User{}, errors.Join(
				&sharedErrors.UserNotFoundError{},
				ports.ErrNotFound,
			)
		}
		return admin.User{}, fmt.Errorf("get user by oidc subject: %w", err)
	}
	return modelToUser(m), nil
}

// GetByJellyfinUserID looks up a user row by immutable Jellyfin User.Id.
// Returns ports.ErrNotFound (joined with UserNotFoundError) when no row
// matches — the Jellyfin login usecase falls through to CreateFromJellyfin.
func (r *UserRepository) GetByJellyfinUserID(ctx context.Context, jellyfinUserID string) (admin.User, error) {
	var m database.UserModel
	err := dbFromContext(ctx, r.db).WithContext(ctx).
		Where("jellyfin_user_id = ?", jellyfinUserID).First(&m).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return admin.User{}, errors.Join(
				&sharedErrors.UserNotFoundError{},
				ports.ErrNotFound,
			)
		}
		return admin.User{}, fmt.Errorf("get user by jellyfin user id: %w", err)
	}
	return modelToUser(m), nil
}

// Create inserts a new user. Populates Role / AvatarMode defaults when
// the caller leaves them zero; CreatedAt / UpdatedAt are stamped if not
// pre-populated.
func (r *UserRepository) Create(ctx context.Context, u admin.User) error {
	now := time.Now().UTC()
	if u.CreatedAt.IsZero() {
		u.CreatedAt = now
	}
	if u.UpdatedAt.IsZero() {
		u.UpdatedAt = now
	}
	if u.Role == "" {
		u.Role = admin.RoleAdmin
	}
	if u.AvatarMode == "" {
		u.AvatarMode = admin.AvatarModeAuto
	}
	m := userToModel(u)
	if err := dbFromContext(ctx, r.db).WithContext(ctx).Create(&m).Error; err != nil {
		return fmt.Errorf("create user: %w", err)
	}
	return nil
}

// CreateFromOIDC inserts a new user row keyed on the OIDC subject claim.
// Password hash stays NULL (OIDC users never authenticate via the
// password path). Role / AvatarMode default to admin / auto.
func (r *UserRepository) CreateFromOIDC(ctx context.Context, subject, username, email string) (admin.User, error) {
	now := time.Now().UTC()
	sub := subject
	// F-08 (AUDIT2-S3): a preferred_username that collides with the reserved
	// X-Api-Key principal sentinel ("api-key") would create a row that
	// permanently 401s on /me — resolveUser treats "api-key" as the automation
	// principal (no stored row), not a real user. Fall back to the OIDC subject,
	// which is opaque, unique, and never collides with a sentinel. Post
	// auth-refactor only "api-key" remains reserved (local/anonymous bypass
	// concepts were removed).
	if username == "api-key" {
		username = subject
	}
	u := admin.User{
		Username:    username,
		OIDCSubject: &sub,
		Role:        admin.RoleAdmin,
		AvatarMode:  admin.AvatarModeAuto,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	if email != "" {
		e := email
		u.Email = &e
	}
	m := userToModel(u)
	if err := dbFromContext(ctx, r.db).WithContext(ctx).Create(&m).Error; err != nil {
		return admin.User{}, fmt.Errorf("create oidc user: %w", err)
	}
	return modelToUser(m), nil
}

// CreateFromJellyfin inserts a new user row keyed on the immutable Jellyfin
// User.Id. Password hash stays NULL (Jellyfin users authenticate against
// Jellyfin on every login, never against a local hash). Unlike CreateFromOIDC
// (admin), a first-seen Jellyfin user is a plain requester: role=user with
// ONLY the `request` permission (grill-Q4 defaults) — auto_approve /
// manage_requests / manage_users / request_4k all false. AvatarMode defaults
// to auto.
func (r *UserRepository) CreateFromJellyfin(ctx context.Context, jellyfinUserID, username, email string) (admin.User, error) {
	now := time.Now().UTC()
	jid := jellyfinUserID
	// Same reserved-sentinel guard as CreateFromOIDC: a Jellyfin display name
	// of "api-key" would collide with the automation principal and 401 on /me
	// forever. Fall back to the opaque, unique Jellyfin id.
	if username == "api-key" {
		username = jellyfinUserID
	}
	u := admin.User{
		Username:       username,
		JellyfinUserID: &jid,
		Role:           admin.RoleUser,
		AvatarMode:     admin.AvatarModeAuto,
		Request:        true,
		AutoApprove:    false,
		ManageRequests: false,
		ManageUsers:    false,
		Request4K:      false,
		CreatedAt:      now,
		UpdatedAt:      now,
	}
	if email != "" {
		e := email
		u.Email = &e
	}
	m := userToModel(u)
	if err := dbFromContext(ctx, r.db).WithContext(ctx).Create(&m).Error; err != nil {
		return admin.User{}, fmt.Errorf("create jellyfin user: %w", err)
	}
	return modelToUser(m), nil
}

// UpdatePassword replaces the password_hash for userID. Greenfield D-5
// drops the legacy auto_generated bool — the auto-gen state is no
// longer DB-persisted (see admin.User docs).
func (r *UserRepository) UpdatePassword(ctx context.Context, userID uint, hash string) error {
	db := dbFromContext(ctx, r.db).WithContext(ctx)
	res := db.Model(&database.UserModel{}).
		Where("id = ?", userID).
		Updates(map[string]any{
			"password_hash": hash,
			"updated_at":    time.Now().UTC(),
		})
	if res.Error != nil {
		return fmt.Errorf("update user password: %w", res.Error)
	}
	if res.RowsAffected == 0 {
		return errors.Join(
			&sharedErrors.UserNotFoundError{},
			ports.ErrNotFound,
		)
	}
	return nil
}

// UpdateLastLoginAt stamps the user's last_login_at column. Best-effort
// observability stamp — handlers ignore the error (the login itself
// already succeeded by the time this is invoked).
func (r *UserRepository) UpdateLastLoginAt(ctx context.Context, userID uint, when time.Time) error {
	res := dbFromContext(ctx, r.db).WithContext(ctx).
		Model(&database.UserModel{}).
		Where("id = ?", userID).
		Update("last_login_at", when.UTC())
	if res.Error != nil {
		return fmt.Errorf("update last_login_at: %w", res.Error)
	}
	if res.RowsAffected == 0 {
		return errors.Join(
			&sharedErrors.UserNotFoundError{},
			ports.ErrNotFound,
		)
	}
	return nil
}

// GetByUsername returns the user row whose `username` column matches name.
// Returns errors.Join(UserNotFoundError, ports.ErrNotFound) when no row
// matches — distinguishable via errors.As / errors.Is.
//
// Story 485 (N-7a). The MeHandler reads
// middleware.UsernameContextKey off the gin context and looks up the
// row via this method so that even under a future multi-user wire
// (N-1) the response always reflects the cookie-authenticated user
// rather than the legacy "first row" invariant on Get().
func (r *UserRepository) GetByUsername(ctx context.Context, name string) (admin.User, error) {
	var m database.UserModel
	err := dbFromContext(ctx, r.db).WithContext(ctx).
		Where("username = ?", name).First(&m).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return admin.User{}, errors.Join(
				&sharedErrors.UserNotFoundError{},
				ports.ErrNotFound,
			)
		}
		return admin.User{}, fmt.Errorf("get user by username: %w", err)
	}
	return modelToUser(m), nil
}

// UpdateSettings applies a partial user-scope settings patch. Only the
// columns whose corresponding pointer in `patch` is non-nil are written;
// updated_at is bumped on every call.
//
// Returns errors.Join(UserNotFoundError, ports.ErrNotFound) when userID
// matches no row. Returns nil on a "no fields to patch" call (the handler
// already short-circuits empty bodies but the repo stays tolerant).
func (r *UserRepository) UpdateSettings(ctx context.Context, userID uint, patch ports.UserSettingsPatch) error {
	updates := map[string]any{"updated_at": time.Now().UTC()}
	if patch.AvatarMode != nil {
		updates["avatar_mode"] = *patch.AvatarMode
	}
	if patch.PreferredLanguage != nil {
		updates["preferred_language"] = *patch.PreferredLanguage
	}
	if len(updates) == 1 {
		return nil
	}
	res := dbFromContext(ctx, r.db).WithContext(ctx).
		Model(&database.UserModel{}).
		Where("id = ?", userID).
		Updates(updates)
	if res.Error != nil {
		return fmt.Errorf("update user settings: %w", res.Error)
	}
	if res.RowsAffected == 0 {
		return errors.Join(
			&sharedErrors.UserNotFoundError{},
			ports.ErrNotFound,
		)
	}
	return nil
}

func userToModel(u admin.User) database.UserModel {
	m := database.UserModel{
		ID:                u.ID,
		Username:          u.Username,
		Email:             u.Email,
		OIDCSubject:       u.OIDCSubject,
		JellyfinUserID:    u.JellyfinUserID,
		Role:              u.Role,
		AvatarMode:        u.AvatarMode,
		PreferredLanguage: u.PreferredLanguage,
		CreatedAt:         u.CreatedAt,
		UpdatedAt:         u.UpdatedAt,
		LastLoginAt:       u.LastLoginAt,
		AutoApprove:       u.AutoApprove,
		Request:           u.Request,
		ManageRequests:    u.ManageRequests,
		ManageUsers:       u.ManageUsers,
		Request4K:         u.Request4K,
	}
	if u.PasswordHash != "" {
		h := u.PasswordHash
		m.PasswordHash = &h
	}
	return m
}

func modelToUser(m database.UserModel) admin.User {
	u := admin.User{
		ID:                m.ID,
		Username:          m.Username,
		Email:             m.Email,
		OIDCSubject:       m.OIDCSubject,
		JellyfinUserID:    m.JellyfinUserID,
		Role:              m.Role,
		AvatarMode:        m.AvatarMode,
		PreferredLanguage: m.PreferredLanguage,
		CreatedAt:         m.CreatedAt,
		UpdatedAt:         m.UpdatedAt,
		LastLoginAt:       m.LastLoginAt,
		AutoApprove:       m.AutoApprove,
		Request:           m.Request,
		ManageRequests:    m.ManageRequests,
		ManageUsers:       m.ManageUsers,
		Request4K:         m.Request4K,
	}
	if m.PasswordHash != nil {
		u.PasswordHash = *m.PasswordHash
	}
	return u
}

var _ ports.UserRepository = (*UserRepository)(nil)
