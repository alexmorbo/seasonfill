package rest

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/alexmorbo/seasonfill/internal/catalog/app/webhookinstall"
	"github.com/alexmorbo/seasonfill/internal/shared/http/dto"
)

// WebhookInstallHandler — POST /api/v1/instances/{name}/webhook/install.
// Drives a synchronous Reconcile pass. "Created" is true when the
// reconciler issued CreateNotification (no prior NotificationID in
// cache); false when an existing entry was updated or already matched.
// Endpoint kept for the legacy UI button — 041h-2 removes it.
type WebhookInstallHandler struct {
	reconciler *webhookinstall.Reconciler
	cache      *webhookinstall.StatusCache
	logger     *slog.Logger
}

func NewWebhookInstallHandler(r *webhookinstall.Reconciler, c *webhookinstall.StatusCache, logger *slog.Logger) *WebhookInstallHandler {
	if logger == nil {
		logger = slog.Default()
	}
	return &WebhookInstallHandler{reconciler: r, cache: c, logger: logger}
}

// Install handles POST /api/v1/instances/:name/webhook/install.
//
// @Summary     Auto-install the seasonfill webhook into Sonarr
// @Description Forces a synchronous Reconcile pass. Returns 200 +
// @Description {created:false} when the webhook was already present
// @Description OR an existing entry's URL was updated in place. Returns
// @Description 201 + {created:true} when a new entry was POSTed.
// @Tags        instances
// @Produce     json
// @Param       name  path      string  true  "Instance name"
// @Success     200   {object}  dto.WebhookInstallDTO
// @Success     201   {object}  dto.WebhookInstallDTO
// @Failure     401   {object}  dto.ErrorResponse
// @Failure     404   {object}  dto.ErrorResponse
// @Failure     412   {object}  dto.ErrorResponse  "public_url undetermined"
// @Failure     502   {object}  dto.ErrorResponse
// @Security    CookieAuth
// @Security    ApiKeyAuth
// @Router      /instances/{name}/webhook/install [post]
func (h *WebhookInstallHandler) Install(c *gin.Context) {
	name := c.Param("name")
	// Snapshot the PRIOR cache state so we can compute "created" as
	// "no prior NotificationID + we now have one". UpdateNotification
	// preserves the ID, so an URL rewrite reports created:false —
	// matches the historical "we did not insert a new row" semantic.
	prior, _ := h.cache.Get(name)

	st, err := h.reconciler.Reconcile(c.Request.Context(), name)
	if err != nil {
		if errors.Is(err, webhookinstall.ErrUnknownInstance) {
			c.JSON(http.StatusNotFound, dto.ErrorResponse{Error: "unknown instance: " + name})
			return
		}
		if st.LastError != nil && *st.LastError == "public_url undetermined" {
			c.JSON(http.StatusPreconditionFailed, dto.ErrorResponse{
				Error: "cannot determine public seasonfill URL from request",
				Code:  "PUBLIC_URL_UNDETERMINED",
			})
			return
		}
		h.logger.WarnContext(c.Request.Context(), "webhook_install_failed",
			slog.String("instance", name), slog.String("error", err.Error()))
		c.JSON(http.StatusBadGateway, dto.ErrorResponse{Error: "sonarr unavailable"})
		return
	}

	id := 0
	if st.NotificationID != nil {
		id = *st.NotificationID
	}
	created := prior.NotificationID == nil && st.NotificationID != nil
	status := http.StatusOK
	if created {
		status = http.StatusCreated
	}
	c.JSON(status, dto.WebhookInstallDTO{
		Installed: st.Installed, Created: created, NotificationID: id,
	})
}
