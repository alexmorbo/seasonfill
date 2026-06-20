package rest

import (
	"log/slog"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/alexmorbo/seasonfill/interface/http/dto"
	"github.com/alexmorbo/seasonfill/internal/catalog/app/webhookinstall"
)

// WebhooksAggregateHandler serves GET /api/v1/webhooks/status.
type WebhooksAggregateHandler struct {
	reconciler *webhookinstall.Reconciler
	instances  InstanceLister
	logger     *slog.Logger
}

// NewWebhooksAggregateHandler wires the handler. logger=nil → slog.Default().
func NewWebhooksAggregateHandler(
	r *webhookinstall.Reconciler,
	instances InstanceLister,
	logger *slog.Logger,
) *WebhooksAggregateHandler {
	if logger == nil {
		logger = slog.Default()
	}
	return &WebhooksAggregateHandler{reconciler: r, instances: instances, logger: logger}
}

// Status handles GET /api/v1/webhooks/status.
//
// @Summary     Aggregate webhook status per instance
// @Description Fan-out over every configured instance using the
// @Description in-process StatusCache (lazy refresh via Reconciler).
// @Tags        webhooks
// @Produce     json
// @Success     200  {object}  dto.WebhookStatusAggregate
// @Failure     500  {object}  dto.ErrorResponse
// @Security    CookieAuth
// @Security    ApiKeyAuth
// @Router      /webhooks/status [get]
func (h *WebhooksAggregateHandler) Status(c *gin.Context) {
	ctx := c.Request.Context()
	names, err := h.instances.ListNames(ctx)
	if err != nil {
		writeInternalError(c, h.logger, "webhooks_status_list_failed", err,
			slog.String("endpoint", "/api/v1/webhooks/status"))
		return
	}

	items, err := webhookinstall.Aggregate(ctx, h.reconciler, names)
	if err != nil {
		writeInternalError(c, h.logger, "webhooks_status_aggregate_failed", err,
			slog.String("endpoint", "/api/v1/webhooks/status"))
		return
	}

	out := dto.WebhookStatusAggregate{Items: make([]dto.WebhookStatusAggregateItem, 0, len(items))}
	for _, it := range items {
		out.Items = append(out.Items, dto.WebhookStatusAggregateItem{
			InstanceName:   it.InstanceName,
			Installed:      it.Installed,
			Healthy:        it.Healthy,
			NotificationID: it.NotificationID,
			URL:            it.URL,
			Error:          it.Error,
		})
		if it.Healthy {
			out.HealthyCount++
		} else {
			out.UnhealthyCount++
		}
	}
	c.JSON(http.StatusOK, out)
}
