// Package rest — seriesdetail HTTP handlers.
//
// global_series_recommendations_handler.go (Story 530 + Story 532). GET
// /api/v1/series/:id/recommendations resolves canonical series.id →
// lex-first instance → splices :name + :id → delegates to inner
// per-instance handler. When NO instance carries the series (TMDB-only
// canon row), Story 532 dispatches to
// TMDBFallbackUseCase.GetRecommendations instead of returning 404 —
// mirrors the main /series/:id fallback (Story 491). True unknown-id
// (no canon row at all) → 404 with body `{"error":"series_not_found"}`.
package rest

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	seriesdetail "github.com/alexmorbo/seasonfill/internal/seriesdetail/app"
	ports "github.com/alexmorbo/seasonfill/internal/shared/dataports"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
	"github.com/alexmorbo/seasonfill/internal/shared/http/dto"
)

// TMDBFallbackRecommendationsPort is the narrow port the wrapper
// consumes for the TMDB-only fallback. *seriesdetail.TMDBFallbackUseCase
// satisfies it. nil-OK at construction — when nil, the wrapper falls
// back to the legacy 404 "series not in any library" response.
//
// Story 565 (B-recs-lang) — lang parameter added to propagate ?lang=
// through the fallback path.
type TMDBFallbackRecommendationsPort interface {
	GetRecommendations(ctx context.Context, seriesID domain.SeriesID, lang string, limit, offset int) (*seriesdetail.Recommendations, error)
}

type GlobalSeriesRecommendationsHandler struct {
	inner        *SeriesRecommendationsHandler
	cacheLookup  seriesdetail.SeriesCacheLookupPort
	tmdbFallback TMDBFallbackRecommendationsPort
	logger       *slog.Logger
}

func NewGlobalSeriesRecommendationsHandler(
	inner *SeriesRecommendationsHandler,
	cacheLookup seriesdetail.SeriesCacheLookupPort,
	tmdbFallback TMDBFallbackRecommendationsPort,
	logger *slog.Logger,
) *GlobalSeriesRecommendationsHandler {
	if logger == nil {
		logger = slog.Default()
	}
	return &GlobalSeriesRecommendationsHandler{
		inner:        inner,
		cacheLookup:  cacheLookup,
		tmdbFallback: tmdbFallback,
		logger:       logger,
	}
}

// Get handles GET /api/v1/series/:id/recommendations.
//
// @Summary     Series recommendations carousel
// @Description Returns ONLY the recommendations slice for a series keyed by
// @Description canonical series.id. Resolves the preferred Sonarr
// @Description instance automatically (lex-first that carries the
// @Description series). When the series is TMDB-only (no library
// @Description carries it), returns a canon-only payload with
// @Description degraded=["tmdb_series"] and instance="". 404 only
// @Description when the canonical id is truly unknown.
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
		if h.tmdbFallback == nil {
			c.JSON(http.StatusNotFound, dto.ErrorResponse{Error: "series not in any library"})
			return
		}
		lang := c.Query("lang")
		limit, lok := parseRecLimit(c)
		if !lok {
			c.JSON(http.StatusBadRequest, dto.ErrorResponse{Error: "invalid limit"})
			return
		}
		offset, ook := parseRecOffset(c)
		if !ook {
			c.JSON(http.StatusBadRequest, dto.ErrorResponse{Error: "invalid offset"})
			return
		}
		rec, ferr := h.tmdbFallback.GetRecommendations(ctx, seriesID, lang, limit, offset)
		if ferr != nil {
			if errors.Is(ferr, ports.ErrNotFound) {
				c.JSON(http.StatusNotFound, dto.ErrorResponse{Error: "series_not_found"})
				return
			}
			_ = c.Error(ferr)
			return
		}
		h.logger.InfoContext(ctx, "global_series_recommendations_tmdb_fallback",
			slog.Int64("series_id", int64(seriesID)),
			slog.Int("limit", limit),
			slog.Int("offset", offset))
		c.JSON(http.StatusOK, toSeriesRecommendationsResponse(rec, limit, offset))
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
