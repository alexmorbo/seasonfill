package rest

import (
	"context"
	"log/slog"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/alexmorbo/seasonfill/internal/catalog/app/smartlists"
	"github.com/alexmorbo/seasonfill/internal/shared/http/dto"
)

// SmartListsUseCase is the narrow port the handler depends on. Production:
// *smartlists.UseCase. Tests pass a real UseCase over a fake repository.
type SmartListsUseCase interface {
	Build(ctx context.Context, instanceFilter string) (smartlists.Report, error)
}

// SmartListsHandler serves GET /api/v1/insights/lists — the read-only
// curated smart-lists report.
type SmartListsHandler struct {
	uc     SmartListsUseCase
	logger *slog.Logger
}

func NewSmartListsHandler(uc SmartListsUseCase, logger *slog.Logger) *SmartListsHandler {
	if logger == nil {
		logger = slog.Default()
	}
	return &SmartListsHandler{uc: uc, logger: logger}
}

// Get handles GET /api/v1/insights/lists.
//
// @Summary     Smart lists
// @Description Curated read-only "smart lists" per Sonarr instance: ended
// @Description series with library gaps, series returning soon (next episode
// @Description within 35 days), and returning series on hiatus (last aired
// @Description > 90 days, no scheduled next airing). Each shelf carries an
// @Description exact match count and a bounded top-50 series slice. Optional
// @Description ?instance= scopes the report to a single instance.
// @Tags        insights
// @Produce     json
// @Param       instance query string false "scope the report to a single Sonarr instance"
// @Success     200 {object} dto.SmartListsReportDTO
// @Failure     401 {object} dto.ErrorResponse
// @Failure     500 {object} dto.ErrorResponse
// @Security    CookieAuth
// @Security    ApiKeyAuth
// @Router      /insights/lists [get]
func (h *SmartListsHandler) Get(c *gin.Context) {
	rep, err := h.uc.Build(c.Request.Context(), c.Query("instance"))
	if err != nil {
		h.logger.ErrorContext(c.Request.Context(), "smartlists_query_failed",
			slog.String("error", err.Error()))
		c.JSON(http.StatusInternalServerError, dto.ErrorResponse{Error: "smart lists unavailable"})
		return
	}
	c.JSON(http.StatusOK, toSmartListsReportDTO(rep))
}

func toSmartListsReportDTO(rep smartlists.Report) dto.SmartListsReportDTO {
	instances := make([]dto.SmartListsInstanceDTO, 0, len(rep.Instances))
	for _, si := range rep.Instances {
		shelves := make([]dto.SmartShelfDTO, 0, len(si.Shelves))
		for _, sh := range si.Shelves {
			shelves = append(shelves, dto.SmartShelfDTO{
				Key:    sh.Key,
				Title:  sh.Title,
				Count:  sh.Count,
				Series: smartSeriesDTO(sh.Series),
			})
		}
		instances = append(instances, dto.SmartListsInstanceDTO{
			InstanceName: si.InstanceName,
			Shelves:      shelves,
		})
	}
	return dto.SmartListsReportDTO{GeneratedAt: rep.GeneratedAt, Instances: instances}
}

func smartSeriesDTO(in []smartlists.SmartListSeries) []dto.SmartListSeriesDTO {
	out := make([]dto.SmartListSeriesDTO, 0, len(in))
	for _, s := range in {
		out = append(out, dto.SmartListSeriesDTO{
			SeriesID:     s.SeriesID,
			SonarrID:     s.SonarrID,
			Title:        s.Title,
			MissingCount: s.MissingCount,
			NextAirDate:  s.NextAirDate,
			LastAiredAt:  s.LastAiredAt,
		})
	}
	return out
}
