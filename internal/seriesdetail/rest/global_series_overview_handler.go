// Package rest — seriesdetail HTTP handlers.
//
// global_series_overview_handler.go (Story 529). GET
// /api/v1/series/:id/overview resolves canonical series.id → lex-first
// instance → splices :name + :id → delegates to inner per-instance
// handler. Mirrors global_series_cast_handler.go.
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

type GlobalSeriesOverviewHandler struct {
	inner       *SeriesOverviewHandler
	cacheLookup seriesdetail.SeriesCacheLookupPort
	logger      *slog.Logger
}

func NewGlobalSeriesOverviewHandler(
	inner *SeriesOverviewHandler,
	cacheLookup seriesdetail.SeriesCacheLookupPort,
	logger *slog.Logger,
) *GlobalSeriesOverviewHandler {
	if logger == nil {
		logger = slog.Default()
	}
	return &GlobalSeriesOverviewHandler{inner: inner, cacheLookup: cacheLookup, logger: logger}
}

// Get handles GET /api/v1/series/:id/overview.
//
// @Summary     Series overview block (description + keywords + awards)
// @Description Returns ONLY the overview slice for a series keyed by
// @Description canonical series.id. Resolves the preferred Sonarr
// @Description instance automatically (lex-first that carries the
// @Description series). 404 when no library carries the series.
// @Tags        series
// @Produce     json
// @Param       id    path      int     true   "Canonical series.id"
// @Param       lang  query     string  false  "BCP-47 language tag"
// @Success     200   {object}  dto.SeriesOverviewResponse
// @Failure     400   {object}  dto.ErrorResponse
// @Failure     401   {object}  dto.ErrorResponse
// @Failure     404   {object}  dto.ErrorResponse
// @Failure     500   {object}  dto.ErrorResponse
// @Security    CookieAuth
// @Security    ApiKeyAuth
// @Router      /series/{id}/overview [get]
func (h *GlobalSeriesOverviewHandler) Get(c *gin.Context) {
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
		c.JSON(http.StatusInternalServerError, dto.ErrorResponse{Error: "overview handler not wired"})
		return
	}

	c.Params = setParam(c.Params, "name", string(preferred.InstanceName))
	c.Params = setParam(c.Params, "id", strconv.Itoa(int(preferred.SonarrSeriesID)))
	h.inner.Get(c)
}
