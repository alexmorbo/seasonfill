package middleware

import (
	"context"
	"crypto/subtle"
	"errors"
	"net"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	auth "github.com/alexmorbo/seasonfill/internal/admin/app"
	"github.com/alexmorbo/seasonfill/internal/observability"
	"github.com/alexmorbo/seasonfill/internal/runtime"
	ports "github.com/alexmorbo/seasonfill/internal/shared/dataports"
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
	return buildAuth(apiKey, sessionKey, ptr, nil, nil, true, true)
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
	adminRepo ports.UserRepository,
	loginLimiter *auth.IPLimiter,
) gin.HandlerFunc {
	return buildAuth(apiKey, sessionKey, ptr, adminRepo, loginLimiter, true, true)
}

// RequireAuthWebhook is the webhook-route variant. IDENTICAL to
// RequireAuthWithRuntime except step 2 (local-bypass) is unconditionally
// skipped. Webhook ALWAYS requires X-Api-Key — invariant D-3 / AC-8.
// Cookie precheck is also disabled (cookieAllowed=false): webhook must
// never be authenticated via a session cookie.
func RequireAuthWebhook(
	apiKey string,
	sessionKey []byte,
	ptr *AuthRuntimePointer,
	adminRepo ports.UserRepository,
	loginLimiter *auth.IPLimiter,
) gin.HandlerFunc {
	return buildAuth(apiKey, sessionKey, ptr, adminRepo, loginLimiter, false, false)
}

// buildAuth is the shared pipeline. localBypassAllowed=false pins the
// bypass branch off (webhook constructor); =true lets the snapshot
// decide per request. cookieAllowed=false disables the basic-mode cookie
// precheck (webhook path must be X-Api-Key only).
func buildAuth(
	apiKey string,
	sessionKey []byte,
	ptr *AuthRuntimePointer,
	adminRepo ports.UserRepository,
	loginLimiter *auth.IPLimiter,
	localBypassAllowed bool,
	cookieAllowed bool,
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
			// 037e: accept a valid session cookie BEFORE issuing the Basic
			// challenge. Lets an OIDC-issued cookie continue working under
			// mode=basic without prompting the browser popup.
			// cookieAllowed is false on the webhook path — webhook must
			// always require X-Api-Key.
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
			if adminRepo == nil {
				break
			}
			handleBasicAuth(c, adminRepo, loginLimiter)
			return
		case runtime.AuthModeOIDC, runtime.AuthModeForms:
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
		default:
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

// handleBasicAuth runs the Basic-mode credential check. Split out so
// buildAuth stays readable.
func handleBasicAuth(c *gin.Context, repo ports.UserRepository, lim *auth.IPLimiter) {
	header := c.GetHeader("Authorization")
	user, pass, ok := parseBasicHeader(header)
	if !ok {
		observability.AuthLogin(observability.AuthLoginModeBasic, observability.AuthLoginFailure)
		c.Header("WWW-Authenticate", basicRealmHeader)
		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
			"error": "unauthorized", "code": "AUTH_REQUIRED",
		})
		return
	}

	if lim != nil && !lim.Allow(c.ClientIP()) {
		observability.AuthLogin(observability.AuthLoginModeBasic, observability.AuthLoginRateLimited)
		c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{
			"error": "Invalid credentials", "code": "AUTH_REQUIRED",
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
		observability.AuthLogin(observability.AuthLoginModeBasic, observability.AuthLoginFailure)
		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
			"error": "unauthorized", "code": "AUTH_REQUIRED",
		})
		return
	}

	// Basic auth is per-request (credentials resent on every call), so there is
	// no discrete "login" event to count here — ticking auth_login_total on the
	// success path would count authenticated requests, not logins, and dwarf
	// forms/oidc login counts on the shared panel. The failure / rate_limited
	// ticks above are retained: each fires only on a rejected request and is a
	// genuine failed-login signal.
	c.Set(UsernameContextKey, row.Username)
	c.Next()
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
		return &AuthRuntime{Mode: runtime.AuthModeForms}
	}
	v := ptr.Load()
	if v == nil {
		return &AuthRuntime{Mode: runtime.AuthModeForms}
	}
	return v
}
