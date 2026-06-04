package handlers

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/alexmorbo/seasonfill/application/auth"
	"github.com/alexmorbo/seasonfill/interface/http/middleware"
)

const (
	oidcStateCookie    = "seasonfill_oidc_state"
	oidcNonceCookie    = "seasonfill_oidc_nonce"
	oidcVerifierCookie = "seasonfill_oidc_pkce"
	oidcReturnToCookie = "seasonfill_oidc_return_to"
	oidcCookieTTL      = 5 * time.Minute
)

// OIDCHandler serves GET /api/v1/auth/oidc/start and
// GET /api/v1/auth/oidc/callback. Both are PUBLIC routes (registered
// before the guarded group). The usecase enforces all OIDC invariants;
// the handler only translates HTTP <-> usecase types and manages the
// short-lived state/nonce/PKCE cookies.
type OIDCHandler struct {
	uc           *auth.OIDCLoginUseCase
	authRuntime  *middleware.AuthRuntimePointer
	sessionKey   []byte
	sessionTTL   time.Duration
	secureCookie bool
	logger       *slog.Logger
	now          func() time.Time
}

func NewOIDCHandler(
	uc *auth.OIDCLoginUseCase,
	rt *middleware.AuthRuntimePointer,
	sessionKey []byte,
	sessionTTL time.Duration,
	secureCookie bool,
	logger *slog.Logger,
) *OIDCHandler {
	if logger == nil {
		logger = slog.Default()
	}
	return &OIDCHandler{
		uc: uc, authRuntime: rt, sessionKey: sessionKey,
		sessionTTL: sessionTTL, secureCookie: secureCookie,
		logger: logger, now: time.Now,
	}
}

// Start kicks off the OIDC Authorization Code + PKCE flow.
//
// @Summary OIDC start
// @Tags    auth
// @Param   next  query  string  false  "same-origin path to return to after login"
// @Success 302
// @Router  /auth/oidc/start [get]
func (h *OIDCHandler) Start(c *gin.Context) {
	cfg := h.config()
	if cfg.Issuer == "" {
		c.AbortWithStatusJSON(http.StatusServiceUnavailable, gin.H{
			"error": "oidc not configured", "code": "OIDC_NOT_CONFIGURED",
		})
		return
	}
	info := auth.RequestInfo{
		Host: c.Request.Host,
		XFH:  c.GetHeader("X-Forwarded-Host"),
		XFP:  c.GetHeader("X-Forwarded-Proto"),
	}
	res, err := h.uc.Start(c.Request.Context(), cfg, info)
	if err != nil {
		code := "OIDC_DISCOVERY_FAILED"
		if err.Error() == "oidc: cannot derive redirect_url from request headers" {
			code = "OIDC_REDIRECT_DERIVE_FAILED"
			c.AbortWithStatusJSON(http.StatusBadGateway, gin.H{
				"error": "cannot derive redirect_url", "code": code,
			})
			return
		}
		h.logger.WarnContext(c.Request.Context(), "oidc.start_failed",
			slog.String("error", err.Error()))
		c.AbortWithStatusJSON(http.StatusBadGateway, gin.H{
			"error": "oidc provider unreachable", "code": code,
		})
		return
	}
	maxAge := int(oidcCookieTTL.Seconds())
	c.SetSameSite(http.SameSiteLaxMode)
	c.SetCookie(oidcStateCookie, res.State, maxAge, "/", "", h.secureCookie, true)
	c.SetCookie(oidcNonceCookie, res.Nonce, maxAge, "/", "", h.secureCookie, true)
	c.SetCookie(oidcVerifierCookie, res.PKCEVerifier, maxAge, "/", "", h.secureCookie, true)
	if next := auth.SafeReturnTo(c.Query("next")); next != "/" {
		c.SetCookie(oidcReturnToCookie, next, maxAge, "/", "", h.secureCookie, true)
	}
	c.Redirect(http.StatusFound, res.AuthURL)
}

// Callback consumes the state/nonce/PKCE cookies, runs the usecase,
// mints a session cookie on success, and 302s to the (validated)
// return-to path.
//
// @Summary OIDC callback
// @Tags    auth
// @Param   code   query  string  true   "authorization code"
// @Param   state  query  string  true   "echoed state"
// @Success 302
// @Router  /auth/oidc/callback [get]
func (h *OIDCHandler) Callback(c *gin.Context) {
	cfg := h.config()
	expectedState, _ := c.Cookie(oidcStateCookie)
	nonce, _ := c.Cookie(oidcNonceCookie)
	verifier, _ := c.Cookie(oidcVerifierCookie)
	returnTo, _ := c.Cookie(oidcReturnToCookie)

	// Clear cookies regardless of outcome to prevent replay.
	defer func() {
		c.SetSameSite(http.SameSiteLaxMode)
		c.SetCookie(oidcStateCookie, "", -1, "/", "", h.secureCookie, true)
		c.SetCookie(oidcNonceCookie, "", -1, "/", "", h.secureCookie, true)
		c.SetCookie(oidcVerifierCookie, "", -1, "/", "", h.secureCookie, true)
		c.SetCookie(oidcReturnToCookie, "", -1, "/", "", h.secureCookie, true)
	}()

	info := auth.RequestInfo{
		Host: c.Request.Host,
		XFH:  c.GetHeader("X-Forwarded-Host"),
		XFP:  c.GetHeader("X-Forwarded-Proto"),
	}
	in := auth.CallbackInput{
		Code:          c.Query("code"),
		State:         c.Query("state"),
		ExpectedState: expectedState,
		Nonce:         nonce,
		PKCEVerifier:  verifier,
	}
	res, err := h.uc.Callback(c.Request.Context(), cfg, info, in)
	if err != nil {
		h.logger.WarnContext(c.Request.Context(), "oidc.callback_failed",
			slog.String("error", err.Error()))
		status := http.StatusUnauthorized
		code := "UNAUTHORIZED"
		if err.Error() == auth.ErrOIDCGroupDenied.Error() {
			status = http.StatusForbidden
			code = "OIDC_GROUP_DENIED"
		}
		c.AbortWithStatusJSON(status, gin.H{
			"error": "oidc login failed", "code": code,
		})
		return
	}

	// Mint a 036-format session cookie with the current SessionEpoch.
	epoch := int64(0)
	if v := h.authRuntime.Load(); v != nil {
		epoch = v.SessionEpoch
	}
	exp := h.now().Add(h.sessionTTL)
	tok, err := middleware.SignSession(h.sessionKey, res.Username, exp, epoch)
	if err != nil {
		h.logger.ErrorContext(c.Request.Context(), "oidc.cookie_sign_failed",
			slog.String("error", err.Error()))
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{
			"error": "internal server error",
		})
		return
	}
	c.SetSameSite(http.SameSiteLaxMode)
	c.SetCookie(middleware.SessionCookieName, tok,
		int(h.sessionTTL.Seconds()), "/", "", h.secureCookie, true)

	if returnTo == "" {
		returnTo = "/"
	}
	c.Redirect(http.StatusFound, auth.SafeReturnTo(returnTo))
}

func (h *OIDCHandler) config() auth.OIDCConfig {
	v := h.authRuntime.Load()
	if v == nil {
		return auth.OIDCConfig{}
	}
	return auth.OIDCConfig{
		Issuer:        v.OIDC.Issuer,
		ClientID:      v.OIDC.ClientID,
		ClientSecret:  v.OIDC.ClientSecret,
		RedirectURL:   v.OIDC.RedirectURL,
		Scopes:        append([]string(nil), v.OIDC.Scopes...),
		UsernameClaim: v.OIDC.UsernameClaim,
		AllowedGroups: append([]string(nil), v.OIDC.AllowedGroups...),
		GroupsClaim:   v.OIDC.GroupsClaim,
	}
}
