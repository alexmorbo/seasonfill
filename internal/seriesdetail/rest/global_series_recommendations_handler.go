// Package rest — seriesdetail HTTP handlers.
//
// global_series_recommendations_handler.go (Story 530). GET
// /api/v1/series/:id/recommendations resolves canonical series.id → lex-first
// instance → splices :name + :id → delegates to inner per-instance
// handler. Mirrors global_series_overview_handler.go.
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

type GlobalSeriesRecommendationsHandler struct {
	inner       *SeriesRecommendationsHandler
	cacheLookup seriesdetail.SeriesCacheLookupPort
	logger      *slog.Logger
}

func NewGlobalSeriesRecommendationsHandler(
	inner *SeriesRecommendationsHandler,
	cacheLookup seriesdetail.SeriesCacheLookupPort,
	logger *slog.Logger,
) *GlobalSeriesRecommendationsHandler {
	if logger == nil {
		logger = slog.Default()
	}
	return &GlobalSeriesRecommendationsHandler{inner: inner, cacheLookup: cacheLookup, logger: logger}
}

// Get handles GET /api/v1/series/:id/recommendations.
//
// @Summary     Series recommendations carousel
// @Description Returns ONLY the recommendations slice for a series keyed by
// @Description canonical series.id. Resolves the preferred Sonarr
// @Description instance automatically (lex-first that carries the
// @Description series). 404 when no library carries the series.
// @Tags        series
// @Produce     json
// @Param       id      path      int     true   "Canonical series.id"
// @Param       limit   query     int     false  "Page size (1..50, default 20)"
// @Param       offset  query     int     false  "Offset (>=0, default 0)"
// @Success     200     {object}  dto.SeriesRecommendationsResponse
// @Failure     400     {object}  dto.ErrorResponse
// @Failure     401     {object}  dto.ErrorResponse
// @Failure     404     {object}  dto.ErrorResponse
// @Failure     500     {object}  dto.ErrorResponse
// @Security    CookieAuth
// @Security    ApiKeyAuth
// @Router      /series/{id}/recommendations [get]
func (h *GlobalSeriesRecommendationsHandler) Get(c *gin.Context) {
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
		c.JSON(http.StatusInternalServerError, dto.ErrorResponse{Error: "recommendations handler not wired"})
		return
	}

	c.Params = setParam(c.Params, "name", string(preferred.InstanceName))
	c.Params = setParam(c.Params, "id", strconv.Itoa(int(preferred.SonarrSeriesID)))
	h.inner.Get(c)
}
