package handlers

import (
	"errors"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"github.com/alexmorbo/seasonfill/domain"
	"github.com/alexmorbo/seasonfill/infrastructure/sonarr"
	"github.com/alexmorbo/seasonfill/interface/http/dto"
)

// SeriesPosterHandler proxies Sonarr's MediaCover poster image so the
// browser can load it without knowing the instance URL or API key.
//
// GET /api/v1/instances/:name/series/:id/poster[?size=full|small]
//
// On success streams the image body with Content-Type forwarded from
// upstream and Cache-Control: public, max-age=86400. ETag is forwarded
// when present and If-None-Match is round-tripped so the browser can
// cheaply 304.
type SeriesPosterHandler struct {
	reg    InstanceRegistry
	logger *slog.Logger
}

func NewSeriesPosterHandler(reg InstanceRegistry, logger *slog.Logger) *SeriesPosterHandler {
	if logger == nil {
		logger = slog.Default()
	}
	return &SeriesPosterHandler{reg: reg, logger: logger}
}

// Proxy handles GET /api/v1/instances/:name/series/:id/poster.
//
// @Summary     Stream a Sonarr series poster
// @Description Streams the poster image from the upstream Sonarr at
// @Description /api/v3/MediaCover/{seriesId}/poster[-500].jpg with
// @Description authentication injected server-side. size=small returns
// @Description the 500px variant; default is the full-size poster.
// @Tags        instances
// @Produce     image/jpeg
// @Param       name  path      string  true   "Instance name"
// @Param       id    path      int     true   "Sonarr series id"
// @Param       size  query     string  false  "full (default) or small"
// @Success     200   {string}  binary  "poster image bytes"
// @Success     304   "not modified (when If-None-Match matched upstream)"
// @Failure     400   {object}  dto.ErrorResponse
// @Failure     401   {object}  dto.ErrorResponse
// @Failure     404   {object}  dto.ErrorResponse  "unknown instance or poster missing"
// @Failure     502   {object}  dto.ErrorResponse
// @Security    CookieAuth
// @Security    ApiKeyAuth
// @Router      /instances/{name}/series/{id}/poster [get]
func (h *SeriesPosterHandler) Proxy(c *gin.Context) {
	name := c.Param("name")
	inst, ok := h.reg.snapshot()[name]
	if !ok || inst.Client == nil {
		c.JSON(http.StatusNotFound, dto.ErrorResponse{Error: "unknown instance: " + name})
		return
	}

	idStr := c.Param("id")
	seriesID, err := strconv.Atoi(idStr)
	if err != nil || seriesID <= 0 {
		c.JSON(http.StatusBadRequest, dto.ErrorResponse{Error: "invalid series id"})
		return
	}

	size := sonarr.PosterFull
	if c.Query("size") == "small" {
		size = sonarr.PosterSmall
	}

	concrete, ok := inst.Client.(*sonarr.Client)
	if !ok {
		writeInternalError(c, h.logger, "series_poster_client_type_mismatch",
			errors.New("instance client is not *sonarr.Client"),
			slog.String("instance", name))
		return
	}

	ctx := c.Request.Context()
	ifNoneMatch := c.GetHeader("If-None-Match")

	resp, err := concrete.GetMediaCover(ctx, seriesID, size, ifNoneMatch)
	if err != nil {
		var se *sonarr.StatusError
		if errors.As(err, &se) && se.Status == http.StatusNotFound {
			c.JSON(http.StatusNotFound, dto.ErrorResponse{Error: "poster not found"})
			return
		}
		if errors.Is(err, domain.ErrInstanceUnauthorized) {
			h.logger.WarnContext(ctx, "series_poster_upstream_unauthorized",
				slog.String("instance", name), slog.Int("series_id", seriesID),
				slog.String("error", err.Error()))
			c.JSON(http.StatusBadGateway, dto.ErrorResponse{Error: "sonarr unauthorized"})
			return
		}
		h.logger.WarnContext(ctx, "series_poster_upstream_failed",
			slog.String("instance", name), slog.Int("series_id", seriesID),
			slog.String("error", err.Error()))
		c.JSON(http.StatusBadGateway, dto.ErrorResponse{Error: "sonarr unavailable"})
		return
	}

	if resp.NotModified {
		if resp.ETag != "" {
			c.Header("ETag", resp.ETag)
		}
		c.Header("Cache-Control", "public, max-age=86400")
		c.Status(http.StatusNotModified)
		return
	}

	defer func() { _ = resp.Body.Close() }()

	extra := map[string]string{
		"Cache-Control": "public, max-age=86400",
	}
	if resp.ETag != "" {
		extra["ETag"] = resp.ETag
	}

	h.logger.DebugContext(ctx, "series_poster_proxied",
		slog.String("instance", name),
		slog.Int("series_id", seriesID),
		slog.String("size", string(size)),
		slog.Int64("content_length", resp.ContentLength),
	)

	c.DataFromReader(http.StatusOK, resp.ContentLength, resp.ContentType, resp.Body, extra)
}
