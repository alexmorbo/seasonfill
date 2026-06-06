package handlers

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/alexmorbo/seasonfill/application/webhookinstall"
	"github.com/alexmorbo/seasonfill/interface/http/dto"
)

// WebhookStatusHandler — GET /api/v1/instances/{name}/webhook/status.
// Reads the StatusCache (lazy-refresh inside Reconciler.GetStatus).
// Refresh failures are logged but do not 502 — the previous Status is
// served with `error` populated so the UI can render a stale-data
// badge.
type WebhookStatusHandler struct {
	reconciler *webhookinstall.Reconciler
	logger     *slog.Logger
}

func NewWebhookStatusHandler(r *webhookinstall.Reconciler, logger *slog.Logger) *WebhookStatusHandler {
	if logger == nil {
		logger = slog.Default()
	}
	return &WebhookStatusHandler{reconciler: r, logger: logger}
}

// Status handles GET /api/v1/instances/:name/webhook/status.
//
// @Summary     Check whether the seasonfill webhook is installed in Sonarr
// @Description Reads the in-memory StatusCache populated by the
// @Description reconciler. Stale entries trigger a lazy refresh
// @Description (one Sonarr round-trip) before serving. Optional
// @Description `error` field carries the last reconcile failure.
// @Tags        instances
// @Produce     json
// @Param       name  path      string  true  "Instance name"
// @Success     200   {object}  dto.WebhookStatusDTO
// @Failure     404   {object}  dto.ErrorResponse
// @Security    CookieAuth
// @Security    ApiKeyAuth
// @Router      /instances/{name}/webhook/status [get]
func (h *WebhookStatusHandler) Status(c *gin.Context) {
	name := c.Param("name")
	st, err := h.reconciler.GetStatus(c.Request.Context(), name)
	if err != nil {
		if errors.Is(err, webhookinstall.ErrUnknownInstance) {
			c.JSON(http.StatusNotFound, dto.ErrorResponse{Error: "unknown instance: " + name})
			return
		}
		h.logger.WarnContext(c.Request.Context(), "webhook_status_refresh_failed",
			slog.String("instance", name), slog.String("error", err.Error()))
	}
	c.JSON(http.StatusOK, dto.WebhookStatusDTO{
		Installed:      st.Installed,
		NotificationID: st.NotificationID,
		URL:            st.InstalledURL,
		Error:          st.LastError,
	})
}
