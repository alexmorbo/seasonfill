package rest

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	domainwebhook "github.com/alexmorbo/seasonfill/internal/catalog/domain/webhook"
	"github.com/alexmorbo/seasonfill/internal/shared/clients/radarr"
	ports "github.com/alexmorbo/seasonfill/internal/shared/dataports"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
)

// RadarrWebhookHandler enqueues Radarr webhooks into the SAME durable
// webhook_inbox (ADR-0005) the Sonarr handler uses — no fork. The drainer routes
// the row to the radarr map+process by the instance's arr_instance.type. Mirror
// of WebhookHandler. Returns 500 only when the durable insert fails (Radarr's
// retry protects it); Test/Health/... are classified-at-ingest and dropped.
type RadarrWebhookHandler struct {
	inbox  ports.WebhookInboxRepository
	txr    ports.Transactor
	poke   func()
	reg    InstanceRegistry
	logger *slog.Logger
}

// NewRadarrWebhookHandler wires the durable-inbox enqueue path for Radarr. reg
// mirrors the sonarr handler: reg.Load nil = "accept any" (R-4b — no radarr
// instance is registered in the sonarr snapshot yet; R-6 wires a radarr-aware
// registry). poke may be nil (guarded).
func NewRadarrWebhookHandler(inbox ports.WebhookInboxRepository, txr ports.Transactor, poke func(), reg InstanceRegistry, logger *slog.Logger) *RadarrWebhookHandler {
	if logger == nil {
		logger = slog.Default()
	}
	return &RadarrWebhookHandler{inbox: inbox, txr: txr, poke: poke, reg: reg, logger: logger}
}

func (h *RadarrWebhookHandler) Handle(c *gin.Context) {
	name := strings.TrimSpace(c.Param("instance_name"))
	if name == "" {
		writeError(c, http.StatusBadRequest, "missing instance_name")
		return
	}
	if h.reg.Load != nil {
		if _, ok := h.reg.Snapshot()[name]; !ok {
			h.logger.WarnContext(c.Request.Context(), "radarr_webhook_unknown_instance", slog.String("instance", name))
			writeError(c, http.StatusNotFound, "unknown instance")
			return
		}
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 1<<20)
	body, err := io.ReadAll(c.Request.Body)
	if err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			writeError(c, http.StatusBadRequest, "payload too large")
			return
		}
		writeError(c, http.StatusBadRequest, "cannot read body")
		return
	}
	evt, err := radarr.MapWebhookEvent(body, domain.InstanceName(name))
	if err != nil {
		writeError(c, http.StatusBadRequest, "malformed payload")
		return
	}
	if evt.Type == domainwebhook.MovieEventTypeUnsupported {
		c.JSON(http.StatusOK, gin.H{"ok": true})
		return
	}
	ctx := c.Request.Context()
	row := ports.WebhookInboxRow{
		InstanceName: name,
		EventType:    evt.RawEventType,
		Payload:      body,
		Status:       ports.WebhookInboxStatusPending,
	}
	if ierr := h.txr.Transaction(ctx, func(ctx context.Context) error {
		return h.inbox.Insert(ctx, row)
	}); ierr != nil {
		h.logger.ErrorContext(ctx, "radarr_webhook_inbox_enqueue_failed",
			slog.String("instance", name), slog.String("event_type", evt.RawEventType), slog.String("error", ierr.Error()))
		writeError(c, http.StatusInternalServerError, "enqueue failed")
		return
	}
	if h.poke != nil {
		h.poke()
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}
