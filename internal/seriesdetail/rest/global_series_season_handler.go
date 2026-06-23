// Package rest — seriesdetail HTTP handlers.
//
// global_series_season_handler.go (Story 492 / N-1b). GET
// /api/v1/series/:id/season/:n resolves the canonical series.id to the
// preferred Sonarr instance via the cacheLookup (lex-first instance
// that carries this series), then delegates to the existing
// per-instance SeriesSeasonHandler by splicing :name + :id into
// c.Params and invoking it. 404 when the series is in zero libraries.
// The legacy /api/v1/instances/:name/series/:id/season/:n route is
// DELETED in this same story.
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

// GlobalSeriesSeasonHandler exposes GET /api/v1/series/:id/season/:n.
type GlobalSeriesSeasonHandler struct {
	inner       *SeriesSeasonHandler
	cacheLookup seriesdetail.SeriesCacheLookupPort
	logger      *slog.Logger
}

// NewGlobalSeriesSeasonHandler constructs the wrapper. inner is the
// existing per-instance handler (its route registration drops in this
// story; only this wrapper reaches it). logger nil-OK.
func NewGlobalSeriesSeasonHandler(
	inner *SeriesSeasonHandler,
	cacheLookup seriesdetail.SeriesCacheLookupPort,
	logger *slog.Logger,
) *GlobalSeriesSeasonHandler {
	if logger == nil {
		logger = slog.Default()
	}
	return &GlobalSeriesSeasonHandler{inner: inner, cacheLookup: cacheLookup, logger: logger}
}

// Get handles GET /api/v1/series/:id/season/:n.
//
// @Summary     Series season detail (global)
// @Description Season detail document for a series keyed by canonical
// @Description series.id. Resolves the preferred Sonarr instance
// @Description automatically (lex-first instance that carries the series
// @Description in series_cache). 404 when no library carries the series —
// @Description TMDB-only series have no per-season surface.
// @Tags        series
// @Produce     json
// @Param       id    path      int     true   "Canonical series.id"
// @Param       n     path      int     true   "Season number (0 = Specials)"
// @Param       lang  query     string  false  "BCP-47 language tag (default en-US)"
// @Success     200   {object}  dto.SeasonDetailResponse
// @Failure     400   {object}  dto.ErrorResponse
// @Failure     401   {object}  dto.ErrorResponse
// @Failure     404   {object}  dto.ErrorResponse
// @Failure     500   {object}  dto.ErrorResponse
// @Security    CookieAuth
// @Security    ApiKeyAuth
// @Router      /series/{id}/season/{n} [get]
func (h *GlobalSeriesSeasonHandler) Get(c *gin.Context) {
	parsedID, err := strconv.Atoi(c.Param("id"))
	if err != nil || parsedID <= 0 {
		c.JSON(http.StatusBadRequest, dto.ErrorResponse{Error: "invalid series id"})
		return
	}
	seriesID := domain.SeriesID(parsedID)
	// Pre-validate :n so the wrapper fails fast on a malformed season
	// number before doing a cache lookup. The inner handler validates
	// again — kept defensive on both sides.
	if n, err := strconv.Atoi(c.Param("n")); err != nil || n < 0 {
		c.JSON(http.StatusBadRequest, dto.ErrorResponse{Error: "invalid season number"})
		return
	}

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
		c.JSON(http.StatusInternalServerError, dto.ErrorResponse{Error: "season handler not wired"})
		return
	}
	// :n already in c.Params from gin — leave untouched. :id needs
	// rewriting (canonical → per-instance sonarr id); :name needs
	// splicing.
	c.Params = setParam(c.Params, "name", string(preferred.InstanceName))
	c.Params = setParam(c.Params, "id", strconv.Itoa(int(preferred.SonarrSeriesID)))
	h.inner.Get(c)
}
