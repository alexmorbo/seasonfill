package handlers

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

	"github.com/alexmorbo/seasonfill/interface/http/dto"
)

const (
	probeDefaultTimeout = 10 * time.Second
	probeBodyLimit      = 4 << 10
	probeMaxResponse    = 16 << 10
)

// InstanceProbeHandler is the stateless POST /api/v1/instances/test
// handler. It owns a vanilla *http.Client (no Sonarr client wrapper —
// that one carries instance-keyed limiter state we don't want here).
type InstanceProbeHandler struct {
	client  *http.Client
	logger  *slog.Logger
	timeout time.Duration
}

// ProbeOption configures an InstanceProbeHandler at construction.
type ProbeOption func(*InstanceProbeHandler)

// WithProbeTimeout overrides the 10s default. Used by tests to drive
// the deadline branch without sleeping for real wall-clock seconds.
// Production wiring leaves the default in place.
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
	h := &InstanceProbeHandler{
		client:  client,
		logger:  logger,
		timeout: probeDefaultTimeout,
	}
	for _, o := range opts {
		o(h)
	}
	return h
}

// Test runs the one-shot reachability check.
//
// @Summary     Probe a Sonarr instance for reachability/auth
// @Tags        instances
// @Accept      json
// @Produce     json
// @Param       body  body      dto.InstanceTestRequest   true  "URL and api_key to probe"
// @Success     200   {object}  dto.InstanceTestResponse
// @Failure     400   {object}  dto.ErrorResponse
// @Failure     429   {object}  dto.ErrorResponse
// @Failure     504   {object}  dto.ErrorResponse
// @Security    CookieAuth
// @Security    ApiKeyAuth
// @Router      /instances/test [post]
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

	// The probe deadline is enforced via context, not http.Client.Timeout,
	// so a slow remote can't bypass the cap by streaming a body slowly.
	ctx, cancel := context.WithTimeout(c.Request.Context(), h.timeout)
	defer cancel()

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		c.AbortWithStatusJSON(http.StatusBadRequest,
			dto.ErrorResponse{Error: fmt.Sprintf("probe: %s", err), Code: "BAD_REQUEST"})
		return
	}
	httpReq.Header.Set("X-Api-Key", req.APIKey)
	httpReq.Header.Set("Accept", "application/json")

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

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		reason := reasonForStatus(resp.StatusCode)
		h.logger.InfoContext(ctx, "instance.probe.non_2xx",
			slog.String("event", "probe.non_2xx"),
			slog.String("instance_url", req.URL),
			slog.Int("status", resp.StatusCode))
		c.JSON(http.StatusOK, dto.InstanceTestResponse{OK: false, Reason: reason})
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
	if strings.TrimSpace(out.APIKey) == "" {
		c.AbortWithStatusJSON(http.StatusBadRequest,
			dto.ErrorResponse{Error: "api_key is required", Code: "BAD_REQUEST"})
		return out, false
	}
	return out, true
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
