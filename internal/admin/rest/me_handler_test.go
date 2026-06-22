package rest

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	authapp "github.com/alexmorbo/seasonfill/internal/admin/app"
	admin "github.com/alexmorbo/seasonfill/internal/admin/domain"
	"github.com/alexmorbo/seasonfill/internal/runtime"
	ports "github.com/alexmorbo/seasonfill/internal/shared/dataports"
	"github.com/alexmorbo/seasonfill/internal/shared/http/dto"
	"github.com/alexmorbo/seasonfill/internal/shared/http/middleware"
)

// fakeMeRepo mirrors fakeAdminRepo in auth_test.go but supports the
// per-username lookup + settings patch the MeHandler needs.
type fakeMeRepo struct {
	mu        sync.Mutex
	byName    map[string]*admin.User
	idCounter uint
}

func newFakeMeRepo() *fakeMeRepo { return &fakeMeRepo{byName: map[string]*admin.User{}} }

func (r *fakeMeRepo) seed(u admin.User) admin.User {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.idCounter++
	u.ID = r.idCounter
	if u.Role == "" {
		u.Role = admin.RoleAdmin
	}
	if u.AvatarMode == "" {
		u.AvatarMode = admin.AvatarModeAuto
	}
	if u.CreatedAt.IsZero() {
		u.CreatedAt = time.Now().UTC()
	}
	if u.UpdatedAt.IsZero() {
		u.UpdatedAt = u.CreatedAt
	}
	cp := u
	r.byName[u.Username] = &cp
	return cp
}

func (r *fakeMeRepo) Get(_ context.Context) (admin.User, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, u := range r.byName {
		return *u, nil
	}
	return admin.User{}, ports.ErrNotFound
}

func (r *fakeMeRepo) GetByUsername(_ context.Context, name string) (admin.User, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if u, ok := r.byName[name]; ok {
		return *u, nil
	}
	return admin.User{}, ports.ErrNotFound
}

func (r *fakeMeRepo) GetByOIDCSubject(_ context.Context, _ string) (admin.User, error) {
	return admin.User{}, ports.ErrNotFound
}

func (r *fakeMeRepo) Create(_ context.Context, u admin.User) error {
	r.seed(u)
	return nil
}

func (r *fakeMeRepo) CreateFromOIDC(_ context.Context, subject, username, email string) (admin.User, error) {
	sub := subject
	u := admin.User{Username: username, OIDCSubject: &sub}
	if email != "" {
		e := email
		u.Email = &e
	}
	return r.seed(u), nil
}

func (r *fakeMeRepo) UpdatePassword(_ context.Context, userID uint, hash string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, u := range r.byName {
		if u.ID == userID {
			u.PasswordHash = hash
			return nil
		}
	}
	return ports.ErrNotFound
}

func (r *fakeMeRepo) UpdateSettings(_ context.Context, userID uint, patch ports.UserSettingsPatch) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, u := range r.byName {
		if u.ID == userID {
			if patch.AvatarMode != nil {
				u.AvatarMode = *patch.AvatarMode
			}
			if patch.PreferredLanguage != nil {
				v := *patch.PreferredLanguage
				u.PreferredLanguage = &v
			}
			u.UpdatedAt = time.Now().UTC()
			return nil
		}
	}
	return ports.ErrNotFound
}

func (r *fakeMeRepo) UpdateLastLoginAt(_ context.Context, _ uint, _ time.Time) error {
	return nil
}

func setupMe(t *testing.T, repo ports.UserRepository, rt *middleware.AuthRuntime, username string) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	uc := authapp.NewMeUseCase(repo)
	ptr := &middleware.AuthRuntimePointer{}
	if rt != nil {
		ptr.Store(rt)
	}
	h := NewMeHandler(uc, ptr, slog.Default())
	r := gin.New()
	r.Use(func(c *gin.Context) {
		if username != "" {
			c.Set(middleware.UsernameContextKey, username)
		}
		c.Next()
	})
	r.GET("/api/v1/me", h.Get)
	r.PATCH("/api/v1/me/settings", h.UpdateSettings)
	r.POST("/api/v1/me/change-password", h.ChangePassword)
	return r
}

func TestMe_Get_FormsModeNoOIDC(t *testing.T) {
	t.Parallel()
	repo := newFakeMeRepo()
	email := "Admin@Example.com"
	repo.seed(admin.User{
		Username:   "admin",
		Email:      &email,
		Role:       admin.RoleAdmin,
		AvatarMode: admin.AvatarModeAuto,
	})
	r := setupMe(t, repo, &middleware.AuthRuntime{Mode: runtime.AuthModeForms}, "admin")

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/v1/me", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)

	var body dto.MeResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.Equal(t, "admin", body.Username)
	require.NotNil(t, body.Email)
	assert.Equal(t, "Admin@Example.com", *body.Email)
	assert.Equal(t, "forms", body.AuthMode)
	assert.Equal(t, "auto", body.AvatarMode)
	assert.Equal(t, "gravatar", body.AvatarResolvedMode)
	assert.Equal(t, admin.ComputeAvatarHash(email), body.AvatarHash)
	assert.Nil(t, body.IDPProfileURL)
	assert.Nil(t, body.OIDCSubject)
}

func TestMe_Get_OIDCModeWithSubject(t *testing.T) {
	t.Parallel()
	repo := newFakeMeRepo()
	email := "user@example.com"
	sub := "kc-sub-123"
	repo.seed(admin.User{
		Username:    "user",
		Email:       &email,
		Role:        admin.RoleUser,
		AvatarMode:  admin.AvatarModeAuto,
		OIDCSubject: &sub,
	})
	r := setupMe(t, repo, &middleware.AuthRuntime{
		Mode: runtime.AuthModeOIDC,
		OIDC: middleware.OIDCRuntime{Issuer: "https://kc.example.com/realms/lab"},
	}, "user")

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/v1/me", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)

	var body dto.MeResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.Equal(t, "oidc", body.AuthMode)
	require.NotNil(t, body.IDPProfileURL)
	assert.Equal(t, "https://kc.example.com/realms/lab/account", *body.IDPProfileURL)
	require.NotNil(t, body.OIDCSubject)
	assert.Equal(t, "kc-sub-123", *body.OIDCSubject)
}

func TestMe_Get_AvatarAutoNoEmailFallsToMonogram(t *testing.T) {
	t.Parallel()
	repo := newFakeMeRepo()
	repo.seed(admin.User{
		Username:   "ghost",
		Role:       admin.RoleAdmin,
		AvatarMode: admin.AvatarModeAuto,
	})
	r := setupMe(t, repo, &middleware.AuthRuntime{Mode: runtime.AuthModeForms}, "ghost")

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/v1/me", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)

	var body dto.MeResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.Equal(t, "monogram", body.AvatarResolvedMode)
	assert.Empty(t, body.AvatarHash)
	assert.Nil(t, body.Email)
}

func TestMe_Get_AvatarAutoWithEmailResolvesGravatar(t *testing.T) {
	t.Parallel()
	repo := newFakeMeRepo()
	email := "x@example.com"
	repo.seed(admin.User{
		Username:   "x",
		Email:      &email,
		Role:       admin.RoleAdmin,
		AvatarMode: admin.AvatarModeAuto,
	})
	r := setupMe(t, repo, &middleware.AuthRuntime{Mode: runtime.AuthModeForms}, "x")

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/v1/me", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)

	var body dto.MeResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.Equal(t, "gravatar", body.AvatarResolvedMode)
	assert.NotEmpty(t, body.AvatarHash)
}

func TestMe_Get_AvatarGravatarNoEmailSilentlyMonogram(t *testing.T) {
	t.Parallel()
	repo := newFakeMeRepo()
	repo.seed(admin.User{
		Username:   "ghost2",
		Role:       admin.RoleAdmin,
		AvatarMode: admin.AvatarModeGravatar,
	})
	r := setupMe(t, repo, &middleware.AuthRuntime{Mode: runtime.AuthModeForms}, "ghost2")

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/v1/me", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)

	var body dto.MeResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.Equal(t, "gravatar", body.AvatarMode)
	assert.Equal(t, "monogram", body.AvatarResolvedMode)
}

func TestMe_PatchSettings_Valid(t *testing.T) {
	t.Parallel()
	repo := newFakeMeRepo()
	email := "x@example.com"
	repo.seed(admin.User{
		Username:   "alice",
		Email:      &email,
		Role:       admin.RoleAdmin,
		AvatarMode: admin.AvatarModeAuto,
	})
	r := setupMe(t, repo, &middleware.AuthRuntime{Mode: runtime.AuthModeForms}, "alice")

	body := bytes.NewBufferString(`{"preferred_language":"ru","avatar_mode":"gravatar"}`)
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPatch, "/api/v1/me/settings", body)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)

	var resp dto.MeResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, "gravatar", resp.AvatarMode)
	require.NotNil(t, resp.PreferredLanguage)
	assert.Equal(t, "ru", *resp.PreferredLanguage)
}

func TestMe_PatchSettings_InvalidAvatarMode(t *testing.T) {
	t.Parallel()
	repo := newFakeMeRepo()
	repo.seed(admin.User{Username: "alice", Role: admin.RoleAdmin})
	r := setupMe(t, repo, &middleware.AuthRuntime{Mode: runtime.AuthModeForms}, "alice")

	body := bytes.NewBufferString(`{"avatar_mode":"sparkles"}`)
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPatch, "/api/v1/me/settings", body)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "avatar_mode")
}

func TestMe_PatchSettings_CustomModeRejected(t *testing.T) {
	t.Parallel()
	repo := newFakeMeRepo()
	repo.seed(admin.User{Username: "alice", Role: admin.RoleAdmin})
	r := setupMe(t, repo, &middleware.AuthRuntime{Mode: runtime.AuthModeForms}, "alice")

	body := bytes.NewBufferString(`{"avatar_mode":"custom"}`)
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPatch, "/api/v1/me/settings", body)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusBadRequest, w.Code,
		"custom must be rejected by the 3-value allowlist gate")
}

func TestMe_PatchSettings_UnknownField(t *testing.T) {
	t.Parallel()
	repo := newFakeMeRepo()
	repo.seed(admin.User{Username: "alice", Role: admin.RoleAdmin})
	r := setupMe(t, repo, &middleware.AuthRuntime{Mode: runtime.AuthModeForms}, "alice")

	body := bytes.NewBufferString(`{"theme":"dark"}`)
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPatch, "/api/v1/me/settings", body)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusBadRequest, w.Code,
		"DisallowUnknownFields must reject keys outside the patch shape")
}

func TestMe_ChangePassword_OIDCMode_Returns405(t *testing.T) {
	t.Parallel()
	repo := newFakeMeRepo()
	repo.seed(admin.User{Username: "alice", Role: admin.RoleAdmin})
	r := setupMe(t, repo, &middleware.AuthRuntime{
		Mode: runtime.AuthModeOIDC,
		OIDC: middleware.OIDCRuntime{Issuer: "https://kc.example.com/realms/lab"},
	}, "alice")

	body := bytes.NewBufferString(`{"current_password":"x","new_password":"NewSecretLongEnough"}`)
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/api/v1/me/change-password", body)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusMethodNotAllowed, w.Code)

	var env dto.MePasswordUnavailableResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &env))
	assert.Equal(t, "password_change_unavailable", env.Error)
	assert.Equal(t, "managed_by_idp", env.Reason)
	require.NotNil(t, env.ManageURL)
	assert.Equal(t, "https://kc.example.com/realms/lab/account", *env.ManageURL)
}

func TestMe_ChangePassword_BasicMode_Returns405(t *testing.T) {
	t.Parallel()
	repo := newFakeMeRepo()
	repo.seed(admin.User{Username: "alice", Role: admin.RoleAdmin})
	r := setupMe(t, repo, &middleware.AuthRuntime{Mode: runtime.AuthModeBasic}, "alice")

	body := bytes.NewBufferString(`{"current_password":"x","new_password":"NewSecretLongEnough"}`)
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/api/v1/me/change-password", body)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusMethodNotAllowed, w.Code)

	var env dto.MePasswordUnavailableResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &env))
	assert.Equal(t, "managed_by_basic_auth", env.Reason)
	assert.Nil(t, env.ManageURL)
}

func TestMe_ChangePassword_WrongCurrent_Returns401(t *testing.T) {
	t.Parallel()
	repo := newFakeMeRepo()
	hash, err := authapp.HashPassword("RealCurrentPassword!")
	require.NoError(t, err)
	repo.seed(admin.User{
		Username:     "alice",
		PasswordHash: hash,
		Role:         admin.RoleAdmin,
	})
	r := setupMe(t, repo, &middleware.AuthRuntime{Mode: runtime.AuthModeForms}, "alice")

	body := bytes.NewBufferString(`{"current_password":"wrong","new_password":"NewSecretLongEnough"}`)
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/api/v1/me/change-password", body)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestMe_ChangePassword_HappyPath_Returns204AndPersists(t *testing.T) {
	t.Parallel()
	repo := newFakeMeRepo()
	hash, err := authapp.HashPassword("RealCurrentPassword!")
	require.NoError(t, err)
	repo.seed(admin.User{
		Username:     "alice",
		PasswordHash: hash,
		Role:         admin.RoleAdmin,
	})
	r := setupMe(t, repo, &middleware.AuthRuntime{Mode: runtime.AuthModeForms}, "alice")

	body := bytes.NewBufferString(`{"current_password":"RealCurrentPassword!","new_password":"NewSecretLongEnough"}`)
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/api/v1/me/change-password", body)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusNoContent, w.Code)

	got, err := repo.GetByUsername(context.Background(), "alice")
	require.NoError(t, err)
	assert.True(t, authapp.VerifyPassword(got.PasswordHash, "NewSecretLongEnough"))
	assert.False(t, authapp.VerifyPassword(got.PasswordHash, "RealCurrentPassword!"))
}

// Defensive: empty body to PATCH /me/settings should not write but should
// 200 with the current user state. The handler treats io.EOF as no-op.
func TestMe_PatchSettings_EmptyBodyNoOp(t *testing.T) {
	t.Parallel()
	repo := newFakeMeRepo()
	email := "x@example.com"
	repo.seed(admin.User{
		Username:   "alice",
		Email:      &email,
		Role:       admin.RoleAdmin,
		AvatarMode: admin.AvatarModeAuto,
	})
	r := setupMe(t, repo, &middleware.AuthRuntime{Mode: runtime.AuthModeForms}, "alice")

	req := httptest.NewRequestWithContext(t.Context(), http.MethodPatch, "/api/v1/me/settings", strings.NewReader(""))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)
}

// Defensive: api-key + local-bypass usernames have no row → 401.
func TestMe_Get_RejectsAPIKeyPseudoUser(t *testing.T) {
	t.Parallel()
	repo := newFakeMeRepo()
	r := setupMe(t, repo, &middleware.AuthRuntime{Mode: runtime.AuthModeForms}, "api-key")

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/v1/me", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusUnauthorized, w.Code)
}

// Verify the use case typed errors surface to the right status codes.
func TestMe_ChangePassword_TooShort_Returns400(t *testing.T) {
	t.Parallel()
	repo := newFakeMeRepo()
	hash, err := authapp.HashPassword("RealCurrentPassword!")
	require.NoError(t, err)
	repo.seed(admin.User{
		Username:     "alice",
		PasswordHash: hash,
		Role:         admin.RoleAdmin,
	})
	r := setupMe(t, repo, &middleware.AuthRuntime{Mode: runtime.AuthModeForms}, "alice")

	body := bytes.NewBufferString(`{"current_password":"RealCurrentPassword!","new_password":"short"}`)
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/api/v1/me/change-password", body)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "password too short")
}

// Defensive guard for direct use-case typed-error paths (decouples
// MeHandler test coverage from the handler's error mapping table).
func TestMeUseCase_ChangePassword_SameAsCurrent(t *testing.T) {
	t.Parallel()
	repo := newFakeMeRepo()
	hash, err := authapp.HashPassword("RealCurrentPassword!")
	require.NoError(t, err)
	repo.seed(admin.User{
		Username:     "alice",
		PasswordHash: hash,
		Role:         admin.RoleAdmin,
	})
	uc := authapp.NewMeUseCase(repo)
	user, err := uc.GetByUsername(context.Background(), "alice")
	require.NoError(t, err)
	err = uc.ChangePassword(context.Background(), user, "RealCurrentPassword!", "RealCurrentPassword!")
	require.Error(t, err)
	assert.True(t, errors.Is(err, authapp.ErrNewPasswordSameAsCurrent))
}
