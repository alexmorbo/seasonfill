package rest

import (
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	seriesdetail "github.com/alexmorbo/seasonfill/internal/seriesdetail/app"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
	"github.com/alexmorbo/seasonfill/internal/shared/http/dto"
)

// SeriesSeasonHandler serves the season-detail subset of the
// composite read (PRD §5.7).
//
// GET /api/v1/instances/:name/series/:id/season/:n?lang=
type SeriesSeasonHandler struct {
	composer *seriesdetail.Composer
	logger   *slog.Logger
}

func NewSeriesSeasonHandler(composer *seriesdetail.Composer, logger *slog.Logger) *SeriesSeasonHandler {
	if logger == nil {
		logger = slog.Default()
	}
	return &SeriesSeasonHandler{composer: composer, logger: logger}
}

// Get handles GET /api/v1/instances/:name/series/:id/season/:n.
//
// @Summary     Series season detail (single-season subset)
// @Description Returns the seasons-accordion subset of the composite
// @Description read for one season. Cheaper than the full series detail
// @Description endpoint — exists for the SPA's polling path when a
// @Description specific season is expanded and needs fresher per-instance
// @Description state without re-fetching the whole series document.
// @Tags        instances
// @Produce     json
// @Param       name  path      string  true   "Instance name"
// @Param       id    path      int     true   "Sonarr series id"
// @Param       n     path      int     true   "Season number (0 = Specials)"
// @Param       lang  query     string  false  "BCP-47 language tag (default en-US)"
// @Success     200   {object}  dto.SeasonDetailResponse
// @Failure     400   {object}  dto.ErrorResponse
// @Failure     401   {object}  dto.ErrorResponse
// @Failure     404   {object}  dto.ErrorResponse
// @Failure     500   {object}  dto.ErrorResponse
// @Security    CookieAuth
// @Security    ApiKeyAuth
// @Router      /instances/{name}/series/{id}/season/{n} [get]
func (h *SeriesSeasonHandler) Get(c *gin.Context) {
	name := c.Param("name")
	idStr := c.Param("id")
	nStr := c.Param("n")
	parsedID, err := strconv.Atoi(idStr)
	if err != nil || parsedID <= 0 {
		c.JSON(http.StatusBadRequest, dto.ErrorResponse{Error: "invalid series id"})
		return
	}
	sonarrID := domain.SonarrSeriesID(parsedID)
	seasonNumber, err := strconv.Atoi(nStr)
	if err != nil || seasonNumber < 0 {
		c.JSON(http.StatusBadRequest, dto.ErrorResponse{Error: "invalid season number"})
		return
	}
	lang := strings.TrimSpace(c.Query("lang"))

	ctx := c.Request.Context()
	detail, err := h.composer.GetSeason(ctx, domain.InstanceName(name), sonarrID, seasonNumber, lang)
	if err != nil {
		_ = c.Error(err)
		return
	}
	if len(detail.Seasons) == 0 {
		c.JSON(http.StatusNotFound, dto.ErrorResponse{Error: "season not found"})
		return
	}
	seasons := mapSeasons(detail)
	resp := dto.SeasonDetailResponse{
		Instance:       detail.Instance,
		SonarrSeriesID: detail.SonarrSeriesID,
		SeriesID:       detail.SeriesID,
		Lang:           detail.Lang,
		Season:         seasons[0],
		Degraded:       sourceStringSlice(detail.Degraded),
		SyncedAt:       detail.SyncedAt,
	}
	c.JSON(http.StatusOK, resp)
}
