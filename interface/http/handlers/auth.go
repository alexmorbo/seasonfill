package handlers

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/alexmorbo/seasonfill/interface/http/dto"
	"github.com/alexmorbo/seasonfill/interface/http/middleware"
)

// cookieMaxAgeSeconds matches middleware.VerifyCookie's TTL so
// the browser and server share one expiry contract.
const cookieMaxAgeSeconds = 30 * 24 * 60 * 60

// loginRequestBodyLimit caps the JSON payload at 4 KiB.
const loginRequestBodyLimit = 4 << 10

// jsonContentTypePrefix gates body parsing in Login. Non-JSON
// Content-Types (e.g. form POSTs — CORS-simple, no preflight)
// must not exercise the body branch.
const jsonContentTypePrefix = "application/json"

// AuthHandler serves the session bridge endpoints. Stateless;
// constructor signature is the interlock with 009a1's server.go.
type AuthHandler struct {
	apiKey       string
	cookieSecret []byte
	secureCookie bool
	logger       *slog.Logger
	now          func() time.Time
}

// AuthOption mirrors the functional-options pattern in
// application/webhook/usecase.go and application/grab/grab_usecase.go.
type AuthOption func(*AuthHandler)

// WithClock injects a clock for deterministic tests. Production
// callers omit it; default is time.Now.
func WithClock(now func() time.Time) AuthOption {
	return func(h *AuthHandler) {
		if now != nil {
			h.now = now
		}
	}
}

// NewAuthHandler builds the handler. `secureCookie` should be true
// in production (HTTPS); 009a1 wires it from cfg.Auth.Enabled.
// Panics on empty `apiKey` — server-misconfig must fail fast.
func NewAuthHandler(apiKey, cookieSecret string, secureCookie bool, logger *slog.Logger, opts ...AuthOption) *AuthHandler {
	if apiKey == "" {
		panic("handlers.NewAuthHandler: apiKey must not be empty")
	}
	if logger == nil {
		logger = slog.Default()
	}
	h := &AuthHandler{
		apiKey:       apiKey,
		cookieSecret: []byte(cookieSecret),
		secureCookie: secureCookie,
		logger:       logger,
		now:          time.Now,
	}
	for _, opt := range opts {
		opt(h)
	}
	return h
}

// Login is POST /api/v1/auth/login. Accepts the API key in the
// JSON body (only when Content-Type starts with application/json)
// or via X-Api-Key. Body wins when both are present and non-empty.
// 401 envelope is identical to middleware/auth.go so a probe
// cannot distinguish failure source.
//
// @Summary     Authenticate and issue a session cookie
// @Description Validates api_key (body or X-Api-Key header). On success
// @Description sets HttpOnly seasonfill_session cookie and returns 200.
// @Tags        auth
// @Accept      json
// @Produce     json
// @Param       body  body      dto.LoginRequest  false  "API key (alternative to X-Api-Key header)"
// @Success     200   {object}  dto.OKResponse
// @Failure     400   {object}  dto.ErrorResponse
// @Failure     401   {object}  dto.ErrorResponse
// @Header      200   {string}  Set-Cookie  "HttpOnly session cookie"
// @Router      /auth/login [post]
func (h *AuthHandler) Login(c *gin.Context) {
	candidate := ""

	ct := c.GetHeader("Content-Type")
	if strings.HasPrefix(ct, jsonContentTypePrefix) {
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, loginRequestBodyLimit)
		raw, err := io.ReadAll(c.Request.Body)
		if err != nil {
			var maxErr *http.MaxBytesError
			if errors.As(err, &maxErr) {
				h.respondInvalidKey(c)
				return
			}
			h.logger.ErrorContext(c.Request.Context(), "auth.login.body_read_failed",
				slog.String("error", err.Error()),
			)
			h.respondInvalidKey(c)
			return
		}
		if len(raw) > 0 {
			var body dto.LoginRequest
			if err := json.Unmarshal(raw, &body); err != nil {
				c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": "malformed body"})
				return
			}
			candidate = body.APIKey
		}
	}
	if candidate == "" {
		candidate = c.GetHeader("X-Api-Key")
	}

	// No early return on empty apiKey / candidate — let
	// subtle.ConstantTimeCompare handle length mismatches uniformly
	// so the timing profile is identical across all failure modes.
	if subtle.ConstantTimeCompare([]byte(candidate), []byte(h.apiKey)) != 1 {
		h.respondInvalidKey(c)
		return
	}

	token, err := middleware.SignCookie(h.cookieSecret, h.now())
	if err != nil {
		h.logger.ErrorContext(c.Request.Context(), "auth.login.sign_failed",
			slog.String("error", err.Error()),
		)
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": "internal server error"})
		return
	}

	c.SetSameSite(http.SameSiteStrictMode)
	c.SetCookie(
		middleware.SessionCookieName,
		token,
		cookieMaxAgeSeconds,
		"/",
		"", // domain — leave blank for host-only
		h.secureCookie,
		true, // HttpOnly
	)
	h.logger.InfoContext(c.Request.Context(), "auth.login.success")
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

// Logout is DELETE /api/v1/auth/session. 009a1 mounts this behind
// RequireAuth. Clears the session cookie with Max-Age=-1 and 204.
//
// @Summary     Clear the session cookie
// @Tags        auth
// @Produce     json
// @Success     204
// @Failure     401   {object}  dto.ErrorResponse
// @Security    CookieAuth
// @Security    ApiKeyAuth
// @Router      /auth/session [delete]
func (h *AuthHandler) Logout(c *gin.Context) {
	c.SetSameSite(http.SameSiteStrictMode)
	c.SetCookie(
		middleware.SessionCookieName,
		"",
		-1, // Max-Age=-1 → delete
		"/",
		"",
		h.secureCookie,
		true,
	)
	h.logger.InfoContext(c.Request.Context(), "auth.logout.success")
	c.Status(http.StatusNoContent)
}

// Session is GET /api/v1/auth/session — used by browser SPAs to verify
// the current session is still valid. 009a1 mounts this behind
// RequireAuth, so reaching the handler at all means auth succeeded.
//
// @Summary     Verify the current session
// @Tags        auth
// @Produce     json
// @Success     200   {object}  dto.OKResponse
// @Failure     401   {object}  dto.ErrorResponse
// @Security    CookieAuth
// @Security    ApiKeyAuth
// @Router      /auth/session [get]
func (h *AuthHandler) Session(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

// respondInvalidKey emits the project's standard 401 envelope —
// matches interface/http/middleware/auth.go:14-17.
func (h *AuthHandler) respondInvalidKey(c *gin.Context) {
	h.logger.WarnContext(c.Request.Context(), "auth.login.failed")
	c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
		"error": "unauthorized",
		"code":  "UNAUTHORIZED",
	})
}
