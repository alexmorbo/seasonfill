package middleware

import (
	"context"
	"crypto/subtle"
	"errors"
	"net"
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
// compiling and behave as forms-mode. Internally builds a throwaway
// AuthRuntimePointer seeded with mode=forms + epoch=0 and delegates to
// RequireAuthWithRuntime with nil basic deps.
func RequireAuth(apiKey string, sessionKey []byte) gin.HandlerFunc {
	ptr := &AuthRuntimePointer{}
	ptr.Store(&AuthRuntime{Mode: runtime.AuthModeForms})
	return RequireAuthWithRuntime(apiKey, sessionKey, ptr, nil, nil)
}

// RequireAuthWithRuntime gates protected non-webhook routes. Dispatch order:
//
//  1. X-Api-Key check (precedence — automation must never be silently
//     attributed to "local")
//  2. Local-bypass (if rt.LocalBypass=true AND client IP ∈ rt.LocalNetworks)
//  3. Mode-specific path (forms | basic | none)
//
// adminRepo + loginLimiter are required only for Basic mode.
func RequireAuthWithRuntime(
	apiKey string,
	sessionKey []byte,
	ptr *AuthRuntimePointer,
	adminRepo ports.AdminUserRepository,
	loginLimiter *auth.IPLimiter,
) gin.HandlerFunc {
	return buildAuth(apiKey, sessionKey, ptr, adminRepo, loginLimiter, true)
}

// RequireAuthWebhook is the webhook-route variant. IDENTICAL to
// RequireAuthWithRuntime except step 2 (local-bypass) is unconditionally
// skipped. Webhook ALWAYS requires X-Api-Key — invariant D-3 / AC-8.
// Mode dispatch still runs after the X-Api-Key check, but in production
// Sonarr always sends X-Api-Key, so the fallthrough is effectively a
// safety net (401 on any non-keyed webhook call regardless of mode).
func RequireAuthWebhook(
	apiKey string,
	sessionKey []byte,
	ptr *AuthRuntimePointer,
	adminRepo ports.AdminUserRepository,
	loginLimiter *auth.IPLimiter,
) gin.HandlerFunc {
	return buildAuth(apiKey, sessionKey, ptr, adminRepo, loginLimiter, false)
}

// buildAuth is the shared pipeline. localBypassAllowed=false pins the
// bypass branch off (webhook constructor); =true lets the snapshot
// decide per request.
func buildAuth(
	apiKey string,
	sessionKey []byte,
	ptr *AuthRuntimePointer,
	adminRepo ports.AdminUserRepository,
	loginLimiter *auth.IPLimiter,
	localBypassAllowed bool,
) gin.HandlerFunc {
	rawKeyBytes := []byte(apiKey)
	return func(c *gin.Context) {
		rt := loadAuthRuntime(ptr)

		// Step 1: X-Api-Key first — automation must never be blocked
		// by mode OR silently collapsed to "local" by step 2.
		if apiKey != "" {
			got := c.GetHeader("X-Api-Key")
			if got != "" && subtle.ConstantTimeCompare([]byte(got), rawKeyBytes) == 1 {
				c.Set(UsernameContextKey, "api-key")
				c.Next()
				return
			}
		}

		// Step 2: local-bypass (non-webhook routes only).
		if localBypassAllowed && rt.LocalBypass && len(rt.LocalNetworks) > 0 {
			if ip := net.ParseIP(c.ClientIP()); ip != nil {
				if IsLocalAddress(ip, rt.LocalNetworks) {
					c.Set(UsernameContextKey, "local")
					c.Next()
					return
				}
			}
		}

		// Step 3: mode dispatch.
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
		case runtime.AuthModeOIDC:
			// OIDC and forms share session-cookie validation. The OIDC-
			// specific work happens in the /auth/oidc/start +
			// /auth/oidc/callback handlers; once a cookie is minted, the
			// gate is identical to forms.
			if cookie, err := c.Cookie(SessionCookieName); err == nil && cookie != "" {
				if p, verr := VerifySession(sessionKey, cookie, time.Now(), rt.SessionEpoch); verr == nil {
					c.Set(UsernameContextKey, p.Username)
					c.Next()
					return
				}
			}
		default:
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

// handleBasicAuth runs the Basic-mode credential check. Split out so
// buildAuth stays readable.
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
