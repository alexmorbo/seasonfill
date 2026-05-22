package middleware

import (
	"crypto/subtle"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
)

// RequireAuth gates protected routes. Accepts a valid session
// cookie OR matching X-Api-Key header (cookie first). Identical
// 401 across all failure modes — callers cannot probe which path
// failed. Header fallback preserves the CLI contract; browsers
// ride the cookie. Empty cookieSecret disables the cookie path
// (VerifyCookie always fails) — the webhook's APIKeyAuth shim
// relies on this for privilege isolation.
func RequireAuth(apiKey, cookieSecret string) gin.HandlerFunc {
	secretBytes := []byte(cookieSecret)
	return func(c *gin.Context) {
		// Cookie path first — cheap when present and valid.
		if cookie, err := c.Cookie(SessionCookieName); err == nil && cookie != "" {
			if VerifyCookie(secretBytes, cookie, time.Now()) == nil {
				c.Next()
				return
			}
			// Fall through to header. Don't log the cookie-failure
			// reason — leaking ErrCookieMalformed vs Signature vs
			// Expired to log readers defeats the single-401 design.
		}

		got := c.GetHeader("X-Api-Key")
		if apiKey != "" && subtle.ConstantTimeCompare([]byte(got), []byte(apiKey)) == 1 {
			c.Next()
			return
		}

		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
			"error": "unauthorized",
			"code":  "UNAUTHORIZED",
		})
	}
}

// APIKeyAuth is a thin shim over RequireAuth so the webhook route
// keeps a single-argument constructor. Passing an empty cookie
// secret disables the cookie path (VerifyCookie always fails) so
// the request falls through to header-only auth — exactly what the
// webhook needs to stay cookie-deaf for privilege isolation.
func APIKeyAuth(expected string) gin.HandlerFunc {
	return RequireAuth(expected, "")
}
