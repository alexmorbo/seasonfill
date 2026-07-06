// Package rest — seriesdetail HTTP handlers.
//
// global_series_ratings_handler.go (W18-7a). GET /api/v1/series/:id/ratings resolves
// the canonical series.id and delegates to the SWR ratings usecase. Unlike the
// overview/cast wrappers there is NO per-instance splice — ratings live on the canon
// series row. Always answers 200 for fetch outcomes; 400 for an unparseable id; 404
// for a truly-unknown canon id (matches sibling handlers).
package rest

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	ports "github.com/alexmorbo/seasonfill/internal/shared/dataports"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
	"github.com/alexmorbo/seasonfill/internal/shared/http/dto"
)

// RatingsUseCase is the narrow port the handler consumes.
// *seriesdetail.SeriesRatingsUseCase satisfies it.
type RatingsUseCase interface {
	GetRatings(ctx context.Context, seriesID domain.SeriesID) (*dto.SeriesRatingsResponse, error)
}

type GlobalSeriesRatingsHandler struct {
	uc     RatingsUseCase
	logger *slog.Logger
}

func NewGlobalSeriesRatingsHandler(uc RatingsUseCase, logger *slog.Logger) *GlobalSeriesRatingsHandler {
	if logger == nil {
		logger = slog.Default()
	}
	return &GlobalSeriesRatingsHandler{uc: uc, logger: logger}
}

// Get handles GET /api/v1/series/:id/ratings.
//
// @Summary     Unified series ratings (stale-while-revalidate)
// @Description Returns every rating a series carries — TMDB ★ + votes, IMDb + votes,
// @Description OMDb content-rating (rated) and awards — each with a per-source
// @Description freshness status (fresh | revalidating | pending | unavailable).
// @Description Shows DB values immediately; refreshes stale/empty sources in the
// @Description background (TTL-driven) and blocks up to 3s only when a source is
// @Description empty and has an upstream id. Works for non-library series. Always
// @Description 200 for fetch outcomes; 404 only when the canonical id is unknown.
// @Tags        series
// @Produce     json
// @Param       id   path      int  true  "Canonical series.id"
// @Success     200  {object}  dto.SeriesRatingsResponse
// @Failure     400  {object}  dto.ErrorResponse
// @Failure     401  {object}  dto.ErrorResponse
// @Failure     404  {object}  dto.ErrorResponse
// @Security    CookieAuth
// @Security    ApiKeyAuth
// @Router      /series/{id}/ratings [get]
func (h *GlobalSeriesRatingsHandler) Get(c *gin.Context) {
	parsedID, err := strconv.Atoi(c.Param("id"))
	if err != nil || parsedID <= 0 {
		c.JSON(http.StatusBadRequest, dto.ErrorResponse{Error: "invalid series id"})
		return
	}
	seriesID := domain.SeriesID(parsedID)

	resp, err := h.uc.GetRatings(c.Request.Context(), seriesID)
	if err != nil {
		if errors.Is(err, ports.ErrNotFound) {
			c.JSON(http.StatusNotFound, dto.ErrorResponse{Error: "series_not_found"})
			return
		}
		// Defensive: the usecase never surfaces a fetch failure as an error, so a
		// non-NotFound error is a genuine load fault. Keep it out of 5xx per the SWR
		// contract — degrade to an all-unavailable 200 rather than failing the page.
		h.logger.WarnContext(c.Request.Context(), "ratings.load_failed",
			slog.Int64("series_id", int64(seriesID)), slog.String("error", err.Error()))
		c.JSON(http.StatusOK, &dto.SeriesRatingsResponse{
			Sources: dto.SeriesRatingsSources{
				TMDB: dto.RatingStatusUnavailable,
				OMDb: dto.RatingStatusUnavailable,
			},
		})
		return
	}
	c.JSON(http.StatusOK, resp)
}
