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
	uc             WebhookProcessor
	knownInstances map[string]struct{}
	logger         *slog.Logger
}

// NewWebhookHandler — knownInstances is the set of configured Sonarr
// instance names. URL path :instance_name is validated against this set;
// unknown names get 404. Pass nil to accept any (tests only).
func NewWebhookHandler(uc WebhookProcessor, knownInstances map[string]struct{}, logger *slog.Logger) *WebhookHandler {
	if logger == nil {
		logger = slog.Default()
	}
	return &WebhookHandler{uc: uc, knownInstances: knownInstances, logger: logger}
}

func (h *WebhookHandler) Handle(c *gin.Context) {
	name := strings.TrimSpace(c.Param("instance_name"))
	if name == "" {
		writeError(c, http.StatusBadRequest, "missing instance_name")
		return
	}
	if h.knownInstances != nil {
		if _, ok := h.knownInstances[name]; !ok {
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
