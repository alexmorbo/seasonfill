package rest

import (
	"context"

	"github.com/gin-gonic/gin"

	admin "github.com/alexmorbo/seasonfill/internal/admin/domain"
	"github.com/alexmorbo/seasonfill/internal/shared/http/middleware"
)

// AdminRoleResolver resolves the caller's role for the auth-only GET handlers
// (List instances, Get runtime config) so they can TRIM cluster-internal
// fields (upstream URLs, operator config) from a non-admin response WITHOUT
// promoting the whole route to admin-only — the sidebar still needs instance
// names. Narrow port mirroring middleware.RequirePermission's lookup;
// ports.UserRepository satisfies it via GetByUsername. F-09.
type AdminRoleResolver interface {
	GetByUsername(ctx context.Context, username string) (admin.User, error)
}

// callerIsAdmin reports whether the authenticated request principal holds the
// admin role. Dispatch mirrors RequirePermission / RequestHandler.caller:
//
//   - "api-key" automation principal → admin (never trimmed).
//   - resolver unwired (nil) → admin. nil-OK preserves the builder convention
//     used across this package; PRODUCTION ALWAYS wires the resolver (see
//     edge/server.go), so the trim is live in prod. Unwired only happens in
//     focused unit tests / minimal boot.
//   - empty username, or a failed / vanished lookup → NON-admin (fail-closed:
//     a caller we cannot positively confirm as admin gets the trimmed view).
//   - u.Role == admin → admin; otherwise non-admin.
func callerIsAdmin(c *gin.Context, resolver AdminRoleResolver) bool {
	username := c.GetString(middleware.UsernameContextKey)
	if username == "api-key" {
		return true
	}
	if resolver == nil {
		return true
	}
	if username == "" {
		return false
	}
	u, err := resolver.GetByUsername(c.Request.Context(), username)
	if err != nil {
		return false
	}
	return u.Role == admin.RoleAdmin
}
