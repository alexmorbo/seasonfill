package handlers

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/alexmorbo/seasonfill/application/ports"
	"github.com/alexmorbo/seasonfill/application/regrab"
	"github.com/alexmorbo/seasonfill/interface/http/dto"
	"github.com/alexmorbo/seasonfill/interface/http/middleware"
)

// QbitSettingsHandler exposes the per-instance Watchdog settings
// surface. Mounted under the existing guarded group in server.go
// (same auth middleware as `/api/v1/instances/:name`).
type QbitSettingsHandler struct {
	uc     *regrab.SettingsUseCase
	logger *slog.Logger
}

func NewQbitSettingsHandler(uc *regrab.SettingsUseCase, logger *slog.Logger) *QbitSettingsHandler {
	if logger == nil {
		logger = slog.Default()
	}
	return &QbitSettingsHandler{uc: uc, logger: logger}
}

// Get returns the masked settings row.
//
// @Summary     Get qBit Watchdog settings for an instance
// @Tags        qbit-settings
// @Produce     json
// @Param       name  path      string  true  "Instance name"
// @Success     200   {object}  dto.QbitSettingsDTO
// @Failure     401   {object}  dto.ErrorResponse
// @Failure     404   {object}  dto.ErrorResponse
// @Security    CookieAuth
// @Security    ApiKeyAuth
// @Router      /instances/{name}/qbit/settings [get]
func (h *QbitSettingsHandler) Get(c *gin.Context) {
	name := c.Param("name")
	view, err := h.uc.GetByInstanceName(c.Request.Context(), name)
	if err != nil {
		h.writeError(c, err)
		return
	}
	c.JSON(http.StatusOK, viewToDTO(view))
}

// Upsert creates or replaces the settings row (idempotent).
//
// @Summary     Create or replace qBit Watchdog settings (upsert)
// @Tags        qbit-settings
// @Accept      json
// @Produce     json
// @Param       name  path      string                          true  "Instance name"
// @Param       body  body      dto.QbitSettingsUpsertRequest   true  "Settings"
// @Success     200   {object}  dto.QbitSettingsDTO
// @Failure     400   {object}  dto.ErrorResponse
// @Failure     401   {object}  dto.ErrorResponse
// @Failure     404   {object}  dto.ErrorResponse
// @Failure     409   {object}  dto.ErrorResponse
// @Security    CookieAuth
// @Security    ApiKeyAuth
// @Router      /instances/{name}/qbit/settings [put]
func (h *QbitSettingsHandler) Upsert(c *gin.Context) {
	name := c.Param("name")
	var req dto.QbitSettingsUpsertRequest
	if !middleware.BindAndValidateJSON(c, &req) {
		return
	}
	in := regrab.UpsertInput{
		Enabled:                req.Enabled,
		URL:                    req.URL,
		Username:               req.Username,
		Password:               req.Password,
		Category:               req.Category,
		PollIntervalMinutes:    req.PollIntervalMinutes,
		RegrabCooldownHours:    req.RegrabCooldownHours,
		MaxConsecutiveNoBetter: req.MaxConsecutiveNoBetter,
		CustomUnregisteredMsgs: req.CustomUnregisteredMsgs,
		PublicURL:              req.QbitPublicURL,
	}
	view, err := h.uc.Upsert(c.Request.Context(), name, in)
	if err != nil {
		h.writeError(c, err)
		return
	}
	c.JSON(http.StatusOK, viewToDTO(view))
}

// Delete removes the settings row.
//
// @Summary     Delete qBit Watchdog settings
// @Tags        qbit-settings
// @Produce     json
// @Param       name  path      string  true  "Instance name"
// @Success     204
// @Failure     401   {object}  dto.ErrorResponse
// @Failure     404   {object}  dto.ErrorResponse
// @Security    CookieAuth
// @Security    ApiKeyAuth
// @Router      /instances/{name}/qbit/settings [delete]
func (h *QbitSettingsHandler) Delete(c *gin.Context) {
	name := c.Param("name")
	if err := h.uc.Delete(c.Request.Context(), name); err != nil {
		h.writeError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

// writeError is the wire-code dispatcher. The order matters: typed
// validation BEFORE generic ErrValidation; webhook gate BEFORE
// generic ErrNotFound mapping; instance-not-found vs
// settings-not-found distinguished by which error wraps
// ports.ErrNotFound (the use case includes the qualifier in the
// message via fmt.Errorf("instance %q: %w", ...) on the instance
// lookup path).
//
// Password plaintext is NEVER part of err.Error() — the use case
// is responsible for never wrapping plaintext into errors, and
// this handler does not log err.Error() at INFO level on validation
// or webhook-gate paths.
func (h *QbitSettingsHandler) writeError(c *gin.Context, err error) {
	var verr *regrab.ValidationError
	switch {
	case errors.As(err, &verr):
		c.AbortWithStatusJSON(http.StatusBadRequest, dto.ErrorResponse{
			Error: verr.Error(), Code: verr.Code,
		})
	case errors.Is(err, regrab.ErrValidation):
		c.AbortWithStatusJSON(http.StatusBadRequest, dto.ErrorResponse{
			Error: err.Error(), Code: "BAD_REQUEST",
		})
	case errors.Is(err, regrab.ErrWebhookNotInstalled):
		c.AbortWithStatusJSON(http.StatusConflict, dto.ErrorResponse{
			Error: "Configure OnGrab webhook before enabling watchdog. " +
				"Use POST /api/v1/instances/{name}/webhook/install.",
			Code: "WEBHOOK_NOT_INSTALLED",
		})
	case errors.Is(err, regrab.ErrWebhookCheckFailed):
		h.logger.WarnContext(c.Request.Context(),
			"qbit_settings.webhook_check_failed",
			slog.String("error", err.Error()))
		c.AbortWithStatusJSON(http.StatusBadGateway, dto.ErrorResponse{
			Error: "webhook installation check failed; retry shortly",
			Code:  "WEBHOOK_CHECK_FAILED",
		})
	case errors.Is(err, ports.ErrNotFound):
		// F-2c-1: dispatch through the typed-error middleware so the
		// wire code derives from the deepest typed sentinel in the
		// chain (instance_not_found vs qbit_settings_not_found). The
		// pre-F-2c string-prefix check is gone — F-2b repos join the
		// typed error with ports.ErrNotFound, the middleware picks the
		// right slug via errors.As. The body now reads
		// {"error":"instance_not_found","message":"<typed>"} or
		// {"error":"qbit_settings_not_found",...} per the F-2c-1
		// contract change (was SCREAMING_CASE Code field).
		_ = c.Error(err)
		c.Abort()
	default:
		h.logger.ErrorContext(c.Request.Context(),
			"qbit_settings.unhandled_error",
			slog.String("error", err.Error()))
		c.AbortWithStatusJSON(http.StatusInternalServerError, dto.ErrorResponse{
			Error: "internal server error",
		})
	}
}

func viewToDTO(v regrab.QbitSettingsView) dto.QbitSettingsDTO {
	msgs := v.CustomUnregisteredMsgs
	if msgs == nil {
		msgs = []string{}
	}
	return dto.QbitSettingsDTO{
		ID:                     v.ID,
		InstanceID:             v.InstanceID,
		InstanceName:           v.InstanceName,
		Enabled:                v.Enabled,
		URL:                    v.URL,
		QbitPublicURL:          v.PublicURL,
		Username:               v.Username,
		PasswordSet:            v.PasswordSet,
		Category:               v.Category,
		PollIntervalMinutes:    v.PollIntervalMinutes,
		RegrabCooldownHours:    v.RegrabCooldownHours,
		MaxConsecutiveNoBetter: v.MaxConsecutiveNoBetter,
		CustomUnregisteredMsgs: msgs,
		CreatedAt:              v.CreatedAt,
		UpdatedAt:              v.UpdatedAt,
	}
}
