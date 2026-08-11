package middleware

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"

	admin "github.com/alexmorbo/seasonfill/internal/admin/domain"
	ports "github.com/alexmorbo/seasonfill/internal/shared/dataports"
	sharedErrors "github.com/alexmorbo/seasonfill/internal/shared/errors"
)

// stubUserRepo implements ports.UserRepository; only GetByUsername is
// exercised by RequirePermission. byName maps username → row; a miss
// returns UserNotFoundError joined with ErrNotFound.
type stubUserRepo struct {
	byName map[string]admin.User
}

func (s *stubUserRepo) GetByUsername(_ context.Context, name string) (admin.User, error) {
	if u, ok := s.byName[name]; ok {
		return u, nil
	}
	return admin.User{}, errors.Join(&sharedErrors.UserNotFoundError{}, ports.ErrNotFound)
}

// Unused-by-guard interface methods.
func (s *stubUserRepo) Get(context.Context) (admin.User, error) {
	return admin.User{}, ports.ErrNotFound
}
func (s *stubUserRepo) GetByOIDCSubject(context.Context, string) (admin.User, error) {
	return admin.User{}, ports.ErrNotFound
}
func (s *stubUserRepo) Create(context.Context, admin.User) error { return nil }
func (s *stubUserRepo) CreateFromOIDC(context.Context, string, string, string) (admin.User, error) {
	return admin.User{}, nil
}
func (s *stubUserRepo) UpdatePassword(context.Context, uint, string) error { return nil }
func (s *stubUserRepo) UpdateSettings(context.Context, uint, ports.UserSettingsPatch) error {
	return nil
}
func (s *stubUserRepo) UpdateLastLoginAt(context.Context, uint, time.Time) error { return nil }

func runGuard(t *testing.T, repo ports.UserRepository, principal string, perms []string) int {
	t.Helper()
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/x", nil)
	if principal != "" {
		c.Set(UsernameContextKey, principal)
	}
	guard := RequirePermission(repo, perms...)
	guard(c)
	return w.Code
}

func TestRequirePermission_Matrix(t *testing.T) {
	t.Parallel()
	adminUser := admin.User{Username: "admin", Role: admin.RoleAdmin}
	requester := admin.User{Username: "bob", Role: admin.RoleUser, Request: true}
	plain := admin.User{Username: "eve", Role: admin.RoleUser}
	repo := &stubUserRepo{byName: map[string]admin.User{
		"admin": adminUser,
		"bob":   requester,
		"eve":   plain,
	}}

	cases := []struct {
		name      string
		principal string
		perms     []string
		wantCode  int // 200 == allowed (gin default when not aborted), else the abort code
	}{
		{"api-key principal is admin-equivalent", "api-key", []string{PermManageUsers}, http.StatusOK},
		{"admin role short-circuits admin bucket", "admin", []string{PermManageUsers, PermManageRequests}, http.StatusOK},
		{"admin role short-circuits request bucket", "admin", []string{PermRequest}, http.StatusOK},
		{"user WITH request perm allowed on request bucket", "bob", []string{PermRequest}, http.StatusOK},
		{"user WITHOUT perm denied on request bucket", "eve", []string{PermRequest}, http.StatusForbidden},
		{"user WITHOUT manage perm denied on admin bucket", "bob", []string{PermManageUsers, PermManageRequests}, http.StatusForbidden},
		{"unknown user denied", "ghost", []string{PermRequest}, http.StatusForbidden},
		{"missing principal 401", "", []string{PermRequest}, http.StatusUnauthorized},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			code := runGuard(t, repo, tc.principal, tc.perms)
			assert.Equal(t, tc.wantCode, code)
		})
	}
}
