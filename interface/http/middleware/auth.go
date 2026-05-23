package middleware

import (
	"crypto/subtle"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
)

const UsernameContextKey = "auth.username"

// RequireAuth gates protected routes. Cookie first, X-Api-Key fallback.
// Cookie HMAC key IS apiKey (D48). Identical 401 envelope across modes.
func RequireAuth(apiKey string) gin.HandlerFunc {
	keyBytes := []byte(apiKey)
	return func(c *gin.Context) {
		if cookie, err := c.Cookie(SessionCookieName); err == nil && cookie != "" {
			if p, verr := VerifySession(keyBytes, cookie, time.Now()); verr == nil {
				c.Set(UsernameContextKey, p.Username)
				c.Next()
				return
			}
		}
		got := c.GetHeader("X-Api-Key")
		if apiKey != "" && subtle.ConstantTimeCompare([]byte(got), keyBytes) == 1 {
			c.Set(UsernameContextKey, "api-key")
			c.Next()
			return
		}
		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
			"error": "unauthorized", "code": "UNAUTHORIZED",
		})
	}
}
