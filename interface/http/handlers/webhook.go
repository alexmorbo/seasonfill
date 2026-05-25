package handlers

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/alexmorbo/seasonfill/application/webhook"
	domainwebhook "github.com/alexmorbo/seasonfill/domain/webhook"
	"github.com/alexmorbo/seasonfill/infrastructure/sonarr"
	"github.com/alexmorbo/seasonfill/internal/observability"
)

type WebhookProcessor interface {
	Process(ctx context.Context, evt domainwebhook.Event) error
}

type WebhookHandler struct {
	uc     WebhookProcessor
	reg    InstanceRegistry
	logger *slog.Logger
}

// NewWebhookHandler — reg.Load nil-OK ("accept any"; tests only).
// In production, reg is wired to the same instanceMapHolder the reload
// bus updates, so a Sonarr added via Settings UI is reachable on its
// webhook URL within one bus tick — no pod restart.
func NewWebhookHandler(uc WebhookProcessor, reg InstanceRegistry, logger *slog.Logger) *WebhookHandler {
	if logger == nil {
		logger = slog.Default()
	}
	return &WebhookHandler{uc: uc, reg: reg, logger: logger}
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
		if _, ok := h.reg.snapshot()[name]; !ok {
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
	evt, err := sonarr.MapWebhookEvent(body, name)
	if err != nil {
		writeError(c, http.StatusBadRequest, "malformed payload")
		return
	}
	if err := h.uc.Process(c.Request.Context(), evt); err != nil {
		if webhook.IsTransient(err) {
			writeError(c, http.StatusInternalServerError, "transient failure, retry")
			return
		}
		kind := webhook.ErrorKind(err)
		observability.IncWebhookProcessingFailures(name, kind)
		h.logger.ErrorContext(c.Request.Context(), "webhook_logic_failure",
			slog.String("instance", name),
			slog.String("event_type", string(evt.Type)),
			slog.String("error", err.Error()))
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}
