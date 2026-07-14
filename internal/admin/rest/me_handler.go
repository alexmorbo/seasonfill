package rest

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"regexp"
	"strings"

	"github.com/gin-gonic/gin"

	authapp "github.com/alexmorbo/seasonfill/internal/admin/app"
	admin "github.com/alexmorbo/seasonfill/internal/admin/domain"
	ports "github.com/alexmorbo/seasonfill/internal/shared/dataports"
	"github.com/alexmorbo/seasonfill/internal/shared/http/dto"
	"github.com/alexmorbo/seasonfill/internal/shared/http/middleware"
)

const (
	meBodyLimit = 4 << 10 // 4 KiB — same as login/password body limit.
)

// preferredLanguageRe enforces the BCP-47 subset documented in PRD §N-7
// (`^[a-z]{2}(-[A-Z]{2})?$`).
var preferredLanguageRe = regexp.MustCompile(`^[a-z]{2}(-[A-Z]{2})?$`)

// MeHandler owns GET /api/v1/me, PATCH /api/v1/me/settings, and
// POST /api/v1/me/change-password. Story 485 (N-7a).
//
// The auth runtime pointer is the SHARED atomic the reload subscriber
// publishes into — never cache the loaded value across requests.
type MeHandler struct {
	uc          *authapp.MeUseCase
	authRuntime *middleware.AuthRuntimePointer
	logger      *slog.Logger
}

// NewMeHandler panics on nil dependencies (init-time bug).
func NewMeHandler(uc *authapp.MeUseCase, authRuntime *middleware.AuthRuntimePointer, logger *slog.Logger) *MeHandler {
	if uc == nil {
		panic("rest.NewMeHandler: uc must not be nil")
	}
	if authRuntime == nil {
		panic("rest.NewMeHandler: authRuntime must not be nil")
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &MeHandler{uc: uc, authRuntime: authRuntime, logger: logger}
}

// Get is GET /api/v1/me.
//
// @Summary     Current user profile + auth context + avatar fields
// @Tags        auth
// @Produce     json
// @Success     200  {object}  dto.MeResponse
// @Failure     401  {object}  dto.ErrorResponse
// @Security    CookieAuth
// @Security    ApiKeyAuth
// @Router      /me [get]
func (h *MeHandler) Get(c *gin.Context) {
	user, ok := h.resolveUser(c)
	if !ok {
		return
	}
	c.JSON(http.StatusOK, h.buildResponse(user))
}

// UpdateSettings is PATCH /api/v1/me/settings.
//
// @Summary     Patch the current user's settings (avatar_mode + preferred_language)
// @Tags        auth
// @Accept      json
// @Produce     json
// @Param       body  body      dto.MeSettingsPatchRequest  true  "Partial settings patch"
// @Success     200   {object}  dto.MeResponse
// @Failure     400   {object}  dto.ErrorResponse
// @Failure     401   {object}  dto.ErrorResponse
// @Security    CookieAuth
// @Security    ApiKeyAuth
// @Router      /me/settings [patch]
func (h *MeHandler) UpdateSettings(c *gin.Context) {
	user, ok := h.resolveUser(c)
	if !ok {
		return
	}
	ct := c.GetHeader("Content-Type")
	if !strings.HasPrefix(ct, jsonPrefix) {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{
			"error": "content-type must be application/json",
		})
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, meBodyLimit)
	dec := json.NewDecoder(c.Request.Body)
	dec.DisallowUnknownFields()
	var body dto.MeSettingsPatchRequest
	if err := dec.Decode(&body); err != nil {
		if errors.Is(err, io.EOF) {
			// Empty body: treat as no-op patch. Re-fetch + return unchanged shape.
			c.JSON(http.StatusOK, h.buildResponse(user))
			return
		}
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": "malformed body"})
		return
	}

	if body.AvatarMode != nil {
		if !isAllowedAvatarMode(*body.AvatarMode) {
			c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{
				"error": "avatar_mode must be one of: auto, monogram, gravatar",
			})
			return
		}
	}
	if body.PreferredLanguage != nil {
		if !preferredLanguageRe.MatchString(*body.PreferredLanguage) {
			c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{
				"error": "preferred_language must match BCP-47 subset ^[a-z]{2}(-[A-Z]{2})?$",
			})
			return
		}
	}

	if err := h.uc.UpdateSettings(c.Request.Context(), user.ID, ports.UserSettingsPatch{
		AvatarMode:        body.AvatarMode,
		PreferredLanguage: body.PreferredLanguage,
	}); err != nil {
		h.logger.ErrorContext(c.Request.Context(), "me.update_settings.failed",
			slog.String("username", user.Username),
			slog.String("error", err.Error()))
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": "internal server error"})
		return
	}
	// Re-fetch so the response reflects the post-write state including
	// updated_at + any server-defaulted column the patch didn't touch.
	refreshed, err := h.uc.GetByUsername(c.Request.Context(), user.Username)
	if err != nil {
		h.logger.ErrorContext(c.Request.Context(), "me.update_settings.refetch_failed",
			slog.String("username", user.Username),
			slog.String("error", err.Error()))
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": "internal server error"})
		return
	}
	c.JSON(http.StatusOK, h.buildResponse(refreshed))
}

// ChangePassword is POST /api/v1/me/change-password.
//
// @Summary     Change the current user's password (not available to OIDC users)
// @Tags        auth
// @Accept      json
// @Produce     json
// @Param       body  body      dto.MeChangePasswordRequest  true  "Current + new password"
// @Success     204
// @Failure     400  {object}  dto.ErrorResponse
// @Failure     401  {object}  dto.ErrorResponse
// @Failure     405  {object}  dto.MePasswordUnavailableResponse
// @Failure     429  {object}  dto.ErrorResponse
// @Security    CookieAuth
// @Security    ApiKeyAuth
// @Router      /me/change-password [post]
func (h *MeHandler) ChangePassword(c *gin.Context) {
	user, ok := h.resolveUser(c)
	if !ok {
		return
	}
	// Per-user gate FIRST — an OIDC-provisioned user has no local password
	// to change; reject before reading the body.
	if userAuthMode(user) != "forms" {
		reason, manageURL := passwordChangeUnavailable(user, h.authRuntime.Load())
		c.AbortWithStatusJSON(http.StatusMethodNotAllowed, dto.MePasswordUnavailableResponse{
			Error:     "password_change_unavailable",
			Reason:    reason,
			ManageURL: manageURL,
		})
		return
	}

	ct := c.GetHeader("Content-Type")
	if !strings.HasPrefix(ct, jsonPrefix) {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{
			"error": "content-type must be application/json",
		})
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, meBodyLimit)
	raw, err := io.ReadAll(c.Request.Body)
	if err != nil {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": "cannot read body"})
		return
	}
	var body dto.MeChangePasswordRequest
	if err := json.Unmarshal(raw, &body); err != nil {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": "malformed body"})
		return
	}

	switch err := h.uc.ChangePassword(c.Request.Context(), user, body.CurrentPassword, body.NewPassword); {
	case err == nil:
		h.logger.InfoContext(c.Request.Context(), "me.change_password.success",
			slog.String("username", user.Username))
		c.Status(http.StatusNoContent)
	case errors.Is(err, authapp.ErrInvalidCurrentPassword):
		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
			"error": "Invalid credentials", "code": "UNAUTHORIZED",
		})
	case errors.Is(err, authapp.ErrNewPasswordTooShort):
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{
			"error": "password too short (min 8 chars)",
		})
	case errors.Is(err, authapp.ErrNewPasswordSameAsCurrent):
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{
			"error": "New password must differ from current", "code": "BAD_REQUEST",
		})
	default:
		h.logger.ErrorContext(c.Request.Context(), "me.change_password.failed",
			slog.String("username", user.Username),
			slog.String("error", err.Error()))
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": "internal server error"})
	}
}

// resolveUser looks up the authenticated user via the username carried
// on the gin context by RequireAuthWithRuntime. Returns false (and writes
// a 401 envelope) when no row matches — defensive: the middleware
// already 401'd if the session was invalid, so this branch only fires on
// a delete-after-login race or the X-Api-Key principal ("api-key"), which
// has no stored user row.
func (h *MeHandler) resolveUser(c *gin.Context) (admin.User, bool) {
	username := c.GetString(middleware.UsernameContextKey)
	if username == "" || username == "api-key" {
		// The X-Api-Key principal does NOT correspond to a stored user row.
		// /me is undefined for it — 401 with a clear message so the SPA can
		// render "log in to see your profile" rather than rendering an Object.
		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
			"error": "no user identity for this session", "code": "UNAUTHORIZED",
		})
		return admin.User{}, false
	}
	user, err := h.uc.GetByUsername(c.Request.Context(), username)
	if err != nil {
		if errors.Is(err, ports.ErrNotFound) {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
				"error": "user no longer exists", "code": "UNAUTHORIZED",
			})
			return admin.User{}, false
		}
		h.logger.ErrorContext(c.Request.Context(), "me.resolve_user.failed",
			slog.String("username", username),
			slog.String("error", err.Error()))
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": "internal server error"})
		return admin.User{}, false
	}
	return user, true
}

// userAuthMode derives the per-user auth mode from the stored row. An
// OIDC-provisioned user (has an oidc_subject, or carries no local password
// hash) is "oidc"; everyone else is "forms".
func userAuthMode(u admin.User) string {
	if u.OIDCSubject != nil || u.PasswordHash == "" {
		return "oidc"
	}
	return "forms"
}

// buildResponse renders the canonical /me payload for the user row, with
// auth_mode + avatar_resolved_mode + idp_profile_url derived from the
// user row and the live AuthRuntime issuer.
func (h *MeHandler) buildResponse(user admin.User) dto.MeResponse {
	mode := userAuthMode(user)
	var issuer string
	if rt := h.authRuntime.Load(); rt != nil {
		issuer = strings.TrimRight(rt.OIDC.Issuer, "/")
	}
	resp := dto.MeResponse{
		ID:                 user.ID,
		Username:           user.Username,
		Email:              user.Email,
		Role:               user.Role,
		AuthMode:           mode,
		AvatarMode:         user.AvatarMode,
		AvatarResolvedMode: resolveAvatarMode(user),
		AvatarHash:         deriveAvatarHash(user),
		PreferredLanguage:  user.PreferredLanguage,
		OIDCSubject:        user.OIDCSubject,
		LastLoginAt:        user.LastLoginAt,
	}
	if mode == "oidc" && user.OIDCSubject != nil && issuer != "" {
		profile := issuer + "/account"
		resp.IDPProfileURL = &profile
	}
	return resp
}

// resolveAvatarMode applies the operator-frozen rules:
//   - "auto": email present → "gravatar", absent → "monogram"
//   - "gravatar" + no email: silent fallback to "monogram"
//   - "gravatar" + email: pass-through
//   - "monogram": pass-through
func resolveAvatarMode(user admin.User) string {
	hasEmail := user.Email != nil && strings.TrimSpace(*user.Email) != ""
	switch user.AvatarMode {
	case admin.AvatarModeAuto:
		if hasEmail {
			return admin.AvatarModeGravatar
		}
		return admin.AvatarModeMonogram
	case admin.AvatarModeGravatar:
		if !hasEmail {
			return admin.AvatarModeMonogram
		}
		return admin.AvatarModeGravatar
	default:
		return admin.AvatarModeMonogram
	}
}

// deriveAvatarHash returns the canonical md5 hash for the user's email,
// or "" when no email is stored.
func deriveAvatarHash(user admin.User) string {
	if user.Email == nil {
		return ""
	}
	return admin.ComputeAvatarHash(*user.Email)
}

// isAllowedAvatarMode enforces the canonical allowlist; "custom" is
// rejected at this gate even if a stale FE sends it.
func isAllowedAvatarMode(s string) bool {
	switch s {
	case admin.AvatarModeAuto, admin.AvatarModeMonogram, admin.AvatarModeGravatar:
		return true
	}
	return false
}

// passwordChangeUnavailable builds the 405 envelope's `reason` +
// `manage_url` for an OIDC-provisioned user. The deep-link to the IdP
// account page is added when the user carries an oidc_subject and the
// live runtime exposes an issuer.
func passwordChangeUnavailable(user admin.User, rt *middleware.AuthRuntime) (string, *string) {
	var manageURL *string
	if user.OIDCSubject != nil && rt != nil {
		if issuer := strings.TrimRight(rt.OIDC.Issuer, "/"); issuer != "" {
			profile := issuer + "/account"
			manageURL = &profile
		}
	}
	return "managed_by_idp", manageURL
}
