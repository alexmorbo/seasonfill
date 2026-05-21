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
	"github.com/alexmorbo/seasonfill/internal/config"
	"github.com/alexmorbo/seasonfill/internal/observability"
)

// WebhookProcessor is the slice of `*application/webhook.UseCase` the
// handler depends on. Declaring the consumer-side interface here lets
// tests substitute a fake; production passes a concrete `*UseCase`
// which satisfies it structurally.
type WebhookProcessor interface {
	Process(ctx context.Context, evt domainwebhook.Event) error
}

// WebhookHandler serves POST /api/v1/webhook/sonarr/:instance_name.
// Stateless; all auth / allow-list decisions read from `cfg` per call.
type WebhookHandler struct {
	uc     WebhookProcessor
	cfg    config.WebhookConfig
	logger *slog.Logger
}

// NewWebhookHandler constructs the handler. cfg is captured by value.
func NewWebhookHandler(uc WebhookProcessor, cfg config.WebhookConfig, logger *slog.Logger) *WebhookHandler {
	if logger == nil {
		logger = slog.Default()
	}
	return &WebhookHandler{uc: uc, cfg: cfg, logger: logger}
}

// Handle is the gin.HandlerFunc.
func (h *WebhookHandler) Handle(c *gin.Context) {
	instanceName := strings.TrimSpace(c.Param("instance_name"))

	// Defensive — Gin populates declared :name params, but an empty
	// trimmed value means the operator broke route registration.
	if instanceName == "" {
		writeError(c, http.StatusBadRequest, "missing instance_name")
		return
	}

	// Defensive instance allow-list (Q-8). Empty list = accept any.
	if !instanceAllowed(instanceName, h.cfg.AllowedInstances) {
		h.logger.WarnContext(c.Request.Context(), "webhook_disallowed_instance",
			slog.String("instance", instanceName),
		)
		writeError(c, http.StatusNotFound, "unknown instance")
		return
	}

	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 1<<20)
	body, err := io.ReadAll(c.Request.Body)
	if err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			writeError(c, http.StatusBadRequest, "payload too large")
			return
		}
		h.logger.ErrorContext(c.Request.Context(), "webhook_body_read_failed",
			slog.String("instance", instanceName),
			slog.String("error", err.Error()),
		)
		writeError(c, http.StatusBadRequest, "cannot read body")
		return
	}

	evt, err := sonarr.MapWebhookEvent(body, instanceName)
	if err != nil {
		h.logger.WarnContext(c.Request.Context(), "webhook_malformed_payload",
			slog.String("instance", instanceName),
			slog.String("error", err.Error()),
		)
		writeError(c, http.StatusBadRequest, "malformed payload")
		return
	}

	if err := h.uc.Process(c.Request.Context(), evt); err != nil {
		if webhook.IsTransient(err) {
			h.logger.ErrorContext(c.Request.Context(), "webhook_transient_failure",
				slog.String("instance", instanceName),
				slog.String("event_type", string(evt.Type)),
				slog.String("raw_event_type", evt.RawEventType),
				slog.String("error", err.Error()),
			)
			writeError(c, http.StatusInternalServerError, "transient failure, retry")
			return
		}

		// Non-transient: log + metric + 200 so Sonarr doesn't retry.
		kind := webhook.ErrorKind(err)
		observability.IncWebhookProcessingFailures(instanceName, kind)
		h.logger.ErrorContext(c.Request.Context(), "webhook_logic_failure",
			slog.String("instance", instanceName),
			slog.String("event_type", string(evt.Type)),
			slog.String("raw_event_type", evt.RawEventType),
			slog.String("error_kind", kind),
			slog.String("error", err.Error()),
		)
	}

	c.JSON(http.StatusOK, gin.H{"ok": true})
}

// instanceAllowed reports whether name appears in allowed. Empty
// allow-list returns true.
func instanceAllowed(name string, allowed []string) bool {
	if len(allowed) == 0 {
		return true
	}
	for _, a := range allowed {
		if a == name {
			return true
		}
	}
	return false
}
