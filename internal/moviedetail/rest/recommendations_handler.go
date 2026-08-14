package rest

import (
	"errors"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	mdapp "github.com/alexmorbo/seasonfill/internal/moviedetail/app"
	ports "github.com/alexmorbo/seasonfill/internal/shared/dataports"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
	"github.com/alexmorbo/seasonfill/internal/shared/http/dto"
	"github.com/alexmorbo/seasonfill/internal/shared/media"
)

// MovieRecommendationsHandler serves GET /api/v1/movies/:tmdb_id/recommendations.
type MovieRecommendationsHandler struct {
	uc       *mdapp.RecommendationsUseCase
	resolver *media.Resolver // nil-OK: raw poster paths flow through
	log      *slog.Logger
}

// NewMovieRecommendationsHandler constructs the handler. resolver nil-OK; log nil → default.
func NewMovieRecommendationsHandler(uc *mdapp.RecommendationsUseCase, resolver *media.Resolver, log *slog.Logger) *MovieRecommendationsHandler {
	if log == nil {
		log = slog.Default()
	}
	return &MovieRecommendationsHandler{uc: uc, resolver: resolver, log: log}
}

// Get handles GET /api/v1/movies/:tmdb_id/recommendations.
//
// @Summary     Movie recommendations ("you might also like")
// @Description Rank-ordered recommended movies for a movie keyed by TMDB id. All
// @Description data is local (no live TMDB). Each item carries tmdb_id (FE link),
// @Description title, year, poster and tmdb_rating. 404 when no canon row exists.
// @Tags        movies
// @Produce     json
// @Param       tmdb_id path      int    true  "TMDB movie id"
// @Param       limit   query     int    false "page size (1..50, default 20)"
// @Param       offset  query     int    false "page offset (>=0, default 0)"
// @Success     200     {object}  dto.MovieRecommendationsResponse
// @Failure     400     {object}  dto.ErrorResponse
// @Failure     401     {object}  dto.ErrorResponse
// @Failure     404     {object}  dto.ErrorResponse
// @Failure     500     {object}  dto.ErrorResponse
// @Security    CookieAuth
// @Security    ApiKeyAuth
// @Router      /movies/{tmdb_id}/recommendations [get]
func (h *MovieRecommendationsHandler) Get(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("tmdb_id"))
	if err != nil || id <= 0 {
		c.JSON(http.StatusBadRequest, dto.ErrorResponse{Error: "invalid tmdb id", Code: "BAD_REQUEST"})
		return
	}

	limit, ok := parseMovieRecLimit(c)
	if !ok {
		c.JSON(http.StatusBadRequest, dto.ErrorResponse{Error: "invalid limit", Code: "BAD_REQUEST"})
		return
	}
	offset, ok := parseMovieRecOffset(c)
	if !ok {
		c.JSON(http.StatusBadRequest, dto.ErrorResponse{Error: "invalid offset", Code: "BAD_REQUEST"})
		return
	}

	page, err := h.uc.Get(c.Request.Context(), domain.TMDBID(id), limit, offset)
	if err != nil {
		if errors.Is(err, ports.ErrNotFound) {
			c.JSON(http.StatusNotFound, dto.ErrorResponse{Error: "movie_not_found", Code: "MOVIE_NOT_FOUND"})
			return
		}
		h.log.ErrorContext(c.Request.Context(), "movie_recommendations_failed", slog.String("error", err.Error()))
		c.JSON(http.StatusInternalServerError, dto.ErrorResponse{Error: "movie recommendations unavailable"})
		return
	}

	resp := dto.MovieRecommendationsResponse{
		TMDBID:     page.TMDBID,
		Items:      make([]dto.MovieRecommendation, 0, len(page.Items)),
		TotalCount: page.TotalCount,
		HasMore:    page.HasMore,
		Limit:      limit,
		Offset:     offset,
		Degraded:   page.Degraded,
	}
	if resp.Degraded == nil {
		resp.Degraded = []string{}
	}
	for _, it := range page.Items {
		// TMDBID is guaranteed non-nil by the usecase (nil-tmdb recs are skipped).
		resp.Items = append(resp.Items, dto.MovieRecommendation{
			TMDBID:      *it.Canon.TMDBID,
			Title:       it.Title,
			Year:        it.Canon.Year,
			PosterAsset: it.Canon.PosterAsset,
			TMDBRating:  it.Canon.TMDBRating,
		})
	}

	// Batch-resolve poster paths → media hashes (one call). nil resolver keeps raw
	// paths. Mirrors MovieCastHandler.Get profile resolution.
	if h.resolver != nil && len(resp.Items) > 0 {
		paths := make([]*string, len(resp.Items))
		for i := range resp.Items {
			paths[i] = resp.Items[i].PosterAsset
		}
		hashes := h.resolver.ResolveBatch(c.Request.Context(), paths, "w342", "poster_w342")
		for i := range resp.Items {
			resp.Items[i].PosterAsset = hashes[i]
		}
	}

	c.JSON(http.StatusOK, resp)
}

// parseMovieRecLimit reads ?limit=N. Empty → default. Non-int / out-of-range →
// (0,false) so the handler returns 400. Mirrors seriesdetail rest parseRecLimit.
func parseMovieRecLimit(c *gin.Context) (int, bool) {
	raw := c.Query("limit")
	if raw == "" {
		return mdapp.MovieRecommendationsLimitDefault, true
	}
	n, err := strconv.Atoi(raw)
	if err != nil {
		return 0, false
	}
	if n < mdapp.MovieRecommendationsLimitMin || n > mdapp.MovieRecommendationsLimitMax {
		return 0, false
	}
	return n, true
}

// parseMovieRecOffset reads ?offset=N. Empty → 0. Non-int / negative → (0,false).
func parseMovieRecOffset(c *gin.Context) (int, bool) {
	raw := c.Query("offset")
	if raw == "" {
		return 0, true
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 0 {
		return 0, false
	}
	return n, true
}
