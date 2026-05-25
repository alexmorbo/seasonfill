package handlers

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/alexmorbo/seasonfill/application/auth"
	"github.com/alexmorbo/seasonfill/application/ports"
	"github.com/alexmorbo/seasonfill/interface/http/dto"
	"github.com/alexmorbo/seasonfill/interface/http/middleware"
)

const (
	loginBodyLimit = 4 << 10
	jsonPrefix     = "application/json"
)

// AuthHandler — D48 handler.
type AuthHandler struct {
	apiKey          string
	repo            ports.AdminUserRepository
	authRuntime     *middleware.AuthRuntimePointer
	limiter         *auth.IPLimiter
	passwordLimiter *auth.IPLimiter
	logger          *slog.Logger
	now             func() time.Time
}

type AuthOption func(*AuthHandler)

func WithClock(now func() time.Time) AuthOption {
	return func(h *AuthHandler) {
		if now != nil {
			h.now = now
		}
	}
}

// WithPasswordLimiter installs a per-IP rate limit on PasswordChange.
// M1: a stolen session cookie could otherwise brute-force the current
// password unthrottled — bcrypt-cost-12 burns CPU but doesn't bound
// attempt count.
func WithPasswordLimiter(lim *auth.IPLimiter) AuthOption {
	return func(h *AuthHandler) { h.passwordLimiter = lim }
}

// NewAuthHandler — panics on empty apiKey or nil repo.
func NewAuthHandler(
	apiKey string, repo ports.AdminUserRepository, sessionTTL time.Duration,
	secureCookie bool, limiter *auth.IPLimiter, logger *slog.Logger, opts ...AuthOption,
) *AuthHandler {
	if apiKey == "" {
		panic("handlers.NewAuthHandler: apiKey must not be empty")
	}
	if repo == nil {
		panic("handlers.NewAuthHandler: repo must not be nil")
	}
	if sessionTTL <= 0 {
		sessionTTL = 12 * time.Hour
	}
	if logger == nil {
		logger = slog.Default()
	}
	ptr := &middleware.AuthRuntimePointer{}
	ptr.Store(&middleware.AuthRuntime{
		SessionTTL:   sessionTTL,
		SecureCookie: secureCookie,
	})
	h := &AuthHandler{
		apiKey: apiKey, repo: repo, authRuntime: ptr,
		limiter: limiter, logger: logger, now: time.Now,
	}
	for _, opt := range opts {
		opt(h)
	}
	return h
}

// WithAuthRuntimePointer wires a SHARED atomic — the reload
// subscriber stores into this same pointer. When not supplied,
// the handler owns a private pointer seeded from the sessionTTL
// argument (used by every test that builds an AuthHandler in
// isolation).
func WithAuthRuntimePointer(ptr *middleware.AuthRuntimePointer) AuthOption {
	return func(h *AuthHandler) {
		if ptr != nil {
			h.authRuntime = ptr
		}
	}
}

// AuthRuntime returns the atomic so cmd/server (and the reload
// subscriber) can store fresh values into it.
func (h *AuthHandler) AuthRuntime() *middleware.AuthRuntimePointer {
	return h.authRuntime
}

// sessionTTL reads the current TTL via the atomic. Used by Login
// + Logout (cookie max-age).
func (h *AuthHandler) sessionTTL() time.Duration {
	if v := h.authRuntime.Load(); v != nil {
		return v.SessionTTL
	}
	return 12 * time.Hour
}

// secureCookieFlag reads the current SecureCookie flag from the atomic and
// OR-combines with the per-request TLS detection. Returning true here makes
// the cookie Secure; we NEVER downgrade a TLS request.
func (h *AuthHandler) secureCookieFlag(c *gin.Context) bool {
	flag := false
	if v := h.authRuntime.Load(); v != nil {
		flag = v.SecureCookie
	}
	return flag || requestIsTLS(c.Request)
}

// Login is POST /api/v1/auth/login.
//
// @Summary     Authenticate and issue a session cookie
// @Description Validates username + password. On success sets HttpOnly
// @Description seasonfill_session cookie and returns 200.
// @Tags        auth
// @Accept      json
// @Produce     json
// @Param       body  body      dto.LoginRequest  true  "Username and password"
// @Success     200   {object}  dto.OKResponse
// @Failure     400   {object}  dto.ErrorResponse
// @Failure     401   {object}  dto.ErrorResponse
// @Failure     429   {object}  dto.ErrorResponse
// @Header      200   {string}  Set-Cookie  "HttpOnly session cookie"
// @Router      /auth/login [post]
func (h *AuthHandler) Login(c *gin.Context) {
	ip := c.ClientIP()
	if h.limiter != nil && !h.limiter.Allow(ip) {
		h.logger.WarnContext(c.Request.Context(), "auth.login.rate_limited",
			slog.String("ip", ip))
		c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{
			"error": "Invalid credentials", "code": "UNAUTHORIZED",
		})
		return
	}

	username, password, ok := h.readLoginBody(c)
	if !ok {
		return
	}

	user, err := h.repo.Get(c.Request.Context())
	if err != nil && !errors.Is(err, ports.ErrNotFound) {
		h.logger.ErrorContext(c.Request.Context(), "auth.login.repo_failed",
			slog.String("error", err.Error()))
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": "internal server error"})
		return
	}

	usernameMatches := err == nil && user.Username == username
	hashToCompare := user.PasswordHash
	if !usernameMatches {
		hashToCompare = ""
	}
	if !auth.ConstantLatencyVerify(hashToCompare, password) {
		h.logger.WarnContext(c.Request.Context(), "auth.login.failed",
			slog.String("ip", ip))
		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
			"error": "Invalid credentials", "code": "UNAUTHORIZED",
		})
		return
	}

	ttl := h.sessionTTL()
	exp := h.now().Add(ttl)
	tok, err := middleware.SignSession([]byte(h.apiKey), user.Username, exp)
	if err != nil {
		h.logger.ErrorContext(c.Request.Context(), "auth.login.sign_failed",
			slog.String("error", err.Error()))
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": "internal server error"})
		return
	}

	c.SetSameSite(http.SameSiteStrictMode)
	c.SetCookie(middleware.SessionCookieName, tok,
		int(ttl.Seconds()), "/", "",
		h.secureCookieFlag(c), true)
	h.logger.InfoContext(c.Request.Context(), "auth.login.success",
		slog.String("username", user.Username))
	c.JSON(http.StatusOK, gin.H{
		"ok": true, "username": user.Username, "auto_generated": user.AutoGenerated,
	})
}

func (h *AuthHandler) readLoginBody(c *gin.Context) (string, string, bool) {
	ct := c.GetHeader("Content-Type")
	if !strings.HasPrefix(ct, jsonPrefix) {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{
			"error": "content-type must be application/json",
		})
		return "", "", false
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, loginBodyLimit)
	raw, err := io.ReadAll(c.Request.Body)
	if err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": "payload too large"})
			return "", "", false
		}
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": "cannot read body"})
		return "", "", false
	}
	var body dto.LoginRequest
	if err := json.Unmarshal(raw, &body); err != nil {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": "malformed body"})
		return "", "", false
	}
	return body.Username, body.Password, true
}

// Logout clears the session cookie.
//
// @Summary     Clear the session cookie
// @Tags        auth
// @Produce     json
// @Success     204
// @Failure     401  {object}  dto.ErrorResponse
// @Security    CookieAuth
// @Security    ApiKeyAuth
// @Router      /auth/session [delete]
func (h *AuthHandler) Logout(c *gin.Context) {
	c.SetSameSite(http.SameSiteStrictMode)
	c.SetCookie(middleware.SessionCookieName, "", -1, "/", "",
		h.secureCookieFlag(c), true)
	h.logger.InfoContext(c.Request.Context(), "auth.logout.success")
	c.Status(http.StatusNoContent)
}

// Session verifies the current session.
//
// @Summary     Verify the current session
// @Tags        auth
// @Produce     json
// @Success     200  {object}  dto.SessionResponse
// @Failure     401  {object}  dto.ErrorResponse
// @Security    CookieAuth
// @Security    ApiKeyAuth
// @Router      /auth/session [get]
func (h *AuthHandler) Session(c *gin.Context) {
	username := c.GetString(middleware.UsernameContextKey)
	autoGen := false
	if username != "" && username != "api-key" {
		if u, err := h.repo.Get(c.Request.Context()); err == nil {
			autoGen = u.AutoGenerated
		}
	}
	c.JSON(http.StatusOK, dto.SessionResponse{
		OK: true, Username: username, AutoGenerated: autoGen,
	})
}

// PasswordChange replaces the admin password.
//
// @Summary     Change the admin password
// @Tags        auth
// @Accept      json
// @Produce     json
// @Param       body  body      dto.PasswordChangeRequest  true  "Current + new password"
// @Success     204
// @Failure     400  {object}  dto.ErrorResponse
// @Failure     401  {object}  dto.ErrorResponse
// @Security    CookieAuth
// @Security    ApiKeyAuth
// @Router      /auth/password [post]
func (h *AuthHandler) PasswordChange(c *gin.Context) {
	// M1: throttle BEFORE bcrypt work to bound CPU on a cookie-thief
	// brute force. Same envelope as login (Invalid credentials / 429).
	if h.passwordLimiter != nil {
		ip := c.ClientIP()
		if !h.passwordLimiter.Allow(ip) {
			h.logger.WarnContext(c.Request.Context(), "auth.password_change.rate_limited",
				slog.String("ip", ip))
			c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{
				"error": "rate limit exceeded", "code": "RATE_LIMITED",
			})
			return
		}
	}
	ct := c.GetHeader("Content-Type")
	if !strings.HasPrefix(ct, jsonPrefix) {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{
			"error": "content-type must be application/json",
		})
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, loginBodyLimit)
	raw, err := io.ReadAll(c.Request.Body)
	if err != nil {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": "cannot read body"})
		return
	}
	var body dto.PasswordChangeRequest
	if err := json.Unmarshal(raw, &body); err != nil {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": "malformed body"})
		return
	}
	if len(body.New) < auth.MinPasswordLen {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{
			"error": "password too short (min 8 chars)",
		})
		return
	}
	user, err := h.repo.Get(c.Request.Context())
	if err != nil {
		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "Invalid credentials"})
		return
	}
	if !auth.VerifyPassword(user.PasswordHash, body.Current) {
		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "Invalid credentials"})
		return
	}
	// M4: same-as-current is a no-op — reject before we hash + write.
	// Check AFTER verifying current so we don't leak whether the new
	// password equals an arbitrary string.
	if body.New == body.Current {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{
			"error": "New password must differ from current", "code": "BAD_REQUEST",
		})
		return
	}
	newHash, err := auth.HashPassword(body.New)
	if err != nil {
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": "internal server error"})
		return
	}
	if err := h.repo.UpdatePassword(c.Request.Context(), newHash, false); err != nil {
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": "internal server error"})
		return
	}
	h.logger.InfoContext(c.Request.Context(), "auth.password_change.success",
		slog.String("username", user.Username))
	c.Status(http.StatusNoContent)
}

func requestIsTLS(r *http.Request) bool {
	if r.TLS != nil {
		return true
	}
	return strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https")
}
