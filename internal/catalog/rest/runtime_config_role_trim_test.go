package rest

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	admin "github.com/alexmorbo/seasonfill/internal/admin/domain"
	"github.com/alexmorbo/seasonfill/internal/catalog/app/runtimeconfig"
	"github.com/alexmorbo/seasonfill/internal/runtime"
	"github.com/alexmorbo/seasonfill/internal/shared/http/middleware"
)

func TestRC_Get_TrimsForNonAdmin(t *testing.T) {
	repo := &rcFakeRuntime{}
	uc := runtimeconfig.New(repo, rcFakeInstances{}, nil, runtime.NewBus(nil), slog.Default())
	resolver := fakeRoleResolver{"admin": admin.RoleAdmin, "ipad": admin.RoleUser}
	h := NewRuntimeConfigHandler(uc, slog.Default()).WithAdminResolver(resolver)

	// Seed a row with a non-trivial auth subtree + a guid_rewrite whose `from`
	// embeds a cluster-internal tracker-proxy hostname — the exact leak F-09
	// closes for non-admins.
	const clusterHost = "http://rutracker-proxy.servarr.svc.cluster.local"
	seed := validRCBody()
	seed["guid_rewrites"] = []map[string]any{{"from": clusterHost, "to": "https://rutracker.org"}}
	{
		r := gin.New()
		r.PUT("/api/v1/config/runtime", h.Update)
		w := rcDoJSON(t, r, http.MethodPut, "/api/v1/config/runtime", seed, nil)
		require.Equal(t, http.StatusOK, w.Code, "seed body=%s", w.Body.String())
	}

	get := func(username string) (map[string]any, string) {
		r := gin.New()
		r.GET("/api/v1/config/runtime", func(c *gin.Context) {
			c.Set(middleware.UsernameContextKey, username)
		}, h.Get)
		w := rcDoJSON(t, r, http.MethodGet, "/api/v1/config/runtime", nil, nil)
		require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())
		var body map[string]any
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
		return body, w.Body.String()
	}

	// Admin — full payload incl. the cluster-hostname rewrite.
	adminBody, adminRaw := get("admin")
	assert.Equal(t, "12h0m0s", adminBody["auth"].(map[string]any)["session_ttl"])
	assert.Equal(t, "0 */6 * * *", adminBody["cron"].(map[string]any)["schedule"])
	adminGR, ok := adminBody["guid_rewrites"].([]any)
	require.True(t, ok, "admin sees guid_rewrites in full")
	require.Len(t, adminGR, 1)
	assert.Equal(t, clusterHost, adminGR[0].(map[string]any)["from"])
	assert.Contains(t, adminRaw, "svc.cluster.local", "admin body carries the cluster host")

	// Non-admin — operator/infra fields zeroed; guid_rewrites emptied so the
	// cluster host never leaks anywhere in the body.
	userBody, userRaw := get("ipad")
	assert.Empty(t, userBody["auth"].(map[string]any)["session_ttl"],
		"auth.session_ttl must be blanked for non-admin")
	assert.Empty(t, userBody["auth"].(map[string]any)["trusted_proxies"],
		"trusted_proxies must not leak to non-admin")
	assert.Empty(t, userBody["cron"].(map[string]any)["schedule"],
		"cron.schedule must be blanked")
	assert.Equal(t, false, userBody["auto_generated_api_key"])
	assert.NotContains(t, userRaw, "127.0.0.1", "trusted-proxy IPs must not leak")
	assert.NotContains(t, userRaw, "svc.cluster.local",
		"cluster tracker-proxy host must never appear in a non-admin body")
	assert.NotContains(t, userRaw, "sonarr", "no sonarr cluster host anywhere")
	assert.NotContains(t, userRaw, "radarr", "no radarr cluster host anywhere")

	userGR, ok := userBody["guid_rewrites"].([]any)
	require.True(t, ok, "guid_rewrites stays a JSON array (never null)")
	assert.Empty(t, userGR, "guid_rewrites must be emptied for non-admin (F-09 override)")

	// Fail-closed: an unknown user (resolver errors) is treated as non-admin.
	ghostBody, ghostRaw := get("ghost")
	assert.Empty(t, ghostBody["auth"].(map[string]any)["session_ttl"],
		"unknown user fails closed to the trimmed view")
	assert.NotContains(t, ghostRaw, "svc.cluster.local")
}
