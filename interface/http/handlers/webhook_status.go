package handlers

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/alexmorbo/seasonfill/domain"
	"github.com/alexmorbo/seasonfill/infrastructure/sonarr"
	"github.com/alexmorbo/seasonfill/interface/http/dto"
)

// WebhookStatusHandler — GET /api/v1/instances/{name}/webhook/status.
// Queries Sonarr's /api/v3/notification list and reports whether the
// seasonfill webhook is already installed, without mutating any state.
type WebhookStatusHandler struct {
	reg    InstanceRegistry
	logger *slog.Logger
}

// NewWebhookStatusHandler constructs the handler. apiKey is unused
// here (status is read-only); the field is kept for symmetry with
// NewWebhookInstallHandler so future callers can use a shared factory.
func NewWebhookStatusHandler(reg InstanceRegistry, logger *slog.Logger) *WebhookStatusHandler {
	if logger == nil {
		logger = slog.Default()
	}
	return &WebhookStatusHandler{reg: reg, logger: logger}
}

// Status handles GET /api/v1/instances/:name/webhook/status.
//
// @Summary     Check whether the seasonfill webhook is installed in Sonarr
// @Description Queries Sonarr GET /api/v3/notification and matches against
// @Description the canonical /api/v1/webhook/sonarr/<instance> path segment.
// @Description Returns installed:true with the matched notification ID and
// @Description URL when found; installed:false otherwise. Does NOT create or
// @Description modify any Sonarr resource.
// @Tags        instances
// @Produce     json
// @Param       name  path      string  true  "Instance name"
// @Success     200   {object}  dto.WebhookStatusDTO
// @Failure     401   {object}  dto.ErrorResponse
// @Failure     404   {object}  dto.ErrorResponse
// @Failure     502   {object}  dto.ErrorResponse
// @Security    CookieAuth
// @Security    ApiKeyAuth
// @Router      /instances/{name}/webhook/status [get]
func (h *WebhookStatusHandler) Status(c *gin.Context) {
	name := c.Param("name")
	inst, ok := h.reg.snapshot()[name]
	if !ok || inst.Client == nil {
		c.JSON(http.StatusNotFound, dto.ErrorResponse{Error: "unknown instance: " + name})
		return
	}
	concrete, ok := inst.Client.(*sonarr.Client)
	if !ok {
		writeInternalError(c, h.logger, "webhook_status_client_type_mismatch",
			errors.New("instance client is not *sonarr.Client"),
			slog.String("instance", name))
		return
	}

	ctx := c.Request.Context()
	existing, err := concrete.ListNotifications(ctx)
	if err != nil {
		if errors.Is(err, domain.ErrInstanceUnauthorized) {
			h.logger.WarnContext(ctx, "webhook_status_list_unauthorized",
				slog.String("instance", name), slog.String("error", err.Error()))
			c.JSON(http.StatusBadGateway, dto.ErrorResponse{Error: "sonarr unauthorized"})
			return
		}
		h.logger.ErrorContext(ctx, "webhook_status_list_failed",
			slog.String("instance", name), slog.String("error", err.Error()))
		c.JSON(http.StatusBadGateway, dto.ErrorResponse{Error: "sonarr unavailable"})
		return
	}

	// Derive expected URL the same way the install handler does, but
	// fall back gracefully to the canonical path segment match when the
	// public URL cannot be determined from this request (e.g. internal
	// health-check callers). matchesWebhookURL handles both cases.
	publicURL := derivePublicURL(c)
	expectedURL := publicURL + "/api/v1/webhook/sonarr/" + name

	for _, n := range existing {
		if n.Implementation != "Webhook" {
			continue
		}
		if matchesWebhookURL(n.Fields, expectedURL, name) {
			nid := n.ID
			urlVal := webhookFieldURL(n.Fields)
			c.JSON(http.StatusOK, dto.WebhookStatusDTO{
				Installed:      true,
				NotificationID: &nid,
				URL:            urlVal,
			})
			return
		}
	}

	c.JSON(http.StatusOK, dto.WebhookStatusDTO{Installed: false})
}

// webhookFieldURL extracts the raw URL string from the notification
// fields array. Returns nil when the url field is absent or not a string.
func webhookFieldURL(fields []sonarr.NotificationField) *string {
	for _, f := range fields {
		if f.Name != "url" {
			continue
		}
		s, ok := f.Value.(string)
		if !ok {
			return nil
		}
		return &s
	}
	return nil
}
