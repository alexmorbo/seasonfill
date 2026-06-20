package handlers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"

	"github.com/alexmorbo/seasonfill/application/ports"
	"github.com/alexmorbo/seasonfill/interface/http/middleware"
	auth "github.com/alexmorbo/seasonfill/internal/admin/app"
	admin "github.com/alexmorbo/seasonfill/internal/admin/domain"
	infraoidc "github.com/alexmorbo/seasonfill/internal/admin/infrastructure/oidc"
	"github.com/alexmorbo/seasonfill/internal/runtime"
)

// stubAdminRepo satisfies ports.AdminUserRepository for unit tests that
// do not exercise the actual repo. OIDC-specific methods return
// ErrNotFound; the other methods return zero values.
type stubAdminRepo struct{}

func (stubAdminRepo) Get(context.Context) (admin.AdminUser, error) {
	return admin.AdminUser{}, ports.ErrNotFound
}
func (stubAdminRepo) GetByOIDCSubject(context.Context, string) (admin.AdminUser, error) {
	return admin.AdminUser{}, ports.ErrNotFound
}
func (stubAdminRepo) Create(context.Context, admin.AdminUser) error { return nil }
func (stubAdminRepo) CreateFromOIDC(context.Context, string, string) (admin.AdminUser, error) {
	return admin.AdminUser{}, nil
}
func (stubAdminRepo) UpdatePassword(context.Context, string, bool) error { return nil }

func TestOIDCStart_NotConfigured_ReturnsServiceUnavailable(t *testing.T) {
	t.Parallel()
	gin.SetMode(gin.TestMode)
	ptr := &middleware.AuthRuntimePointer{}
	ptr.Store(&middleware.AuthRuntime{Mode: runtime.AuthModeOIDC})
	uc := auth.NewOIDCLoginUseCase(infraoidc.NewProviderCache(), stubAdminRepo{})
	h := NewOIDCHandler(uc, ptr, []byte("k"), 0, false, nil)

	r := gin.New()
	r.GET("/api/v1/auth/oidc/start", h.Start)
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/v1/auth/oidc/start", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusServiceUnavailable, w.Code)
	assert.Contains(t, w.Body.String(), "OIDC_NOT_CONFIGURED")
}

func TestOIDCConfig_ThreadsClientSecretFromRuntime(t *testing.T) {
	t.Parallel()
	ptr := &middleware.AuthRuntimePointer{}
	ptr.Store(&middleware.AuthRuntime{
		OIDC: middleware.OIDCRuntime{
			Issuer: "https://example.test", ClientID: "c", RedirectURL: "https://x/cb",
			Scopes: []string{"openid"}, UsernameClaim: "preferred_username",
			ClientSecret: "secret-from-runtime", GroupsClaim: "groups",
		},
	})
	h := NewOIDCHandler(nil, ptr, nil, 0, false, nil)
	cfg := h.config()
	assert.Equal(t, "secret-from-runtime", cfg.ClientSecret)
	assert.Equal(t, "https://example.test", cfg.Issuer)
	assert.Equal(t, "c", cfg.ClientID)
	assert.Equal(t, "groups", cfg.GroupsClaim)
}

func TestOIDCConfig_NilRuntimeReturnsEmpty(t *testing.T) {
	t.Parallel()
	ptr := &middleware.AuthRuntimePointer{}
	h := NewOIDCHandler(nil, ptr, nil, 0, false, nil)
	cfg := h.config()
	assert.Empty(t, cfg.Issuer)
	// Client secret flows from runtime; nil runtime → empty config.
}

// Full Start/Callback success paths exercise the provider cache (HTTP
// discovery) — covered in tests/integration/oidc_callback_e2e_test.go.
func TestOIDCStart_HappyPath_DeferredToIntegration(t *testing.T) {
	t.Skip("covered in tests/integration/oidc_callback_e2e_test.go")
}
