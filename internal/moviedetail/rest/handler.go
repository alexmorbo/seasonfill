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

// Handler serves GET /api/v1/movies/:tmdb_id.
type Handler struct {
	uc  *mdapp.UseCase
	log *slog.Logger
}

// NewHandler constructs the movie-detail REST handler.
func NewHandler(uc *mdapp.UseCase, log *slog.Logger) *Handler {
	if log == nil {
		log = slog.Default()
	}
	return &Handler{uc: uc, log: log}
}

// Get handles GET /api/v1/movies/:tmdb_id.
//
// @Summary     Movie detail aggregate
// @Description Returns the movie detail keyed by TMDB id: canon + localized
// @Description title/overview + franchise collection + per-instance Radarr
// @Description library membership. All data is local (no live TMDB). 404 when
// @Description no canon row exists for the tmdb id.
// @Tags        movies
// @Produce     json
// @Param       tmdb_id path      int    true  "TMDB movie id"
// @Param       lang    query     string false "BCP-47 language tag"
// @Success     200     {object}  dto.MovieDetailResponse
// @Failure     400     {object}  dto.ErrorResponse
// @Failure     401     {object}  dto.ErrorResponse
// @Failure     404     {object}  dto.ErrorResponse
// @Failure     500     {object}  dto.ErrorResponse
// @Security    CookieAuth
// @Security    ApiKeyAuth
// @Router      /movies/{tmdb_id} [get]
func (h *Handler) Get(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("tmdb_id"))
	if err != nil || id <= 0 {
		c.JSON(http.StatusBadRequest, dto.ErrorResponse{Error: "invalid tmdb id", Code: "BAD_REQUEST"})
		return
	}
	lang := strings.TrimSpace(c.Query("lang"))
	d, err := h.uc.Get(c.Request.Context(), domain.TMDBID(id), lang)
	if err != nil {
		if errors.Is(err, ports.ErrNotFound) {
			c.JSON(http.StatusNotFound, dto.ErrorResponse{Error: "movie_not_found", Code: "MOVIE_NOT_FOUND"})
			return
		}
		h.log.ErrorContext(c.Request.Context(), "movie_detail_failed", slog.String("error", err.Error()))
		c.JSON(http.StatusInternalServerError, dto.ErrorResponse{Error: "movie detail unavailable"})
		return
	}
	c.JSON(http.StatusOK, toMovieDetailResponse(d))
}

func toMovieDetailResponse(d mdapp.Detail) dto.MovieDetailResponse {
	out := dto.MovieDetailResponse{
		Title:      d.Title,
		Overview:   d.Overview,
		Tagline:    d.Tagline,
		Year:       d.Canon.Year,
		Status:     d.Canon.Status,
		Runtime:    d.Canon.RuntimeMinutes,
		Poster:     d.Poster,
		Backdrop:   d.Backdrop,
		Released:   d.Canon.ReleaseDate,
		Digital:    d.Canon.DigitalReleaseDate,
		Physical:   d.Canon.PhysicalReleaseDate,
		TMDBRating: d.Canon.TMDBRating,
		IMDBRating: d.Canon.IMDBRating,
		Degraded:   d.Degraded,
	}
	if d.Canon.TMDBID != nil {
		out.TMDBID = int(*d.Canon.TMDBID)
	}
	if d.Canon.IMDBID != nil {
		s := string(*d.Canon.IMDBID)
		out.IMDBID = &s
	}
	if d.Collection != nil {
		out.Collection = &dto.MovieDetailCollection{
			TMDBCollectionID: d.Collection.TMDBCollectionID,
			Name:             d.Collection.Name,
			Poster:           d.Collection.PosterAsset,
			RadarrMonitored:  d.Collection.RadarrMonitored,
		}
	}
	for _, m := range d.Library {
		out.Library = append(out.Library, dto.MovieDetailLibrary{
			InstanceName:  m.InstanceName,
			RadarrMovieID: m.RadarrMovieID,
			Monitored:     m.Monitored,
			HasFile:       m.HasFile,
			Availability:  m.Availability,
			SizeOnDisk:    m.SizeOnDisk,
		})
	}
	return out
}
