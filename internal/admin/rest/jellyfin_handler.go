package rest

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	auth "github.com/alexmorbo/seasonfill/internal/admin/app"
	jellyfin "github.com/alexmorbo/seasonfill/internal/shared/clients/jellyfin"
	"github.com/alexmorbo/seasonfill/internal/shared/http/dto"
	"github.com/alexmorbo/seasonfill/internal/shared/http/middleware"
)

// jellyfinLoginBodyLimit mirrors loginBodyLimit (4 KiB).
const jellyfinLoginBodyLimit = 4 << 10

// JellyfinHandler serves POST /api/v1/auth/jellyfin/login — a PUBLIC route
// (registered before the guarded group, like /auth/login and /auth/oidc/*).
// The Jellyfin base URL is read from the live AuthRuntime per request; the
// jellyfin.Client is constructed per request from it (cheap). Empty base URL
// => 503 JELLYFIN_NOT_CONFIGURED (mirrors OIDC Start's OIDC_NOT_CONFIGURED).
type JellyfinHandler struct {
	uc           *auth.JellyfinLoginUseCase
	authRuntime  *middleware.AuthRuntimePointer
	httpClient   *http.Client
	sessionKey   []byte
	sessionTTL   time.Duration
	secureCookie bool
	logger       *slog.Logger
	now          func() time.Time
}

func NewJellyfinHandler(
	uc *auth.JellyfinLoginUseCase,
	rt *middleware.AuthRuntimePointer,
	sessionKey []byte,
	sessionTTL time.Duration,
	secureCookie bool,
	logger *slog.Logger,
) *JellyfinHandler {
	if logger == nil {
		logger = slog.Default()
	}
	return &JellyfinHandler{
		uc: uc, authRuntime: rt, sessionKey: sessionKey,
		sessionTTL: sessionTTL, secureCookie: secureCookie,
		httpClient: &http.Client{Timeout: 15 * time.Second},
		logger:     logger, now: time.Now,
	}
}

// Login is POST /api/v1/auth/jellyfin/login.
//
// @Summary     Authenticate against Jellyfin and issue a session cookie
// @Description Validates username + password against the configured Jellyfin
// @Description server. Lazily provisions a role=user requester on first login.
// @Tags        auth
// @Accept      json
// @Produce     json
// @Param       body  body      dto.LoginRequest  true  "Jellyfin username and password"
// @Success     200   {object}  dto.OKResponse
// @Failure     400   {object}  dto.ErrorResponse
// @Failure     401   {object}  dto.ErrorResponse
// @Failure     500   {object}  dto.ErrorResponse
// @Failure     503   {object}  dto.ErrorResponse
// @Header      200   {string}  Set-Cookie  "HttpOnly session cookie"
// @Router      /auth/jellyfin/login [post]
func (h *JellyfinHandler) Login(c *gin.Context) {
	baseURL := ""
	if v := h.authRuntime.Load(); v != nil {
		baseURL = v.Jellyfin.BaseURL
	}
	if baseURL == "" {
		c.AbortWithStatusJSON(http.StatusServiceUnavailable, gin.H{
			"error": "jellyfin not configured", "code": "JELLYFIN_NOT_CONFIGURED",
		})
		return
	}

	username, password, ok := h.readBody(c)
	if !ok {
		return
	}

	client := jellyfin.New(baseURL, h.httpClient)
	user, err := h.uc.Login(c.Request.Context(), client, username, password)
	if err != nil {
		if errors.Is(err, auth.ErrJellyfinLoginFailed) {
			h.logger.WarnContext(c.Request.Context(), "jellyfin.login.failed",
				slog.String("ip", c.ClientIP()))
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
				"error": "Invalid credentials", "code": "UNAUTHORIZED",
			})
			return
		}
		h.logger.ErrorContext(c.Request.Context(), "jellyfin.login.error",
			slog.String("error", err.Error()))
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{
			"error": "internal server error",
		})
		return
	}

	epoch := int64(0)
	if v := h.authRuntime.Load(); v != nil {
		epoch = v.SessionEpoch
	}
	exp := h.now().Add(h.sessionTTL)
	tok, err := middleware.SignSession(h.sessionKey, user.Username, exp, epoch)
	if err != nil {
		h.logger.ErrorContext(c.Request.Context(), "jellyfin.login.sign_failed",
			slog.String("error", err.Error()))
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{
			"error": "internal server error",
		})
		return
	}
	c.SetSameSite(http.SameSiteStrictMode)
	c.SetCookie(middleware.SessionCookieName, tok,
		int(h.sessionTTL.Seconds()), "/", "", h.secureCookie, true)
	h.logger.InfoContext(c.Request.Context(), "jellyfin.login.success",
		slog.String("username", user.Username))
	c.JSON(http.StatusOK, gin.H{"ok": true, "username": user.Username})
}

func (h *JellyfinHandler) readBody(c *gin.Context) (string, string, bool) {
	ct := c.GetHeader("Content-Type")
	if !strings.HasPrefix(ct, jsonPrefix) {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{
			"error": "content-type must be application/json",
		})
		return "", "", false
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, jellyfinLoginBodyLimit)
	raw, err := io.ReadAll(c.Request.Body)
	if err != nil {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": "cannot read body"})
		return "", "", false
	}
	var body dto.LoginRequest
	if err := json.Unmarshal(raw, &body); err != nil {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": "malformed body"})
		return "", "", false
	}
	if body.Username == "" || body.Password == "" {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": "username and password required"})
		return "", "", false
	}
	return body.Username, body.Password, true
}
