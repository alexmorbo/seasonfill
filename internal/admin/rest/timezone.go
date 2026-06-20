package rest

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/alexmorbo/seasonfill/interface/http/middleware"
	"github.com/alexmorbo/seasonfill/internal/runtime/tz"
	"github.com/alexmorbo/seasonfill/internal/shared/http/dto"
)

// TimezoneHandler exposes GET/PATCH /api/v1/settings/timezone.
// Mounts under the same guarded group as /external-services
// (handlers/external_services.go).
type TimezoneHandler struct {
	resolver *tz.Resolver
	logger   *slog.Logger
	// dirty flips true on the first successful PATCH. Surfaced
	// in GET responses so the frontend can show a "restart
	// required" hint — cron schedulers built at boot don't
	// reload their location on PATCH.
	dirty bool
}

func NewTimezoneHandler(resolver *tz.Resolver, logger *slog.Logger) *TimezoneHandler {
	if logger == nil {
		logger = slog.Default()
	}
	return &TimezoneHandler{resolver: resolver, logger: logger}
}

// Get returns the current timezone view.
//
// @Summary     Get current timezone setting
// @Tags        settings
// @Produce     json
// @Success     200  {object}  dto.TimezoneResponse
// @Failure     401  {object}  dto.ErrorResponse
// @Security    CookieAuth
// @Security    ApiKeyAuth
// @Router      /settings/timezone [get]
func (h *TimezoneHandler) Get(c *gin.Context) {
	c.JSON(http.StatusOK, dto.TimezoneResponse{
		Timezone:        h.resolver.Name(),
		Source:          string(h.resolver.Source()),
		RequiresRestart: h.dirty,
	})
}

// Patch updates the timezone (empty string clears the override).
//
// @Summary     Update timezone setting
// @Tags        settings
// @Accept      json
// @Produce     json
// @Param       body  body      dto.TimezonePatchRequest  true  "Timezone"
// @Success     200   {object}  dto.TimezoneResponse
// @Failure     400   {object}  dto.ErrorResponse
// @Failure     401   {object}  dto.ErrorResponse
// @Security    CookieAuth
// @Security    ApiKeyAuth
// @Router      /settings/timezone [patch]
func (h *TimezoneHandler) Patch(c *gin.Context) {
	var req dto.TimezonePatchRequest
	if !middleware.BindAndValidateJSON(c, &req) {
		return
	}
	if err := h.resolver.Set(c.Request.Context(), req.Timezone); err != nil {
		if errors.Is(err, tz.ErrInvalidTimezone) {
			c.AbortWithStatusJSON(http.StatusBadRequest, dto.ErrorResponse{
				Error: "invalid IANA timezone", Code: "INVALID_TIMEZONE",
			})
			return
		}
		h.logger.ErrorContext(c.Request.Context(),
			"timezone.patch_failed", slog.String("error", err.Error()))
		c.AbortWithStatusJSON(http.StatusInternalServerError, dto.ErrorResponse{
			Error: "internal server error",
		})
		return
	}
	h.dirty = true
	c.JSON(http.StatusOK, dto.TimezoneResponse{
		Timezone:        h.resolver.Name(),
		Source:          string(h.resolver.Source()),
		RequiresRestart: true,
	})
}
