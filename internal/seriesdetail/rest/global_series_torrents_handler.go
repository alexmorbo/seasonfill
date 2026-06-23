// Package rest — seriesdetail HTTP handlers.
//
// global_series_torrents_handler.go (Story 492 / N-1b). GET
// /api/v1/series/:id/torrents resolves the canonical series.id to the
// preferred Sonarr instance via the cacheLookup, then delegates to the
// existing per-instance SeriesTorrentsHandler by splicing :name + :id
// into c.Params and invoking it. 404 when the series is in zero
// libraries. The legacy
// /api/v1/instances/:name/series/:id/torrents route is DELETED in
// this same story.
package rest

import (
	"log/slog"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	seriesdetail "github.com/alexmorbo/seasonfill/internal/seriesdetail/app"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
	"github.com/alexmorbo/seasonfill/internal/shared/http/dto"
)

// GlobalSeriesTorrentsHandler exposes GET /api/v1/series/:id/torrents.
type GlobalSeriesTorrentsHandler struct {
	inner       *SeriesTorrentsHandler
	cacheLookup seriesdetail.SeriesCacheLookupPort
	logger      *slog.Logger
}

// NewGlobalSeriesTorrentsHandler constructs the wrapper. inner is the
// existing per-instance handler (its route registration drops in this
// story; only this wrapper reaches it). logger nil-OK.
func NewGlobalSeriesTorrentsHandler(
	inner *SeriesTorrentsHandler,
	cacheLookup seriesdetail.SeriesCacheLookupPort,
	logger *slog.Logger,
) *GlobalSeriesTorrentsHandler {
	if logger == nil {
		logger = slog.Default()
	}
	return &GlobalSeriesTorrentsHandler{inner: inner, cacheLookup: cacheLookup, logger: logger}
}

// Get handles GET /api/v1/series/:id/torrents.
//
// @Summary     Per-series torrent inventory (global)
// @Description Per-series torrent inventory keyed by canonical series.id.
// @Description Resolves the preferred Sonarr instance automatically
// @Description (lex-first instance that carries the series in
// @Description series_cache). 404 when no library carries the series —
// @Description TMDB-only series have no torrent surface.
// @Tags        series
// @Produce     json
// @Param       id  path  int  true  "Canonical series.id"
// @Success     200 {object} dto.SeriesTorrentsResponse
// @Success     304 "not modified — If-None-Match matched the current ETag"
// @Failure     400 {object} dto.ErrorResponse
// @Failure     401 {object} dto.ErrorResponse
// @Failure     404 {object} dto.ErrorResponse
// @Failure     500 {object} dto.ErrorResponse
// @Security    CookieAuth
// @Security    ApiKeyAuth
// @Router      /series/{id}/torrents [get]
func (h *GlobalSeriesTorrentsHandler) Get(c *gin.Context) {
	parsedID, err := strconv.Atoi(c.Param("id"))
	if err != nil || parsedID <= 0 {
		c.JSON(http.StatusBadRequest, dto.ErrorResponse{Error: "invalid series id"})
		return
	}
	seriesID := domain.SeriesID(parsedID)

	ctx := c.Request.Context()
	preferred, ok, err := resolvePreferredCacheEntry(ctx, h.cacheLookup, seriesID)
	if err != nil {
		_ = c.Error(err)
		return
	}
	if !ok {
		c.JSON(http.StatusNotFound, dto.ErrorResponse{Error: "series not in any library"})
		return
	}
	if h.inner == nil {
		c.JSON(http.StatusInternalServerError, dto.ErrorResponse{Error: "torrents handler not wired"})
		return
	}
	c.Params = setParam(c.Params, "name", string(preferred.InstanceName))
	c.Params = setParam(c.Params, "id", strconv.Itoa(int(preferred.SonarrSeriesID)))
	h.inner.Get(c)
}
