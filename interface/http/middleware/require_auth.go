package middleware

import (
	"crypto/subtle"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
)

// UsernameContextKey is where RequireAuthV2 stores the resolved username.
const UsernameContextKey = "auth.username"

// RequireAuthV2 gates protected routes. Cookie first, X-Api-Key fallback.
// Identical 401 envelope across all failure modes.
//
// The cookie HMAC key IS apiKey (D48). 021a-2 renames this to
// RequireAuth and deletes the old two-arg version.
func RequireAuthV2(apiKey string) gin.HandlerFunc {
	apiKeyBytes := []byte(apiKey)
	return func(c *gin.Context) {
		if cookie, err := c.Cookie(NewSessionCookieName); err == nil && cookie != "" {
			if p, verr := VerifySession(apiKeyBytes, cookie, time.Now()); verr == nil {
				c.Set(UsernameContextKey, p.Username)
				c.Next()
				return
			}
		}
		got := c.GetHeader("X-Api-Key")
		if apiKey != "" && subtle.ConstantTimeCompare([]byte(got), apiKeyBytes) == 1 {
			c.Set(UsernameContextKey, "api-key")
			c.Next()
			return
		}
		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
			"error": "unauthorized", "code": "UNAUTHORIZED",
		})
	}
}
