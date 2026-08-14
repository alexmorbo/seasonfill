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
)

// MovieRatingsHandler serves GET /api/v1/movies/:tmdb_id/ratings.
type MovieRatingsHandler struct {
	uc  *mdapp.RatingsUseCase
	log *slog.Logger
}

// NewMovieRatingsHandler constructs the handler. log nil → default.
func NewMovieRatingsHandler(uc *mdapp.RatingsUseCase, log *slog.Logger) *MovieRatingsHandler {
	if log == nil {
		log = slog.Default()
	}
	return &MovieRatingsHandler{uc: uc, log: log}
}

// ratingStatus reports the static per-source freshness for a movie rating value:
// fresh when present, unavailable when absent. Movie ratings are read-only (no
// on-view refresh) so `revalidating` / `pending` never apply.
func ratingStatus(present bool) string {
	if present {
		return dto.RatingStatusFresh
	}
	return dto.RatingStatusUnavailable
}

// Get handles GET /api/v1/movies/:tmdb_id/ratings.
//
// @Summary     Movie ratings (TMDB + OMDb/IMDb)
// @Description Returns the ratings a movie carries on its canon row — TMDB ★ +
// @Description votes and OMDb/IMDb ★ + votes + content rating (rated) + awards.
// @Description All data is local (no live TMDB/OMDb). The movie vertical is
// @Description read-only, so each source reports fresh (value present) or
// @Description unavailable (absent); revalidating/pending never occur. There is
// @Description no Rotten Tomatoes / Metacritic field. 404 when no canon row exists.
// @Tags        movies
// @Produce     json
// @Param       tmdb_id path      int  true  "TMDB movie id"
// @Success     200     {object}  dto.MovieRatingsResponse
// @Failure     400     {object}  dto.ErrorResponse
// @Failure     401     {object}  dto.ErrorResponse
// @Failure     404     {object}  dto.ErrorResponse
// @Failure     500     {object}  dto.ErrorResponse
// @Security    CookieAuth
// @Security    ApiKeyAuth
// @Router      /movies/{tmdb_id}/ratings [get]
func (h *MovieRatingsHandler) Get(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("tmdb_id"))
	if err != nil || id <= 0 {
		c.JSON(http.StatusBadRequest, dto.ErrorResponse{Error: "invalid tmdb id", Code: "BAD_REQUEST"})
		return
	}

	page, err := h.uc.Get(c.Request.Context(), domain.TMDBID(id))
	if err != nil {
		if errors.Is(err, ports.ErrNotFound) {
			c.JSON(http.StatusNotFound, dto.ErrorResponse{Error: "movie_not_found", Code: "MOVIE_NOT_FOUND"})
			return
		}
		h.log.ErrorContext(c.Request.Context(), "movie_ratings_failed", slog.String("error", err.Error()))
		c.JSON(http.StatusInternalServerError, dto.ErrorResponse{Error: "movie ratings unavailable"})
		return
	}

	resp := dto.MovieRatingsResponse{
		TMDBRating: page.TMDBRating,
		TMDBVotes:  page.TMDBVotes,
		IMDBRating: page.IMDBRating,
		IMDBVotes:  page.IMDBVotes,
		Rated:      page.Rated,
		Awards:     page.Awards,
		Sources: dto.MovieRatingsSources{
			TMDB: ratingStatus(page.TMDBRating != nil),
			OMDb: ratingStatus(page.IMDBRating != nil),
		},
	}
	c.JSON(http.StatusOK, resp)
}
