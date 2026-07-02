// Package rest — seriesdetail HTTP handlers.
//
// series_seasons_handler.go (Story 582 / E-1 B3c). GET /api/v1/series/:id/seasons
// returns the canon list-of-seasons document (posters + counts + localized names)
// for the SPA accordion. Unlike the season/overview/cast handlers this is a DIRECT
// composer-backed handler (no inner per-instance delegate) because seasons are
// canon-level data with no per-instance Sonarr state.
package rest

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	seriesdetail "github.com/alexmorbo/seasonfill/internal/seriesdetail/app"
	ports "github.com/alexmorbo/seasonfill/internal/shared/dataports"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
	"github.com/alexmorbo/seasonfill/internal/shared/http/dto"
)

// SeasonsComposerPort is the narrow surface the handler consumes.
// *seriesdetail.SeasonsComposer satisfies it.
type SeasonsComposerPort interface {
	Compose(ctx context.Context, seriesID domain.SeriesID, lang string) (seriesdetail.SeasonsListDTO, error)
}

// SeasonsHandler exposes GET /api/v1/series/:id/seasons.
type SeasonsHandler struct {
	composer SeasonsComposerPort
	logger   *slog.Logger
}

// NewSeasonsHandler constructs the handler. logger nil-OK.
func NewSeasonsHandler(composer SeasonsComposerPort, logger *slog.Logger) *SeasonsHandler {
	if logger == nil {
		logger = slog.Default()
	}
	return &SeasonsHandler{composer: composer, logger: logger}
}

// Get handles GET /api/v1/series/:id/seasons.
//
// @Summary     Series season list (posters + counts)
// @Description Canon-level list of seasons for a series keyed by canonical
// @Description series.id — localized season names (season_texts fallback
// @Description ru-RU→en-US→canon), per-season poster hash, episode_count, and
// @Description air_date_start / air_date_end (MAX of the season's episode air
// @Description dates). No episodes embed (see /series/{id}/season/{n}); no
// @Description per-instance Sonarr state (see /series/{id}/library). 404 only when
// @Description the canonical id is unknown.
// @Tags        series
// @Produce     json
// @Param       id    path      int     true   "Canonical series.id"
// @Param       lang  query     string  false  "BCP-47 language tag (e.g. ru-RU)"
// @Success     200   {object}  dto.SeriesSeasonsResponse
// @Failure     400   {object}  dto.ErrorResponse
// @Failure     401   {object}  dto.ErrorResponse
// @Failure     404   {object}  dto.ErrorResponse
// @Failure     500   {object}  dto.ErrorResponse
// @Security    CookieAuth
// @Security    ApiKeyAuth
// @Router      /series/{id}/seasons [get]
func (h *SeasonsHandler) Get(c *gin.Context) {
	parsedID, err := strconv.Atoi(c.Param("id"))
	if err != nil || parsedID <= 0 {
		c.JSON(http.StatusBadRequest, dto.ErrorResponse{Error: "invalid series id"})
		return
	}
	seriesID := domain.SeriesID(parsedID)
	lang := strings.TrimSpace(c.Query("lang"))

	ctx := c.Request.Context()
	result, err := h.composer.Compose(ctx, seriesID, lang)
	if err != nil {
		if errors.Is(err, ports.ErrNotFound) {
			c.JSON(http.StatusNotFound, dto.ErrorResponse{Error: "series_not_found"})
			return
		}
		_ = c.Error(err) // ErrorResponseMiddleware maps to 500
		return
	}
	c.JSON(http.StatusOK, toSeriesSeasonsResponse(result))
}

func toSeriesSeasonsResponse(d seriesdetail.SeasonsListDTO) dto.SeriesSeasonsResponse {
	seasons := make([]dto.SeasonSummaryDTO, 0, len(d.Seasons))
	for i := range d.Seasons {
		s := d.Seasons[i]
		seasons = append(seasons, dto.SeasonSummaryDTO{
			SeasonNumber: s.SeasonNumber,
			Name:         s.Name,
			AirDateStart: s.AirDateStart,
			AirDateEnd:   s.AirDateEnd,
			EpisodeCount: s.EpisodeCount,
			PosterAsset:  s.PosterAsset,
			Overview:     s.Overview,
		})
	}
	return dto.SeriesSeasonsResponse{
		SeriesID: d.SeriesID,
		Seasons:  seasons,
		Degraded: d.Degraded,
		SyncedAt: d.SyncedAt,
	}
}
