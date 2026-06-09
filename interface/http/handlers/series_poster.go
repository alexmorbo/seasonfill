package handlers

import (
	"bytes"
	"errors"
	"io"
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
// upstream and Cache-Control: public, max-age=86400. ETag is
// synthesized server-side (the cache + upstream both contribute) and
// If-None-Match is honoured for a 304 fast path that skips Sonarr
// entirely.
type SeriesPosterHandler struct {
	reg    InstanceRegistry
	cache  sonarr.PosterCache
	logger *slog.Logger
}

// PosterHandlerOption configures a SeriesPosterHandler at construction.
type PosterHandlerOption func(*SeriesPosterHandler)

// WithPosterCache wires an in-process LRU cache between the handler
// and the upstream Sonarr client. Passing nil leaves the handler in
// the pre-cache passthrough mode (every request hits Sonarr).
func WithPosterCache(cache sonarr.PosterCache) PosterHandlerOption {
	return func(h *SeriesPosterHandler) { h.cache = cache }
}

func NewSeriesPosterHandler(reg InstanceRegistry, logger *slog.Logger, opts ...PosterHandlerOption) *SeriesPosterHandler {
	if logger == nil {
		logger = slog.Default()
	}
	h := &SeriesPosterHandler{reg: reg, logger: logger}
	for _, o := range opts {
		o(h)
	}
	return h
}

// Proxy handles GET /api/v1/instances/:name/series/:id/poster.
//
// @Summary     Stream a Sonarr series poster
// @Description Streams the poster image from the upstream Sonarr at
// @Description /api/v3/MediaCover/{seriesId}/poster[-500].jpg with
// @Description authentication injected server-side. size=small returns
// @Description the 500px variant; default is the full-size poster.
// @Description Responses are served from an in-process LRU cache after
// @Description the first fetch; ETag is synthesized server-side so
// @Description If-None-Match cheaply produces a 304 without touching
// @Description Sonarr.
// @Tags        instances
// @Produce     image/jpeg
// @Param       name  path      string  true   "Instance name"
// @Param       id    path      int     true   "Sonarr series id"
// @Param       size  query     string  false  "full (default) or small"
// @Success     200   {string}  binary  "poster image bytes"
// @Success     304   "not modified (when If-None-Match matched cache or upstream)"
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

	cacheKey := sonarr.PosterCacheKey(name, seriesID, size)
	if h.cache != nil {
		if entry, etag, ok := h.cache.Get(cacheKey); ok {
			h.logger.DebugContext(ctx, "series_poster_cache_hit",
				slog.String("instance", name),
				slog.Int("series_id", seriesID),
				slog.String("size", string(size)),
			)
			if ifNoneMatch != "" && ifNoneMatch == etag {
				c.Header("ETag", etag)
				c.Header("Cache-Control", "public, max-age=86400")
				c.Status(http.StatusNotModified)
				return
			}
			h.writeCachedBytes(c, entry, etag)
			return
		}
	}

	h.logger.DebugContext(ctx, "series_poster_cache_miss",
		slog.String("instance", name),
		slog.Int("series_id", seriesID),
		slog.String("size", string(size)),
	)

	// Upstream sees only If-None-Match values it could have minted —
	// our synthesized cache ETag is opaque to Sonarr, so forwarding it
	// would always miss. The cache hit path above already short-circuits
	// for matching cache ETags; we forward the raw header only when the
	// cache is disabled.
	upstreamINM := ""
	if h.cache == nil {
		upstreamINM = ifNoneMatch
	}

	resp, err := concrete.GetMediaCover(ctx, seriesID, size, upstreamINM)
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

	if h.cache == nil {
		h.streamUpstreamBody(c, resp, name, seriesID, size)
		return
	}

	body, err := readPosterBody(resp)
	if err != nil {
		h.logger.WarnContext(ctx, "series_poster_body_read_failed",
			slog.String("instance", name), slog.Int("series_id", seriesID),
			slog.String("error", err.Error()))
		c.JSON(http.StatusBadGateway, dto.ErrorResponse{Error: "sonarr unavailable"})
		return
	}

	h.cache.Put(cacheKey, body, resp.ContentType)
	entry, etag, ok := h.cache.Get(cacheKey)
	if !ok {
		// Single-blob too big for the byte cap → Put silently rejected.
		// Serve the bytes inline without claiming a stable ETag.
		etag = ""
		entry = sonarr.PosterCacheEntry{Bytes: body, ContentType: resp.ContentType}
	}

	if ifNoneMatch != "" && etag != "" && ifNoneMatch == etag {
		c.Header("ETag", etag)
		c.Header("Cache-Control", "public, max-age=86400")
		c.Status(http.StatusNotModified)
		return
	}
	h.writeCachedBytes(c, entry, etag)
}

func (h *SeriesPosterHandler) writeCachedBytes(c *gin.Context, entry sonarr.PosterCacheEntry, etag string) {
	if etag != "" {
		c.Header("ETag", etag)
	}
	c.Header("Cache-Control", "public, max-age=86400")
	ct := entry.ContentType
	if ct == "" {
		ct = "image/jpeg"
	}
	c.Data(http.StatusOK, ct, entry.Bytes)
}

func (h *SeriesPosterHandler) streamUpstreamBody(c *gin.Context, resp *sonarr.MediaCoverResponse, name string, seriesID int, size sonarr.PosterSize) {
	defer func() { _ = resp.Body.Close() }()

	extra := map[string]string{
		"Cache-Control": "public, max-age=86400",
	}
	if resp.ETag != "" {
		extra["ETag"] = resp.ETag
	}

	h.logger.DebugContext(c.Request.Context(), "series_poster_streamed",
		slog.String("instance", name),
		slog.Int("series_id", seriesID),
		slog.String("size", string(size)),
		slog.Int64("content_length", resp.ContentLength),
	)

	c.DataFromReader(http.StatusOK, resp.ContentLength, resp.ContentType, resp.Body, extra)
}

// readPosterBody drains the upstream body, capped at posterBodyMaxBytes
// so a runaway upstream can't OOM the process. The cap is generous —
// real Sonarr posters cap at a few hundred KB — but still bounded.
func readPosterBody(resp *sonarr.MediaCoverResponse) ([]byte, error) {
	defer func() { _ = resp.Body.Close() }()
	var buf bytes.Buffer
	if resp.ContentLength > 0 && resp.ContentLength < posterBodyMaxBytes {
		buf.Grow(int(resp.ContentLength))
	}
	if _, err := buf.ReadFrom(io.LimitReader(resp.Body, posterBodyMaxBytes)); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

const posterBodyMaxBytes int64 = 32 << 20 // 32 MiB hard cap per poster.
