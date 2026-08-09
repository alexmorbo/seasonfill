package rest

import (
	"context"
	"log/slog"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/alexmorbo/seasonfill/internal/catalog/app/collections"
	"github.com/alexmorbo/seasonfill/internal/shared/http/dto"
)

// CollectionsUseCase is the narrow port the handler depends on. Production:
// *collections.UseCase. Tests pass a real UseCase over a fake repository.
type CollectionsUseCase interface {
	Build(ctx context.Context, instanceFilter string) (collections.Report, error)
}

// CollectionsHandler serves GET /api/v1/insights/collections — the read-only
// curated collections report.
type CollectionsHandler struct {
	uc     CollectionsUseCase
	logger *slog.Logger
}

func NewCollectionsHandler(uc CollectionsUseCase, logger *slog.Logger) *CollectionsHandler {
	if logger == nil {
		logger = slog.Default()
	}
	return &CollectionsHandler{uc: uc, logger: logger}
}

// Get handles GET /api/v1/insights/collections.
//
// @Summary     Curated collections
// @Description Curated read-only theme/collection buckets per Sonarr instance:
// @Description the owned library grouped by TMDB keyword sets (based on books,
// @Description true crime, sci-fi & alt-history, comic-book, MCU, …). Each
// @Description collection carries its exact owned series count and a bounded
// @Description top-50 title-ordered series slice; empty collections are hidden
// @Description and the rest are ordered by owned count. Optional ?instance=
// @Description scopes the report to a single instance.
// @Tags        insights
// @Produce     json
// @Param       instance query string false "scope the report to a single Sonarr instance"
// @Success     200 {object} dto.CollectionsReportDTO
// @Failure     401 {object} dto.ErrorResponse
// @Failure     500 {object} dto.ErrorResponse
// @Security    CookieAuth
// @Security    ApiKeyAuth
// @Router      /insights/collections [get]
func (h *CollectionsHandler) Get(c *gin.Context) {
	rep, err := h.uc.Build(c.Request.Context(), c.Query("instance"))
	if err != nil {
		h.logger.ErrorContext(c.Request.Context(), "collections_query_failed",
			slog.String("error", err.Error()))
		c.JSON(http.StatusInternalServerError, dto.ErrorResponse{Error: "collections unavailable"})
		return
	}
	c.JSON(http.StatusOK, toCollectionsReportDTO(rep))
}

func toCollectionsReportDTO(rep collections.Report) dto.CollectionsReportDTO {
	instances := make([]dto.CollectionsInstanceDTO, 0, len(rep.Instances))
	for _, si := range rep.Instances {
		cols := make([]dto.CollectionDTO, 0, len(si.Collections))
		for _, c := range si.Collections {
			cols = append(cols, dto.CollectionDTO{
				Slug:        c.Slug,
				Title:       c.Title,
				OwnedCount:  c.OwnedCount,
				IsFranchise: c.IsFranchise,
				Series:      collectionSeriesDTO(c.Series),
			})
		}
		instances = append(instances, dto.CollectionsInstanceDTO{
			InstanceName: si.InstanceName,
			Collections:  cols,
		})
	}
	return dto.CollectionsReportDTO{GeneratedAt: rep.GeneratedAt, Instances: instances}
}

func collectionSeriesDTO(in []collections.CollectionSeries) []dto.CollectionSeriesDTO {
	out := make([]dto.CollectionSeriesDTO, 0, len(in))
	for _, s := range in {
		out = append(out, dto.CollectionSeriesDTO{
			SeriesID: s.SeriesID,
			SonarrID: s.SonarrID,
			Title:    s.Title,
		})
	}
	return out
}
