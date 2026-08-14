package rest

import (
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	mdapp "github.com/alexmorbo/seasonfill/internal/moviedetail/app"
	ports "github.com/alexmorbo/seasonfill/internal/shared/dataports"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
	"github.com/alexmorbo/seasonfill/internal/shared/http/dto"
)

// MovieOverviewHandler serves GET /api/v1/movies/:tmdb_id/overview.
type MovieOverviewHandler struct {
	uc  *mdapp.OverviewUseCase
	log *slog.Logger
}

// NewMovieOverviewHandler constructs the handler. log nil → default.
func NewMovieOverviewHandler(uc *mdapp.OverviewUseCase, log *slog.Logger) *MovieOverviewHandler {
	if log == nil {
		log = slog.Default()
	}
	return &MovieOverviewHandler{uc: uc, log: log}
}

// Get handles GET /api/v1/movies/:tmdb_id/overview.
//
// @Summary     Movie overview block (localized title/overview/tagline)
// @Description Returns ONLY the localized text slice for a movie keyed by TMDB
// @Description id: title (localized > canon), overview and tagline. All data is
// @Description local (no live TMDB). served_language reports the language the
// @Description title resolved to; degraded=["missing_lang"] when a fallback
// @Description language was served. 404 when no canon row exists.
// @Tags        movies
// @Produce     json
// @Param       tmdb_id path      int    true  "TMDB movie id"
// @Param       lang    query     string false "BCP-47 language tag"
// @Success     200     {object}  dto.MovieOverviewResponse
// @Failure     400     {object}  dto.ErrorResponse
// @Failure     401     {object}  dto.ErrorResponse
// @Failure     404     {object}  dto.ErrorResponse
// @Failure     500     {object}  dto.ErrorResponse
// @Security    CookieAuth
// @Security    ApiKeyAuth
// @Router      /movies/{tmdb_id}/overview [get]
func (h *MovieOverviewHandler) Get(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("tmdb_id"))
	if err != nil || id <= 0 {
		c.JSON(http.StatusBadRequest, dto.ErrorResponse{Error: "invalid tmdb id", Code: "BAD_REQUEST"})
		return
	}
	lang := strings.TrimSpace(c.Query("lang"))

	page, err := h.uc.Get(c.Request.Context(), domain.TMDBID(id), lang)
	if err != nil {
		if errors.Is(err, ports.ErrNotFound) {
			c.JSON(http.StatusNotFound, dto.ErrorResponse{Error: "movie_not_found", Code: "MOVIE_NOT_FOUND"})
			return
		}
		h.log.ErrorContext(c.Request.Context(), "movie_overview_failed", slog.String("error", err.Error()))
		c.JSON(http.StatusInternalServerError, dto.ErrorResponse{Error: "movie overview unavailable"})
		return
	}

	resp := dto.MovieOverviewResponse{
		TMDBID:         page.TMDBID,
		Lang:           page.Lang,
		Title:          page.Title,
		Overview:       page.Overview,
		Tagline:        page.Tagline,
		ServedLanguage: page.ServedLanguage,
		Degraded:       page.Degraded,
	}
	if resp.Degraded == nil {
		resp.Degraded = []string{}
	}
	c.JSON(http.StatusOK, resp)
}
