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
	"github.com/alexmorbo/seasonfill/internal/shared/clients/sonarr"
	ports "github.com/alexmorbo/seasonfill/internal/shared/dataports"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
)

// WebhookHandler enqueues Sonarr webhooks into the durable webhook_inbox
// (ADR 0005, E4 hard-cutover). It no longer runs Process inline: it writes
// a pending row inside a Transactor.Transaction and best-effort pokes the
// drainer. The handler returns 500 ONLY when the durable insert fails
// (F-11) — that is the write Sonarr's retry protects. All error
// classification (transient vs dead-letter) lives in the drainer.
type WebhookHandler struct {
	inbox  ports.WebhookInboxRepository
	txr    ports.Transactor
	poke   func()
	reg    InstanceRegistry
	logger *slog.Logger
}

// NewWebhookHandler wires the durable-inbox enqueue path. reg.Load nil =
// "accept any" (test only); in production reg is the reload-aware snapshot
// so a Sonarr added via Settings is reachable on its webhook URL within one
// bus tick. poke may be nil (guarded); inbox/txr are only dereferenced for
// supported events.
func NewWebhookHandler(
	inbox ports.WebhookInboxRepository,
	txr ports.Transactor,
	poke func(),
	reg InstanceRegistry,
	logger *slog.Logger,
) *WebhookHandler {
	if logger == nil {
		logger = slog.Default()
	}
	return &WebhookHandler{inbox: inbox, txr: txr, poke: poke, reg: reg, logger: logger}
}

func (h *WebhookHandler) Handle(c *gin.Context) {
	name := strings.TrimSpace(c.Param("instance_name"))
	if name == "" {
		writeError(c, http.StatusBadRequest, "missing instance_name")
		return
	}
	// reg.Load nil = accept any (test only). Otherwise consult the
	// reload-aware snapshot every request.
	if h.reg.Load != nil {
		if _, ok := h.reg.Snapshot()[name]; !ok {
			h.logger.WarnContext(c.Request.Context(), "webhook_unknown_instance",
				slog.String("instance", name))
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
	evt, err := sonarr.MapWebhookEvent(body, domain.InstanceName(name))
	if err != nil {
		writeError(c, http.StatusBadRequest, "malformed payload")
		return
	}
	// Classify-at-ingest: Test/Rename/Health/ApplicationUpdate/... are dropped
	// here rather than enqueued+drained-to-noop. Cheaper and equally correct.
	if evt.Type == domainwebhook.EventTypeUnsupported {
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
	if err := h.txr.Transaction(ctx, func(ctx context.Context) error {
		return h.inbox.Insert(ctx, row)
	}); err != nil {
		h.logger.ErrorContext(ctx, "webhook_inbox_enqueue_failed",
			slog.String("instance", name),
			slog.String("event_type", evt.RawEventType),
			slog.String("error", err.Error()))
		writeError(c, http.StatusInternalServerError, "enqueue failed")
		return
	}

	// Best-effort latency optimisation: signal an early drain. Non-blocking;
	// losing the signal is safe (the drainer ticker catches up).
	if h.poke != nil {
		h.poke()
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}
