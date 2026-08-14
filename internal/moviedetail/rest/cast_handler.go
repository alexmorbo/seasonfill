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
	"github.com/alexmorbo/seasonfill/internal/shared/media"
)

// MovieCastHandler serves GET /api/v1/movies/:tmdb_id/cast.
type MovieCastHandler struct {
	uc       *mdapp.CastUseCase
	resolver *media.Resolver // nil-OK: raw profile paths flow through
	log      *slog.Logger
}

// NewMovieCastHandler constructs the handler. resolver nil-OK; log nil → default.
func NewMovieCastHandler(uc *mdapp.CastUseCase, resolver *media.Resolver, log *slog.Logger) *MovieCastHandler {
	if log == nil {
		log = slog.Default()
	}
	return &MovieCastHandler{uc: uc, resolver: resolver, log: log}
}

// Get handles GET /api/v1/movies/:tmdb_id/cast.
//
// @Summary     Movie cast list
// @Description Full cast for a movie keyed by TMDB id. All data is local (no
// @Description live TMDB). Default sort is credit_order ASC (?sort=name switches
// @Description to localized name collation). 404 when no canon row exists.
// @Tags        movies
// @Produce     json
// @Param       tmdb_id path      int    true  "TMDB movie id"
// @Param       lang    query     string false "BCP-47 language tag"
// @Param       sort    query     string false "credit (default) | name"
// @Success     200     {object}  dto.MovieCastResponse
// @Failure     400     {object}  dto.ErrorResponse
// @Failure     401     {object}  dto.ErrorResponse
// @Failure     404     {object}  dto.ErrorResponse
// @Failure     500     {object}  dto.ErrorResponse
// @Security    CookieAuth
// @Security    ApiKeyAuth
// @Router      /movies/{tmdb_id}/cast [get]
func (h *MovieCastHandler) Get(c *gin.Context) {
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
		h.log.ErrorContext(c.Request.Context(), "movie_cast_failed", slog.String("error", err.Error()))
		c.JSON(http.StatusInternalServerError, dto.ErrorResponse{Error: "movie cast unavailable"})
		return
	}

	resp := dto.MovieCastResponse{
		TMDBID:         page.TMDBID,
		Lang:           page.Lang,
		Cast:           make([]dto.MovieCastMember, 0, len(page.Cast)),
		ServedLanguage: page.ServedLanguage,
		Degraded:       page.Degraded,
	}
	if resp.Degraded == nil {
		resp.Degraded = []string{}
	}
	for _, e := range page.Cast {
		resp.Cast = append(resp.Cast, dto.MovieCastMember{
			PersonID:      e.Person.ID,
			TMDBID:        e.Person.TMDBID,
			Name:          e.Person.Name,
			ProfileAsset:  e.Person.ProfileAsset,
			CharacterName: e.CharacterName,
			CreditOrder:   e.CreditOrder,
		})
	}

	// Batch-resolve profile paths → media hashes (one call). nil resolver keeps
	// raw paths (pre-M-FIX-1 behaviour).
	if h.resolver != nil && len(resp.Cast) > 0 {
		paths := make([]*string, len(resp.Cast))
		for i := range resp.Cast {
			paths[i] = resp.Cast[i].ProfileAsset
		}
		hashes := h.resolver.ResolveBatch(c.Request.Context(), paths, "w185", "profile_w185")
		for i := range resp.Cast {
			resp.Cast[i].ProfileAsset = hashes[i]
		}
	}

	// Server-side sort (default credit_order ASC). resp.Lang drives name collation.
	sortMovieCast(resp.Cast, parseMovieCastSort(c), resp.Lang)
	c.JSON(http.StatusOK, resp)
}
