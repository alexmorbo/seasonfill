package rest

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/alexmorbo/seasonfill/internal/shared/clients/sonarr"
	shareddomain "github.com/alexmorbo/seasonfill/internal/shared/domain"
	"github.com/alexmorbo/seasonfill/internal/shared/http/dto"
)

// metadataProbeInstanceName is the placeholder name stamped on the transient
// Sonarr client used by the stateless metadata endpoint. No instance row of
// this name exists — it only labels the client's logs.
const metadataProbeInstanceName = shareddomain.InstanceName("__metadata_probe__")

// Metadata is the stateless POST /api/v1/admin/instances/metadata handler
// (ADR-0009 S6). It builds a transient Sonarr client from the posted
// {url, api_key} — no instance row required — and returns that Sonarr's
// quality profiles + root folders so the InstanceFormDialog can populate the
// Add-to-Sonarr default pickers in both create and edit flows. Mirrors the
// connectivity probe's body validation and timeout; a Sonarr error surfaces as
// 502 SONARR_UNREACHABLE so the FE can leave the dropdowns empty.
//
// @Summary     List a Sonarr instance's quality profiles and root folders (stateless)
// @Description Builds a transient Sonarr client from the posted url+api_key (no stored instance required) and returns quality profiles + root folders for the Add-to-Sonarr default pickers.
// @Tags        instances
// @Accept      json
// @Produce     json
// @Param       body  body      dto.InstanceTestRequest         true  "URL and api_key of the Sonarr to introspect"
// @Success     200   {object}  dto.InstanceMetadataResponse
// @Failure     400   {object}  dto.ErrorResponse
// @Failure     404   {object}  dto.ErrorResponse  "STORED_KEY_NOT_FOUND — name given but no stored key"
// @Failure     429   {object}  dto.ErrorResponse
// @Failure     502   {object}  dto.ErrorResponse
// @Security    CookieAuth
// @Security    ApiKeyAuth
// @Router      /admin/instances/metadata [post]
func (h *InstanceProbeHandler) Metadata(c *gin.Context) {
	req, ok := h.readBody(c)
	if !ok {
		return
	}
	base, err := sanitizeInstanceBaseURL(req.URL)
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

	client := sonarr.New(metadataProbeInstanceName, base, apiKey, h.timeout, h.logger)

	profiles, err := client.ListQualityProfiles(ctx)
	if err != nil {
		h.logMetadataUpstreamErr(ctx, req.URL, "quality_profiles", err)
		c.AbortWithStatusJSON(http.StatusBadGateway,
			dto.ErrorResponse{Error: "sonarr unreachable: " + err.Error(), Code: "SONARR_UNREACHABLE"})
		return
	}
	folders, err := client.ListRootFolders(ctx)
	if err != nil {
		h.logMetadataUpstreamErr(ctx, req.URL, "root_folders", err)
		c.AbortWithStatusJSON(http.StatusBadGateway,
			dto.ErrorResponse{Error: "sonarr unreachable: " + err.Error(), Code: "SONARR_UNREACHABLE"})
		return
	}

	qps := make([]dto.InstanceMetadataQualityProfile, 0, len(profiles))
	for _, p := range profiles {
		qps = append(qps, dto.InstanceMetadataQualityProfile{ID: p.ID, Name: p.Name})
	}
	rfs := make([]dto.InstanceMetadataRootFolder, 0, len(folders))
	for _, f := range folders {
		rfs = append(rfs, dto.InstanceMetadataRootFolder{
			ID: f.ID, Path: f.Path, Accessible: f.Accessible, FreeSpace: f.FreeSpace,
		})
	}

	h.logger.InfoContext(ctx, "instance.metadata.ok",
		slog.String("event", "metadata.ok"),
		slog.String("instance_url", req.URL),
		slog.Int("quality_profiles", len(qps)),
		slog.Int("root_folders", len(rfs)))
	c.JSON(http.StatusOK, dto.InstanceMetadataResponse{QualityProfiles: qps, RootFolders: rfs})
}

func (h *InstanceProbeHandler) logMetadataUpstreamErr(ctx context.Context, instanceURL, stage string, err error) {
	h.logger.WarnContext(ctx, "instance.metadata.upstream_error",
		slog.String("event", "metadata.upstream_error"),
		slog.String("instance_url", instanceURL),
		slog.String("stage", stage),
		slog.String("error", err.Error()))
}

// sanitizeInstanceBaseURL validates and normalises the posted URL down to a
// scheme://host[:port] base (no trailing slash, no path suffix) suitable for
// sonarr.New — which appends /api/v3/... itself. Mirrors validateProbeURL's
// scheme allow-list / no-userinfo rules but returns the bare base instead of
// the /system/status probe path.
func sanitizeInstanceBaseURL(raw string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return "", fmt.Errorf("metadata: %w", err)
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
	return strings.TrimRight(u.String(), "/"), nil
}
