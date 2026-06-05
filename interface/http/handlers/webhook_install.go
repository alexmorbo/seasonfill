package handlers

import (
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/alexmorbo/seasonfill/domain"
	"github.com/alexmorbo/seasonfill/infrastructure/sonarr"
	"github.com/alexmorbo/seasonfill/interface/http/dto"
)

// WebhookInstallHandler — POST /api/v1/instances/{name}/webhook/install.
// Auto-installs the seasonfill OnGrab/OnImport/OnImportFailure
// webhook into Sonarr's notification list. No-op (200) if a webhook
// matching our URL already exists; creates (201) otherwise.
type WebhookInstallHandler struct {
	reg    InstanceRegistry
	apiKey string
	logger *slog.Logger
}

// NewWebhookInstallHandler. apiKey is `cfg.Auth.APIKey` — the same
// value Sonarr already needs to POST our webhook endpoint per the
// C-6 invariant from the parent story.
func NewWebhookInstallHandler(reg InstanceRegistry, apiKey string, logger *slog.Logger) *WebhookInstallHandler {
	if logger == nil {
		logger = slog.Default()
	}
	return &WebhookInstallHandler{reg: reg, apiKey: apiKey, logger: logger}
}

// Install handles POST /api/v1/instances/:name/webhook/install.
//
// @Summary     Auto-install the seasonfill webhook into Sonarr
// @Description Looks for an existing notification whose URL matches
// @Description <public_url>/api/v1/webhook/sonarr/<instance_name>; if
// @Description present, returns 200 + {created:false}. Otherwise
// @Description POSTs a new Webhook notification with OnGrab+OnImport+
// @Description OnImportFailure triggers and the X-Api-Key header, then
// @Description returns 201 + {created:true}.
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
	inst, ok := h.reg.snapshot()[name]
	if !ok || inst.Client == nil {
		c.JSON(http.StatusNotFound, dto.ErrorResponse{Error: "unknown instance: " + name})
		return
	}
	concrete, ok := inst.Client.(*sonarr.Client)
	if !ok {
		writeInternalError(c, h.logger, "webhook_install_client_type_mismatch",
			errors.New("instance client is not *sonarr.Client"),
			slog.String("instance", name))
		return
	}

	publicURL := derivePublicURL(c)
	if publicURL == "" {
		c.JSON(http.StatusPreconditionFailed, dto.ErrorResponse{
			Error: "cannot determine public seasonfill URL from request",
			Code:  "PUBLIC_URL_UNDETERMINED",
		})
		return
	}
	expectedURL := publicURL + "/api/v1/webhook/sonarr/" + name

	ctx := c.Request.Context()
	existing, err := concrete.ListNotifications(ctx)
	if err != nil {
		h.respondSonarrErr(c, name, "webhook_install_list_failed", err)
		return
	}
	for _, n := range existing {
		if !strings.EqualFold(n.Implementation, "Webhook") {
			continue
		}
		if matchesWebhookURL(n.Fields, expectedURL, name) {
			c.JSON(http.StatusOK, dto.WebhookInstallDTO{
				Installed: true, Created: false, NotificationID: n.ID,
			})
			return
		}
	}

	created, err := concrete.CreateNotification(ctx, sonarr.NotificationPayload{
		Name:           "seasonfill",
		URL:            expectedURL,
		APIKeyHeader:   h.apiKey,
		TemplateFields: pickTemplateFields(existing),
	})
	if err != nil {
		h.respondSonarrErr(c, name, "webhook_install_create_failed", err)
		return
	}
	c.JSON(http.StatusCreated, dto.WebhookInstallDTO{
		Installed: true, Created: true, NotificationID: created.ID,
	})
}

// respondSonarrErr applies the same Sonarr → HTTP mapping the rest of
// the handlers use: 401/403 → 502 "sonarr unauthorized"; everything
// else → 502 "sonarr unavailable" + ERROR log.
func (h *WebhookInstallHandler) respondSonarrErr(c *gin.Context, name, event string, err error) {
	ctx := c.Request.Context()
	if errors.Is(err, domain.ErrInstanceUnauthorized) {
		h.logger.WarnContext(ctx, event+"_unauthorized",
			slog.String("instance", name), slog.String("error", err.Error()))
		c.JSON(http.StatusBadGateway, dto.ErrorResponse{Error: "sonarr unauthorized"})
		return
	}
	h.logger.ErrorContext(ctx, event,
		slog.String("instance", name), slog.String("error", err.Error()))
	c.JSON(http.StatusBadGateway, dto.ErrorResponse{Error: "sonarr unavailable"})
}

// derivePublicURL builds the public seasonfill URL by combining
// scheme + host from request headers. Mirrors the OIDC handler's
// approach (oidc_handler.go:70). Prefers X-Forwarded-Proto +
// X-Forwarded-Host; falls back to request.TLS + request.Host. Returns
// empty string when host is unavailable, signalling 412.
func derivePublicURL(c *gin.Context) string {
	host := strings.TrimSpace(c.GetHeader("X-Forwarded-Host"))
	if host == "" {
		host = strings.TrimSpace(c.Request.Host)
	}
	if host == "" {
		return ""
	}
	scheme := strings.TrimSpace(c.GetHeader("X-Forwarded-Proto"))
	if scheme == "" {
		if c.Request.TLS != nil {
			scheme = "https"
		} else {
			scheme = "http"
		}
	}
	return scheme + "://" + host
}

// matchesWebhookURL scans the field array for a `url` entry whose
// string value either equals the expected URL exactly OR contains the
// canonical `/api/v1/webhook/sonarr/<instance>` segment — prefix-match
// per the parent story's open-question recommendation, so a stale
// webhook still pointing at the old public URL is still recognised.
func matchesWebhookURL(fields []sonarr.NotificationField, expected, instance string) bool {
	canonical := "/api/v1/webhook/sonarr/" + instance
	expectedLower := strings.ToLower(expected)
	for _, f := range fields {
		if f.Name != "url" {
			continue
		}
		s, ok := f.Value.(string)
		if !ok {
			continue
		}
		sLower := strings.ToLower(s)
		if sLower == expectedLower {
			return true
		}
		if strings.Contains(sLower, strings.ToLower(canonical)) {
			return true
		}
	}
	return false
}

// pickTemplateFields returns the field array of the first existing
// Webhook notification, if any — defends against Sonarr field-schema
// drift across versions. Returns nil to signal "use minimal known-
// good template" when no Webhook is configured yet.
func pickTemplateFields(list []sonarr.Notification) []sonarr.NotificationField {
	for _, n := range list {
		if strings.EqualFold(n.Implementation, "Webhook") && len(n.Fields) > 0 {
			return n.Fields
		}
	}
	return nil
}
