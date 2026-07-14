package middleware

import (
	"crypto/subtle"
	"errors"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	auth "github.com/alexmorbo/seasonfill/internal/admin/app"
	"github.com/alexmorbo/seasonfill/internal/observability"
	ports "github.com/alexmorbo/seasonfill/internal/shared/dataports"
)

const UsernameContextKey = "auth.username"

// RequireAuthWithRuntime gates protected non-webhook routes. Dispatch order:
//
//  1. X-Api-Key check (precedence — automation must never be silently
//     attributed to another principal)
//  2. Session-cookie check
//
// adminRepo + loginLimiter are retained in the signature for call-site
// compatibility but are no longer consulted.
func RequireAuthWithRuntime(
	apiKey string,
	sessionKey []byte,
	ptr *AuthRuntimePointer,
	adminRepo ports.UserRepository,
	loginLimiter *auth.IPLimiter,
) gin.HandlerFunc {
	return buildAuth(apiKey, sessionKey, ptr, true)
}

// RequireAuthWebhook is the webhook-route variant. IDENTICAL to
// RequireAuthWithRuntime except the cookie precheck is disabled
// (cookieAllowed=false): webhook must never be authenticated via a
// session cookie. Webhook ALWAYS requires X-Api-Key — invariant D-3 / AC-8.
func RequireAuthWebhook(
	apiKey string,
	sessionKey []byte,
	ptr *AuthRuntimePointer,
	adminRepo ports.UserRepository,
	loginLimiter *auth.IPLimiter,
) gin.HandlerFunc {
	return buildAuth(apiKey, sessionKey, ptr, false)
}

// buildAuth is the shared pipeline. cookieAllowed=false disables the
// cookie precheck (webhook path must be X-Api-Key only).
func buildAuth(
	apiKey string,
	sessionKey []byte,
	ptr *AuthRuntimePointer,
	cookieAllowed bool,
) gin.HandlerFunc {
	rawKeyBytes := []byte(apiKey)
	return func(c *gin.Context) {
		rt := loadAuthRuntime(ptr)

		// Step 1: X-Api-Key first — automation must never be blocked.
		if apiKey != "" {
			got := c.GetHeader("X-Api-Key")
			if got != "" && subtle.ConstantTimeCompare([]byte(got), rawKeyBytes) == 1 {
				c.Set(UsernameContextKey, "api-key")
				c.Next()
				return
			}
		}

		// Step 2: session cookie (disabled on the webhook path).
		if cookieAllowed {
			if cookie, err := c.Cookie(SessionCookieName); err == nil && cookie != "" {
				now := time.Now()
				p, verr := VerifySession(sessionKey, cookie, now, rt.SessionEpoch)
				observability.AuthSessionValidation(sessionResultLabel(verr))
				if verr == nil {
					maybeSlideCookie(c, sessionKey, p, now, rt)
					c.Set(UsernameContextKey, p.Username)
					c.Next()
					return
				}
			}
		}

		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
			"error": "unauthorized", "code": "AUTH_REQUIRED",
		})
	}
}

// maybeSlideCookie re-issues the session cookie when the remaining lifetime
// drops below half the configured TTL. Only valid cookies reach this path.
// The threshold (TTL/2) is hard-coded — operators control session_ttl; the
// slide behaviour is an implementation detail. X-Api-Key paths return
// before this point, so an API key caller never gets a Set-Cookie header.
func maybeSlideCookie(
	c *gin.Context, sessionKey []byte, p SessionPayload,
	now time.Time, rt *AuthRuntime,
) {
	ttl := rt.SessionTTL
	if ttl <= 0 {
		return
	}
	remaining := time.Until(time.Unix(p.Exp, 0))
	if remaining <= 0 || remaining > ttl/2 {
		return
	}
	newExp := now.Add(ttl)
	tok, err := SignSession(sessionKey, p.Username, newExp, rt.SessionEpoch)
	if err != nil {
		return // best-effort; original cookie still valid
	}
	c.SetSameSite(http.SameSiteLaxMode)
	c.SetCookie(SessionCookieName, tok,
		int(ttl.Seconds()), "/", "", rt.SecureCookie, true)
}

// sessionResultLabel maps a VerifySession outcome to a bounded metric label for
// seasonfill_auth_session_validations_total. err==nil → "valid". The label is
// server-side observability only and is NEVER surfaced to the client, so this
// does not violate the "don't leak which sentinel rejected" rule on the 401.
func sessionResultLabel(err error) string {
	switch {
	case err == nil:
		return observability.AuthSessionValid
	case errors.Is(err, ErrSessionExpired):
		return observability.AuthSessionExpired
	case errors.Is(err, ErrSessionSignature):
		return observability.AuthSessionBadSignature
	case errors.Is(err, ErrSessionEpoch):
		return observability.AuthSessionStaleEpoch
	case errors.Is(err, ErrSessionMalformed):
		return observability.AuthSessionMalformed
	default:
		return observability.AuthSessionInvalid
	}
}

func loadAuthRuntime(ptr *AuthRuntimePointer) *AuthRuntime {
	if ptr == nil {
		return &AuthRuntime{}
	}
	v := ptr.Load()
	if v == nil {
		return &AuthRuntime{}
	}
	return v
}
