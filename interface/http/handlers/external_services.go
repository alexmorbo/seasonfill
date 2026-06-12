package handlers

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/gin-gonic/gin"

	appext "github.com/alexmorbo/seasonfill/application/externalservices"
	infra "github.com/alexmorbo/seasonfill/infrastructure/externalservices"
	"github.com/alexmorbo/seasonfill/interface/http/dto"
)

// ExternalServicesHandler exposes the runtime config for the three
// enrichment sources. Mounted under the existing guarded group in
// server.go (same auth middleware as /api/v1/instances/:name).
type ExternalServicesHandler struct {
	uc     *appext.UseCase
	logger *slog.Logger
}

func NewExternalServicesHandler(uc *appext.UseCase, logger *slog.Logger) *ExternalServicesHandler {
	if logger == nil {
		logger = slog.Default()
	}
	return &ExternalServicesHandler{uc: uc, logger: logger}
}

// List returns the masked configuration for every supported service.
//
// @Summary     List external service configurations (masked)
// @Tags        external-services
// @Produce     json
// @Success     200  {object}  dto.ExternalServiceListResponse
// @Failure     401  {object}  dto.ErrorResponse
// @Security    CookieAuth
// @Security    ApiKeyAuth
// @Router      /external-services [get]
func (h *ExternalServicesHandler) List(c *gin.Context) {
	views, err := h.uc.List(c.Request.Context())
	if err != nil {
		h.writeError(c, err)
		return
	}
	out := dto.ExternalServiceListResponse{Services: make([]dto.ExternalServiceDTO, 0, len(views))}
	for _, v := range views {
		out.Services = append(out.Services, externalServiceViewToDTO(v))
	}
	c.JSON(http.StatusOK, out)
}

// Upsert creates or replaces the row for the given service.
//
// @Summary     Upsert external service configuration
// @Tags        external-services
// @Accept      json
// @Produce     json
// @Param       service  path      string                              true  "Service (tmdb|omdb|tvdb)"
// @Param       body     body      dto.ExternalServiceUpsertRequest    true  "Settings"
// @Success     200      {object}  dto.ExternalServiceDTO
// @Failure     400      {object}  dto.ErrorResponse
// @Failure     401      {object}  dto.ErrorResponse
// @Security    CookieAuth
// @Security    ApiKeyAuth
// @Router      /external-services/{service} [put]
func (h *ExternalServicesHandler) Upsert(c *gin.Context) {
	svc := infra.Service(c.Param("service"))
	if !svc.Valid() {
		c.JSON(http.StatusBadRequest, dto.ErrorResponse{Error: "invalid service"})
		return
	}
	var req dto.ExternalServiceUpsertRequest
	if !readJSONBody(c, &req) {
		return
	}
	in := appext.UpsertInput{
		Enabled:       req.Enabled,
		APIKey:        req.APIKey,
		ProxyURL:      req.ProxyURL,
		ProxyUsername: req.ProxyUsername,
		ProxyPassword: req.ProxyPassword,
	}
	view, err := h.uc.Upsert(c.Request.Context(), svc, in)
	if err != nil {
		h.writeError(c, err)
		return
	}
	c.JSON(http.StatusOK, externalServiceViewToDTO(view))
}

// Test runs the cheap probe and persists the outcome.
//
// @Summary     Test external service connectivity
// @Tags        external-services
// @Produce     json
// @Param       service  path      string  true  "Service (tmdb|omdb|tvdb)"
// @Success     200      {object}  dto.ExternalServiceTestResponse
// @Failure     400      {object}  dto.ErrorResponse
// @Failure     401      {object}  dto.ErrorResponse
// @Security    CookieAuth
// @Security    ApiKeyAuth
// @Router      /external-services/{service}/test [post]
func (h *ExternalServicesHandler) Test(c *gin.Context) {
	svc := infra.Service(c.Param("service"))
	if !svc.Valid() {
		c.JSON(http.StatusBadRequest, dto.ErrorResponse{Error: "invalid service"})
		return
	}
	res, err := h.uc.Test(c.Request.Context(), svc)
	if err != nil {
		h.writeError(c, err)
		return
	}
	c.JSON(http.StatusOK, dto.ExternalServiceTestResponse{
		Outcome:   string(res.Outcome),
		Message:   res.Message,
		LatencyMS: res.LatencyMS,
	})
}

func (h *ExternalServicesHandler) writeError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, infra.ErrInvalidService):
		c.JSON(http.StatusBadRequest, dto.ErrorResponse{Error: err.Error()})
	default:
		h.logger.ErrorContext(c.Request.Context(), "external_services.handler_err", slog.Any("err", err))
		c.JSON(http.StatusInternalServerError, dto.ErrorResponse{Error: "internal error"})
	}
}

func externalServiceViewToDTO(v appext.MaskedView) dto.ExternalServiceDTO {
	out := dto.ExternalServiceDTO{
		Service:          string(v.Service),
		Enabled:          v.Enabled,
		APIKeyMasked:     v.APIKeyMasked,
		APIKeyConfigured: v.APIKeyConfigured,
		ProxyURLSet:      v.ProxyURLSet,
		ProxyAuthSet:     v.ProxyAuthSet,
		ProxyScheme:      v.ProxyScheme,
		ProxyHost:        v.ProxyHost,
		LastTestAt:       v.LastTestAt,
	}
	if v.LastTestOutcome != "" {
		out.LastTestOutcome = string(v.LastTestOutcome)
	}
	if v.LastTestMessage != "" {
		out.LastTestMessage = v.LastTestMessage
	}
	return out
}
