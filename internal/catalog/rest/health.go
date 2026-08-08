package rest

import (
	"context"
	"log/slog"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/alexmorbo/seasonfill/internal/catalog/app/health"
	ports "github.com/alexmorbo/seasonfill/internal/shared/dataports"
	"github.com/alexmorbo/seasonfill/internal/shared/http/dto"
)

// HealthUseCase is the narrow port the handler depends on. Production:
// *health.UseCase. Tests pass a real UseCase over a fake repository.
type HealthUseCase interface {
	Build(ctx context.Context) (health.Report, error)
}

// HealthHandler serves GET /api/v1/insights/health — the catalog-health
// operator pulse.
type HealthHandler struct {
	uc     HealthUseCase
	logger *slog.Logger
}

func NewHealthHandler(uc HealthUseCase, logger *slog.Logger) *HealthHandler {
	if logger == nil {
		logger = slog.Default()
	}
	return &HealthHandler{uc: uc, logger: logger}
}

// Get handles GET /api/v1/insights/health.
//
// @Summary     Catalog health dashboard
// @Description Per-signal operator pulse: COUNT + bounded drill-down for
// @Description missing tvdb_id, missing poster (any-lang), stale
// @Description enrichment, stuck grabs, and inbox dead-letters. Rate-limit
// @Description pressure is a deferred envelope pointing at its metric.
// @Tags        insights
// @Produce     json
// @Success     200 {object} dto.HealthDashboardDTO
// @Failure     401 {object} dto.ErrorResponse
// @Failure     500 {object} dto.ErrorResponse
// @Security    CookieAuth
// @Security    ApiKeyAuth
// @Router      /insights/health [get]
func (h *HealthHandler) Get(c *gin.Context) {
	rep, err := h.uc.Build(c.Request.Context())
	if err != nil {
		h.logger.ErrorContext(c.Request.Context(), "health_query_failed",
			slog.String("error", err.Error()))
		c.JSON(http.StatusInternalServerError, dto.ErrorResponse{Error: "health unavailable"})
		return
	}
	c.JSON(http.StatusOK, toHealthDTO(rep))
}

func toHealthDTO(rep health.Report) dto.HealthDashboardDTO {
	return dto.HealthDashboardDTO{
		GeneratedAt: rep.GeneratedAt,
		MissingTVDBID: dto.HealthSeriesSignalDTO{
			Count: rep.MissingTVDBID.Count,
			Items: seriesItemsDTO(rep.MissingTVDBID.Items),
		},
		MissingPoster: dto.HealthSeriesSignalDTO{
			Count: rep.MissingPoster.Count,
			Items: seriesItemsDTO(rep.MissingPoster.Items),
		},
		StaleEnrichment: dto.HealthStaleSignalDTO{
			Count: rep.StaleEnrichment.Count,
			Items: staleItemsDTO(rep.StaleEnrichment.Items),
		},
		StuckGrabs: dto.HealthGrabSignalDTO{
			Count: rep.StuckGrabs.Count,
			Note:  rep.StuckGrabs.Note,
			Items: grabItemsDTO(rep.StuckGrabs.Items),
		},
		DeadLetters: dto.HealthInboxSignalDTO{
			Count: rep.DeadLetters.Count,
			Items: inboxItemsDTO(rep.DeadLetters.Items),
		},
		RateLimitPressure: dto.HealthDeferredSignalDTO{
			Deferred: rep.RateLimitPressure.Deferred,
			Reason:   rep.RateLimitPressure.Reason,
			Metric:   rep.RateLimitPressure.Metric,
		},
	}
}

func seriesItemsDTO(in []ports.HealthSeriesItem) []dto.HealthSeriesItemDTO {
	out := make([]dto.HealthSeriesItemDTO, 0, len(in))
	for _, it := range in {
		out = append(out, dto.HealthSeriesItemDTO{SeriesID: it.SeriesID, Title: it.Title})
	}
	return out
}

func staleItemsDTO(in []ports.HealthStaleItem) []dto.HealthStaleItemDTO {
	out := make([]dto.HealthStaleItemDTO, 0, len(in))
	for _, it := range in {
		out = append(out, dto.HealthStaleItemDTO{
			SeriesID: it.SeriesID,
			Title:    it.Title,
			Tier:     it.Tier,
			SyncedAt: it.SyncedAt,
		})
	}
	return out
}

func grabItemsDTO(in []ports.HealthGrabItem) []dto.HealthGrabItemDTO {
	out := make([]dto.HealthGrabItemDTO, 0, len(in))
	for _, it := range in {
		out = append(out, dto.HealthGrabItemDTO{
			ID:           it.ID,
			InstanceName: it.InstanceName,
			SeriesTitle:  it.SeriesTitle,
			SeasonNumber: it.SeasonNumber,
			CreatedAt:    it.CreatedAt,
		})
	}
	return out
}

func inboxItemsDTO(in []ports.HealthInboxItem) []dto.HealthInboxItemDTO {
	out := make([]dto.HealthInboxItemDTO, 0, len(in))
	for _, it := range in {
		out = append(out, dto.HealthInboxItemDTO{
			ID:           it.ID,
			InstanceName: it.InstanceName,
			EventType:    it.EventType,
			Attempts:     it.Attempts,
			LastError:    it.LastError,
			CreatedAt:    it.CreatedAt,
		})
	}
	return out
}
