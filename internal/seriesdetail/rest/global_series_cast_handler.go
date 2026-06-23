// Package rest — seriesdetail HTTP handlers.
//
// global_series_cast_handler.go (Story 492 / N-1b). GET
// /api/v1/series/:id/cast resolves the canonical series.id to the
// preferred Sonarr instance via the cacheLookup (lex-first instance
// that carries this series), then delegates to the existing
// per-instance SeriesCastHandler by splicing :name + :id into c.Params
// and invoking it. 404 when the series is in zero libraries (cast is
// library-derived; TMDB-only series have no Sonarr-side cast surface
// in v1). The legacy /api/v1/instances/:name/series/:id/cast route is
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

// GlobalSeriesCastHandler exposes GET /api/v1/series/:id/cast.
type GlobalSeriesCastHandler struct {
	inner       *SeriesCastHandler
	cacheLookup seriesdetail.SeriesCacheLookupPort
	logger      *slog.Logger
}

// NewGlobalSeriesCastHandler constructs the wrapper. inner is the
// existing per-instance handler (its route registration drops in this
// story; only this wrapper reaches it). logger nil-OK.
func NewGlobalSeriesCastHandler(
	inner *SeriesCastHandler,
	cacheLookup seriesdetail.SeriesCacheLookupPort,
	logger *slog.Logger,
) *GlobalSeriesCastHandler {
	if logger == nil {
		logger = slog.Default()
	}
	return &GlobalSeriesCastHandler{inner: inner, cacheLookup: cacheLookup, logger: logger}
}

// Get handles GET /api/v1/series/:id/cast.
//
// @Summary     Full series cast & crew (global)
// @Description Same shape as the per-instance
// @Description /api/v1/instances/{name}/series/{id}/cast, but resolves
// @Description the preferred Sonarr instance automatically from the
// @Description canonical series.id (lex-first instance that carries
// @Description the series in series_cache). 404 when no library
// @Description carries the series — TMDB-only series have no cast
// @Description surface in v1.
// @Tags        series
// @Produce     json
// @Param       id    path      int     true   "Canonical series.id"
// @Param       lang  query     string  false  "BCP-47 language tag (default en-US)"
// @Success     200   {object}  dto.SeriesCastResponse
// @Failure     400   {object}  dto.ErrorResponse
// @Failure     401   {object}  dto.ErrorResponse
// @Failure     404   {object}  dto.ErrorResponse
// @Failure     500   {object}  dto.ErrorResponse
// @Security    CookieAuth
// @Security    ApiKeyAuth
// @Router      /series/{id}/cast [get]
func (h *GlobalSeriesCastHandler) Get(c *gin.Context) {
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
		c.JSON(http.StatusInternalServerError, dto.ErrorResponse{Error: "cast handler not wired"})
		return
	}

	// Splice :name + :id into c.Params so the inner per-instance
	// handler reads them via its existing c.Param lookups. setParam
	// REPLACES :id (gin's Params.Get returns the FIRST match so a plain
	// append on `id` would leave the inner handler reading the canonical
	// series.id from the URL instead of the per-instance Sonarr id).
	c.Params = setParam(c.Params, "name", string(preferred.InstanceName))
	c.Params = setParam(c.Params, "id", strconv.Itoa(int(preferred.SonarrSeriesID)))
	h.inner.Get(c)
}
