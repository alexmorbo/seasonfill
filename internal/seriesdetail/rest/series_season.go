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

// DEAD: per-instance route deleted at N-1b cutover (story 492). Function retained for future cleanup sweep.
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
		ServedLanguage: detail.ServedLanguage,
		Degraded:       seriesdetail.AppendMissingLang(sourceStringSlice(detail.Degraded), detail.ServedLanguage, detail.Lang),
		SyncedAt:       detail.SyncedAt,
	}
	c.JSON(http.StatusOK, resp)
}
