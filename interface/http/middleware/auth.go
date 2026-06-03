package middleware

import (
	"crypto/subtle"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/alexmorbo/seasonfill/internal/runtime"
)

const UsernameContextKey = "auth.username"

// RequireAuth preserves the pre-036a signature so existing callers
// (tests, helm-deployed binaries that haven't been rebuilt) keep
// compiling and behave as forms-mode. Internally it builds a
// throwaway AuthRuntimePointer seeded with mode=forms + epoch=0 and
// delegates to RequireAuthWithRuntime.
func RequireAuth(apiKey string, sessionKey []byte) gin.HandlerFunc {
	ptr := &AuthRuntimePointer{}
	ptr.Store(&AuthRuntime{Mode: runtime.AuthModeForms})
	return RequireAuthWithRuntime(apiKey, sessionKey, ptr)
}

// RequireAuthWithRuntime gates protected routes. Mode is dispatched per
// request from the AuthRuntime atomic (hot-reload safe — never cache
// the pointer across requests). Order of evaluation:
//
//  1. X-Api-Key — honored in ALL modes (automation invariant, D-9)
//  2. Mode-specific path:
//     - forms → cookie check + epoch validation
//     - basic → 036b stub: returns 501 NOT_IMPLEMENTED (036b lands the
//     Authorization-header parser + WWW-Authenticate response)
//     - none  → pass through as anonymous (no cookie required)
//  3. Fallthrough → 401 UNAUTHORIZED with the identical envelope
//     pre-036a returned (rejection sentinel is not leaked).
func RequireAuthWithRuntime(apiKey string, sessionKey []byte, ptr *AuthRuntimePointer) gin.HandlerFunc {
	rawKeyBytes := []byte(apiKey)
	return func(c *gin.Context) {
		rt := loadAuthRuntime(ptr)

		// X-Api-Key first — automation must never be blocked by a mode.
		if apiKey != "" {
			got := c.GetHeader("X-Api-Key")
			if got != "" && subtle.ConstantTimeCompare([]byte(got), rawKeyBytes) == 1 {
				c.Set(UsernameContextKey, "api-key")
				c.Next()
				return
			}
		}

		switch rt.Mode {
		case runtime.AuthModeNone:
			c.Set(UsernameContextKey, "anonymous")
			c.Next()
			return
		case runtime.AuthModeBasic:
			// 036b replaces this stub with a full Basic Auth handler
			// (WWW-Authenticate: Basic realm="Seasonfill" + constant-
			// time compare against admin_users.password_hash). Until
			// then, surface a distinct 501 so an operator who flips
			// the mode pre-036b deploy sees an unambiguous error
			// instead of a silent 401.
			c.AbortWithStatusJSON(http.StatusNotImplemented, gin.H{
				"error": "basic auth not yet implemented", "code": "NOT_IMPLEMENTED",
			})
			return
		default:
			// runtime.AuthModeForms (or empty-string fallback).
			if cookie, err := c.Cookie(SessionCookieName); err == nil && cookie != "" {
				if p, verr := VerifySession(sessionKey, cookie, time.Now(), rt.SessionEpoch); verr == nil {
					c.Set(UsernameContextKey, p.Username)
					c.Next()
					return
				}
			}
		}
		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
			"error": "unauthorized", "code": "UNAUTHORIZED",
		})
	}
}

// loadAuthRuntime returns the current snapshot or a forms-mode default
// when the atomic is nil/unset. Never returns nil.
func loadAuthRuntime(ptr *AuthRuntimePointer) *AuthRuntime {
	if ptr == nil {
		return &AuthRuntime{Mode: runtime.AuthModeForms}
	}
	v := ptr.Load()
	if v == nil {
		return &AuthRuntime{Mode: runtime.AuthModeForms}
	}
	return v
}
