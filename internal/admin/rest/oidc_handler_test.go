package rest

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"

	auth "github.com/alexmorbo/seasonfill/internal/admin/app"
	admin "github.com/alexmorbo/seasonfill/internal/admin/domain"
	infraoidc "github.com/alexmorbo/seasonfill/internal/admin/infrastructure/oidc"
	ports "github.com/alexmorbo/seasonfill/internal/shared/dataports"
	"github.com/alexmorbo/seasonfill/internal/shared/http/middleware"
)

// stubAdminRepo satisfies ports.UserRepository for unit tests that do
// not exercise the actual repo. OIDC-specific methods return ErrNotFound;
// the other methods return zero values.
type stubAdminRepo struct{}

func (stubAdminRepo) Get(context.Context) (admin.User, error) {
	return admin.User{}, ports.ErrNotFound
}
func (stubAdminRepo) GetByOIDCSubject(context.Context, string) (admin.User, error) {
	return admin.User{}, ports.ErrNotFound
}
func (stubAdminRepo) Create(context.Context, admin.User) error { return nil }
func (stubAdminRepo) CreateFromOIDC(context.Context, string, string, string) (admin.User, error) {
	return admin.User{}, nil
}
func (stubAdminRepo) UpdatePassword(context.Context, uint, string) error       { return nil }
func (stubAdminRepo) UpdateLastLoginAt(context.Context, uint, time.Time) error { return nil }
func (stubAdminRepo) GetByUsername(context.Context, string) (admin.User, error) {
	return admin.User{}, ports.ErrNotFound
}
func (stubAdminRepo) UpdateSettings(context.Context, uint, ports.UserSettingsPatch) error {
	return nil
}

func TestOIDCStart_NotConfigured_ReturnsServiceUnavailable(t *testing.T) {
	t.Parallel()
	gin.SetMode(gin.TestMode)
	ptr := &middleware.AuthRuntimePointer{}
	ptr.Store(&middleware.AuthRuntime{})
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
