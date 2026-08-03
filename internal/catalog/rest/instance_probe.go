package rest

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/alexmorbo/seasonfill/internal/shared/http/dto"
)

const (
	probeDefaultTimeout = 10 * time.Second
	probeBodyLimit      = 4 << 10
	probeMaxResponse    = 16 << 10
)

// InstanceProbeHandler is the stateless POST /api/v1/instances/test handler.
// The injected *http.Client MUST be configured with
// CheckRedirect = http.ErrUseLastResponse so 3xx surfaces as a response.
// Construction lives in cmd/server/main.go so tests can swap clients freely.
type InstanceProbeHandler struct {
	client  *http.Client
	logger  *slog.Logger
	timeout time.Duration
	// storedKey resolves an instance's stored, decrypted Sonarr api key by
	// name (ADR-0009 S9). nil when no lookup is wired (stateless test wiring) —
	// an empty api_key then stays a 400.
	storedKey StoredKeyLookup
}

type ProbeOption func(*InstanceProbeHandler)

// StoredKeyLookup resolves an instance's stored, decrypted Sonarr api key by
// name. Injected via WithStoredKeyLookup so the stateless probe handler stays
// decoupled from persistence. Returns ErrStoredKeyUnavailable when the named
// instance does not exist or carries no stored secret.
type StoredKeyLookup func(ctx context.Context, name string) (string, error)

// ErrStoredKeyUnavailable is returned by a StoredKeyLookup when the named
// instance is absent or has no stored api key. The handler maps it to
// 404 STORED_KEY_NOT_FOUND — there is no key to fall back to.
var ErrStoredKeyUnavailable = errors.New("stored api key unavailable")

// WithStoredKeyLookup wires the edit-mode stored-key fallback (ADR-0009 S9).
func WithStoredKeyLookup(fn StoredKeyLookup) ProbeOption {
	return func(h *InstanceProbeHandler) { h.storedKey = fn }
}

// WithProbeTimeout overrides the 10s default. Tests use it to exercise the
// deadline branch without real wall-clock waits.
func WithProbeTimeout(d time.Duration) ProbeOption {
	return func(h *InstanceProbeHandler) {
		if d > 0 {
			h.timeout = d
		}
	}
}

func NewInstanceProbeHandler(client *http.Client, logger *slog.Logger, opts ...ProbeOption) *InstanceProbeHandler {
	if client == nil {
		client = &http.Client{}
	}
	if logger == nil {
		logger = slog.Default()
	}
	h := &InstanceProbeHandler{client: client, logger: logger, timeout: probeDefaultTimeout}
	for _, o := range opts {
		o(h)
	}
	return h
}

// @Summary     Probe a Sonarr instance for reachability/auth
// @Tags        instances
// @Accept      json
// @Produce     json
// @Param       body  body      dto.InstanceTestRequest   true  "URL and api_key to probe"
// @Success     200   {object}  dto.InstanceTestResponse
// @Failure     400   {object}  dto.ErrorResponse
// @Failure     404   {object}  dto.ErrorResponse  "STORED_KEY_NOT_FOUND — name given but no stored key"
// @Failure     429   {object}  dto.ErrorResponse
// @Failure     502   {object}  dto.ErrorResponse  "STORED_KEY_LOOKUP_FAILED"
// @Failure     504   {object}  dto.ErrorResponse
// @Security    CookieAuth
// @Security    ApiKeyAuth
// @Router      /admin/instances/test [post]
func (h *InstanceProbeHandler) Test(c *gin.Context) {
	req, ok := h.readBody(c)
	if !ok {
		return
	}
	target, err := validateProbeURL(req.URL)
	if err != nil {
		c.AbortWithStatusJSON(http.StatusBadRequest,
			dto.ErrorResponse{Error: err.Error(), Code: "BAD_REQUEST"})
		return
	}

	apiKey, ok := h.resolveAPIKey(c, req)
	if !ok {
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), h.timeout)
	defer cancel()

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		c.AbortWithStatusJSON(http.StatusBadRequest,
			dto.ErrorResponse{Error: fmt.Sprintf("probe: %s", err), Code: "BAD_REQUEST"})
		return
	}
	httpReq.Header.Set("X-Api-Key", apiKey)
	httpReq.Header.Set("Accept", "application/json")
	httpReq.Header.Set("User-Agent", "seasonfill-probe")

	resp, err := h.client.Do(httpReq)
	if err != nil {
		h.logger.WarnContext(ctx, "instance.probe.timeout",
			slog.String("event", "probe.timeout"),
			slog.String("instance_url", req.URL),
			slog.String("error", err.Error()))
		c.AbortWithStatusJSON(http.StatusGatewayTimeout,
			dto.ErrorResponse{Error: "timeout", Code: "PROBE_TIMEOUT"})
		return
	}
	defer func() { _ = resp.Body.Close() }()

	// Redirect path: CheckRedirect=ErrUseLastResponse surfaces 3xx as-is.
	if resp.StatusCode >= 300 && resp.StatusCode < 400 {
		h.logger.InfoContext(ctx, "instance.probe.redirect_rejected",
			slog.String("event", "probe.redirect_rejected"),
			slog.String("instance_url", req.URL),
			slog.Int("status", resp.StatusCode))
		c.JSON(http.StatusOK, dto.InstanceTestResponse{OK: false, Reason: "redirect rejected"})
		return
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		reason := reasonForStatus(resp.StatusCode)
		h.logger.InfoContext(ctx, "instance.probe.non_2xx",
			slog.String("event", "probe.non_2xx"),
			slog.String("instance_url", req.URL),
			slog.Int("status", resp.StatusCode))
		c.JSON(http.StatusOK, dto.InstanceTestResponse{OK: false, Reason: reason})
		return
	}

	ct := resp.Header.Get("Content-Type")
	if !strings.HasPrefix(strings.ToLower(strings.TrimSpace(ct)), "application/json") {
		h.logger.InfoContext(ctx, "instance.probe.bad_content_type",
			slog.String("event", "probe.bad_content_type"),
			slog.String("instance_url", req.URL),
			slog.String("content_type", ct))
		c.JSON(http.StatusOK, dto.InstanceTestResponse{OK: false, Reason: "not a Sonarr API endpoint"})
		return
	}

	body, _ := io.ReadAll(io.LimitReader(resp.Body, probeMaxResponse))
	var parsed struct {
		Version string `json:"version"`
	}
	_ = json.Unmarshal(body, &parsed)

	h.logger.InfoContext(ctx, "instance.probe.ok",
		slog.String("event", "probe.ok"),
		slog.String("instance_url", req.URL),
		slog.String("version", parsed.Version))
	c.JSON(http.StatusOK, dto.InstanceTestResponse{OK: true, Version: parsed.Version})
}

func (h *InstanceProbeHandler) readBody(c *gin.Context) (dto.InstanceTestRequest, bool) {
	var out dto.InstanceTestRequest
	ct := c.GetHeader("Content-Type")
	if !strings.HasPrefix(ct, "application/json") {
		c.AbortWithStatusJSON(http.StatusBadRequest,
			dto.ErrorResponse{Error: "content-type must be application/json", Code: "BAD_REQUEST"})
		return out, false
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, probeBodyLimit)
	raw, err := io.ReadAll(c.Request.Body)
	if err != nil {
		c.AbortWithStatusJSON(http.StatusBadRequest,
			dto.ErrorResponse{Error: "cannot read body", Code: "BAD_REQUEST"})
		return out, false
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		c.AbortWithStatusJSON(http.StatusBadRequest,
			dto.ErrorResponse{Error: "malformed body", Code: "BAD_REQUEST"})
		return out, false
	}
	if strings.TrimSpace(out.URL) == "" {
		c.AbortWithStatusJSON(http.StatusBadRequest,
			dto.ErrorResponse{Error: "url is required", Code: "BAD_REQUEST"})
		return out, false
	}
	// ADR-0009 S9: api_key may be empty IF a name is supplied — the handler then
	// falls back to the instance's stored decrypted key. Both empty (no key, no
	// name) stays a 400 as before.
	if strings.TrimSpace(out.APIKey) == "" &&
		(out.Name == nil || strings.TrimSpace(*out.Name) == "") {
		c.AbortWithStatusJSON(http.StatusBadRequest,
			dto.ErrorResponse{Error: "api_key or name is required", Code: "BAD_REQUEST"})
		return out, false
	}
	return out, true
}

// resolveAPIKey returns the effective api key for the probe. A non-empty request
// api_key wins (typed key overrides stored). When it is empty the handler falls
// back to the named instance's stored decrypted key via the injected lookup. On
// any failure it writes the error response and returns ok=false. The resolved key
// is used only to build the transient outbound request — never logged, never
// returned to the caller.
func (h *InstanceProbeHandler) resolveAPIKey(c *gin.Context, req dto.InstanceTestRequest) (string, bool) {
	if strings.TrimSpace(req.APIKey) != "" {
		return req.APIKey, true
	}
	name := ""
	if req.Name != nil {
		name = strings.TrimSpace(*req.Name)
	}
	if name == "" || h.storedKey == nil {
		c.AbortWithStatusJSON(http.StatusBadRequest,
			dto.ErrorResponse{Error: "api_key is required", Code: "BAD_REQUEST"})
		return "", false
	}
	key, err := h.storedKey(c.Request.Context(), name)
	if err != nil {
		if errors.Is(err, ErrStoredKeyUnavailable) {
			c.AbortWithStatusJSON(http.StatusNotFound,
				dto.ErrorResponse{Error: "instance has no stored api key", Code: "STORED_KEY_NOT_FOUND"})
			return "", false
		}
		// Do NOT log name or key — only the request url, matching existing logs.
		h.logger.WarnContext(c.Request.Context(), "instance.probe.stored_key_lookup_failed",
			slog.String("event", "probe.stored_key_lookup_failed"),
			slog.String("instance_url", req.URL),
			slog.String("error", err.Error()))
		c.AbortWithStatusJSON(http.StatusBadGateway,
			dto.ErrorResponse{Error: "stored key lookup failed", Code: "STORED_KEY_LOOKUP_FAILED"})
		return "", false
	}
	return key, true
}

func validateProbeURL(raw string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return "", fmt.Errorf("probe: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", errors.New("url scheme must be http or https")
	}
	if u.Host == "" {
		return "", errors.New("url must include a host")
	}
	if u.User != nil {
		return "", errors.New("url must not include userinfo")
	}
	trimmed := strings.TrimRight(u.String(), "/")
	return trimmed + "/api/v3/system/status", nil
}

func reasonForStatus(code int) string {
	switch {
	case code == http.StatusUnauthorized, code == http.StatusForbidden:
		return "authentication failed"
	case code >= 400 && code < 500:
		return "bad request"
	case code >= 500:
		return "upstream error"
	default:
		return fmt.Sprintf("unexpected status %d", code)
	}
}
