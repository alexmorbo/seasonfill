package middleware

import (
	"net/http"

	"github.com/gin-gonic/gin"

	admin "github.com/alexmorbo/seasonfill/internal/admin/domain"
	ports "github.com/alexmorbo/seasonfill/internal/shared/dataports"
)

// Permission identifiers — mirror the bool columns added to `users` in
// migration 000055. Kept as package-local typed strings so route
// registrations read declaratively (middleware.PermRequest) without the
// edge server importing the admin domain.
const (
	PermAutoApprove    = "auto_approve"
	PermRequest        = "request"
	PermManageRequests = "manage_requests"
	PermManageUsers    = "manage_users"
	PermRequest4K      = "request_4k"
)

// RequirePermission returns a gin middleware enforcing RBAC on a mutating
// route. Dispatch (cheapest reject first):
//
//  1. The X-Api-Key principal ("api-key", set by buildAuth) is automation
//     and is treated as admin-equivalent — short-circuit allow BEFORE any
//     DB hit. This preserves the pre-RBAC behaviour where any valid
//     X-Api-Key caller could hit every guarded route.
//  2. role=='admin' → allow-all (Overseerr ADMIN short-circuit).
//  3. otherwise the user must hold AT LEAST ONE of perms (Seerr or-helper).
//
// A missing principal → 401 (defensive; RequireAuth already ran upstream).
// An unknown/vanished user row → 403 (treated as no-longer-authorized
// rather than 500). Denied → 403 {"error":"forbidden","code":"PERMISSION_DENIED"}.
//
// The returned closure is stateless and safe to share across many routes.
func RequirePermission(repo ports.UserRepository, perms ...string) gin.HandlerFunc {
	return func(c *gin.Context) {
		username := c.GetString(UsernameContextKey)

		// Automation principal — admin-equivalent, no DB lookup.
		if username == "api-key" {
			c.Next()
			return
		}
		if username == "" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
				"error": "unauthorized", "code": "AUTH_REQUIRED",
			})
			return
		}

		u, err := repo.GetByUsername(c.Request.Context(), username)
		if err != nil {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{
				"error": "forbidden", "code": "PERMISSION_DENIED",
			})
			return
		}

		// Overseerr ADMIN short-circuit — role=admin passes every check.
		if u.Role == admin.RoleAdmin {
			c.Next()
			return
		}

		for _, p := range perms {
			if hasPermission(u, p) {
				c.Next()
				return
			}
		}

		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{
			"error": "forbidden", "code": "PERMISSION_DENIED",
		})
	}
}

// hasPermission maps a permission identifier to the matching bool field
// on the user. Unknown identifiers return false (fail-closed).
func hasPermission(u admin.User, perm string) bool {
	switch perm {
	case PermAutoApprove:
		return u.AutoApprove
	case PermRequest:
		return u.Request
	case PermManageRequests:
		return u.ManageRequests
	case PermManageUsers:
		return u.ManageUsers
	case PermRequest4K:
		return u.Request4K
	default:
		return false
	}
}
