package middleware

import (
	"context"
	"crypto/subtle"
	"errors"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/alexmorbo/seasonfill/application/auth"
	"github.com/alexmorbo/seasonfill/application/ports"
	"github.com/alexmorbo/seasonfill/internal/runtime"
)

const UsernameContextKey = "auth.username"

// RequireAuth preserves the pre-036a signature so existing callers
// (tests, helm-deployed binaries that haven't been rebuilt) keep
// compiling and behave as forms-mode. Internally it builds a
// throwaway AuthRuntimePointer seeded with mode=forms + epoch=0 and
// delegates to RequireAuthWithRuntime with nil basic deps — Basic
// mode is not reachable through this shim because the default
// snapshot is forms.
func RequireAuth(apiKey string, sessionKey []byte) gin.HandlerFunc {
	ptr := &AuthRuntimePointer{}
	ptr.Store(&AuthRuntime{Mode: runtime.AuthModeForms})
	return RequireAuthWithRuntime(apiKey, sessionKey, ptr, nil, nil)
}

// RequireAuthWithRuntime gates protected routes. Mode is dispatched per
// request from the AuthRuntime atomic (hot-reload safe — never cache
// the pointer across requests). Order of evaluation:
//
//  1. X-Api-Key — honored in ALL modes (automation invariant, D-9)
//  2. Mode-specific path:
//     - forms → cookie check + epoch validation
//     - basic → Authorization: Basic header parse + constant-latency
//     bcrypt compare against admin_users.password_hash. Missing or
//     malformed header → 401 + WWW-Authenticate. Bad creds → 401
//     without WWW-Authenticate (no popup re-prompt loop).
//     - none  → pass through as anonymous (no cookie required)
//  3. Fallthrough → 401 UNAUTHORIZED with the identical envelope
//     pre-036a returned.
//
// adminRepo + loginLimiter are required for Basic mode; if nil and
// mode=basic happens to be active (test shim only), the dispatcher
// falls through to the generic 401.
func RequireAuthWithRuntime(
	apiKey string,
	sessionKey []byte,
	ptr *AuthRuntimePointer,
	adminRepo ports.AdminUserRepository,
	loginLimiter *auth.IPLimiter,
) gin.HandlerFunc {
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
			if adminRepo == nil {
				break
			}
			handleBasicAuth(c, adminRepo, loginLimiter)
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

// handleBasicAuth runs the Basic-mode credential check. It is split out
// so RequireAuthWithRuntime stays readable. On success it sets the
// username context key and calls c.Next(). On any failure it writes a
// 401 (or 429 / 500 as appropriate) and returns without calling Next.
//
// Branch outcomes:
//   - header absent or malformed → 401 + WWW-Authenticate: Basic realm=…
//   - rate-limit tripped → 429 (identical envelope to Login's 429)
//   - repo error other than not-found → 500
//   - creds bad (any of: user mismatch, password mismatch, no row) → 401
//     WITHOUT WWW-Authenticate. Constant-latency bcrypt still runs even
//     on user-not-found via auth.ConstantLatencyVerify.
//   - creds good → set context user, c.Next()
func handleBasicAuth(c *gin.Context, repo ports.AdminUserRepository, lim *auth.IPLimiter) {
	header := c.GetHeader("Authorization")
	user, pass, ok := parseBasicHeader(header)
	if !ok {
		c.Header("WWW-Authenticate", basicRealmHeader)
		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
			"error": "unauthorized", "code": "UNAUTHORIZED",
		})
		return
	}

	if lim != nil && !lim.Allow(c.ClientIP()) {
		c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{
			"error": "Invalid credentials", "code": "UNAUTHORIZED",
		})
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 2*time.Second)
	defer cancel()
	row, err := repo.Get(ctx)
	if err != nil && !errors.Is(err, ports.ErrNotFound) {
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{
			"error": "internal server error",
		})
		return
	}

	usernameMatches := err == nil && row.Username == user
	hashToCompare := row.PasswordHash
	if !usernameMatches {
		hashToCompare = ""
	}
	if !auth.ConstantLatencyVerify(hashToCompare, pass) {
		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
			"error": "unauthorized", "code": "UNAUTHORIZED",
		})
		return
	}

	c.Set(UsernameContextKey, row.Username)
	c.Next()
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
