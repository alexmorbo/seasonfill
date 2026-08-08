package rest

import (
	"context"
	"log/slog"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/alexmorbo/seasonfill/internal/catalog/app/gaps"
	"github.com/alexmorbo/seasonfill/internal/shared/http/dto"
)

// GapsUseCase is the narrow port the handler depends on. Production:
// *gaps.UseCase. Tests pass a real UseCase over a fake repository.
type GapsUseCase interface {
	Build(ctx context.Context, instanceFilter string) (gaps.Report, error)
}

// GapsHandler serves GET /api/v1/insights/gaps — the library-gap
// operator report.
type GapsHandler struct {
	uc     GapsUseCase
	logger *slog.Logger
}

func NewGapsHandler(uc GapsUseCase, logger *slog.Logger) *GapsHandler {
	if logger == nil {
		logger = slog.Default()
	}
	return &GapsHandler{uc: uc, logger: logger}
}

// Get handles GET /api/v1/insights/gaps.
//
// @Summary     Library gap detector
// @Description Detects library gaps — monitored, already-aired, fileless
// @Description canonical episodes (specials excluded) — per Sonarr
// @Description instance. Each instance carries an exact missing-episode
// @Description count, a whole-season-missing count, and a bounded
// @Description series → season → episode drill-down. Optional ?instance=
// @Description scopes the report to a single instance.
// @Tags        insights
// @Produce     json
// @Param       instance query string false "scope the report to a single Sonarr instance"
// @Success     200 {object} dto.GapReportDTO
// @Failure     401 {object} dto.ErrorResponse
// @Failure     500 {object} dto.ErrorResponse
// @Security    CookieAuth
// @Security    ApiKeyAuth
// @Router      /insights/gaps [get]
func (h *GapsHandler) Get(c *gin.Context) {
	rep, err := h.uc.Build(c.Request.Context(), c.Query("instance"))
	if err != nil {
		h.logger.ErrorContext(c.Request.Context(), "gaps_query_failed",
			slog.String("error", err.Error()))
		c.JSON(http.StatusInternalServerError, dto.ErrorResponse{Error: "gaps unavailable"})
		return
	}
	c.JSON(http.StatusOK, toGapReportDTO(rep))
}

func toGapReportDTO(rep gaps.Report) dto.GapReportDTO {
	instances := make([]dto.GapInstanceDTO, 0, len(rep.Instances))
	for _, gi := range rep.Instances {
		instances = append(instances, dto.GapInstanceDTO{
			InstanceName:            gi.InstanceName,
			MissingEpisodeCount:     gi.MissingEpisodeCount,
			WholeSeasonMissingCount: gi.WholeSeasonMissingCount,
			Series:                  gapSeriesDTO(gi.Series),
		})
	}
	return dto.GapReportDTO{
		GeneratedAt: rep.GeneratedAt,
		Instances:   instances,
	}
}

func gapSeriesDTO(in []gaps.GapSeries) []dto.GapSeriesDTO {
	out := make([]dto.GapSeriesDTO, 0, len(in))
	for _, s := range in {
		out = append(out, dto.GapSeriesDTO{
			SeriesID:     s.SeriesID,
			Title:        s.Title,
			MissingCount: s.MissingCount,
			Seasons:      gapSeasonsDTO(s.Seasons),
		})
	}
	return out
}

func gapSeasonsDTO(in []gaps.GapSeason) []dto.GapSeasonDTO {
	out := make([]dto.GapSeasonDTO, 0, len(in))
	for _, s := range in {
		out = append(out, dto.GapSeasonDTO{
			SeasonNumber:        s.SeasonNumber,
			MissingCount:        s.MissingCount,
			AiredMonitoredCount: s.AiredMonitoredCount,
			WholeSeasonMissing:  s.WholeSeasonMissing,
			Episodes:            gapEpisodesDTO(s.Episodes),
		})
	}
	return out
}

func gapEpisodesDTO(in []gaps.GapEpisode) []dto.GapEpisodeDTO {
	out := make([]dto.GapEpisodeDTO, 0, len(in))
	for _, e := range in {
		out = append(out, dto.GapEpisodeDTO{
			EpisodeID:     e.EpisodeID,
			SeasonNumber:  e.SeasonNumber,
			EpisodeNumber: e.EpisodeNumber,
			AirDate:       e.AirDate,
		})
	}
	return out
}
