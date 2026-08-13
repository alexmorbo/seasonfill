package rest

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	admin "github.com/alexmorbo/seasonfill/internal/admin/domain"
	"github.com/alexmorbo/seasonfill/internal/admin/rest/healthcheck"
	ports "github.com/alexmorbo/seasonfill/internal/shared/dataports"
	"github.com/alexmorbo/seasonfill/internal/shared/http/dto"
	"github.com/alexmorbo/seasonfill/internal/shared/http/middleware"
)

// fakeRoleResolver maps username → role for the F-09 trim tests. Shared across
// role_trim_test.go and runtime_config_role_trim_test.go (same package). A
// username absent from the map resolves to an error → callerIsAdmin fails
// closed → the caller is treated as NON-admin (trimmed view).
type fakeRoleResolver map[string]string

func (f fakeRoleResolver) GetByUsername(_ context.Context, u string) (admin.User, error) {
	role, ok := f[u]
	if !ok {
		return admin.User{}, errors.New("no such user")
	}
	return admin.User{Username: u, Role: role}, nil
}

func TestInstancesHandler_List_TrimsClusterURLForNonAdmin(t *testing.T) {
	c := healthcheck.New(openInstancesDB(t), []ports.SonarrClient{&fakeSonarr{name: "alpha"}})
	c.Preflight(context.Background())
	urls := map[string]string{"alpha": "http://sonarr.internal:8989"}
	resolver := fakeRoleResolver{"admin": admin.RoleAdmin, "ipad": admin.RoleUser}
	h := NewInstancesHandler(c, buildRegistry(nil, nil, urls), nil).WithAdminResolver(resolver)

	cases := []struct {
		name       string
		username   string
		wantURL    string
		urlVisible bool
	}{
		{"admin sees cluster url", "admin", "http://sonarr.internal:8989", true},
		{"non-admin url trimmed", "ipad", "", false},
		// fail-closed: an unknown/vanished user (resolver returns error) is
		// treated as non-admin and gets the trimmed view.
		{"unknown user fails closed to trimmed", "ghost", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := gin.New()
			r.GET("/api/v1/admin/instances", func(ctx *gin.Context) {
				ctx.Set(middleware.UsernameContextKey, tc.username)
			}, h.List)
			req := httptest.NewRequestWithContext(context.Background(), http.MethodGet,
				"/api/v1/admin/instances", nil)
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)
			require.Equal(t, http.StatusOK, w.Code)

			var body dto.InstanceList
			require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
			require.Len(t, body.Instances, 1)
			assert.Equal(t, "alpha", body.Instances[0].Name, "public identity always present")
			assert.Equal(t, tc.wantURL, body.Instances[0].URL)
			if !tc.urlVisible {
				assert.NotContains(t, w.Body.String(), "sonarr.internal",
					"cluster host must never appear in a non-admin body")
			}
		})
	}
}
