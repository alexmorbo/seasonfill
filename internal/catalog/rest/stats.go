package rest

import (
	"context"
	"log/slog"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/alexmorbo/seasonfill/internal/catalog/app/stats"
	"github.com/alexmorbo/seasonfill/internal/shared/http/dto"
)

// StatsUseCase is the narrow port the handler depends on. Production:
// *stats.UseCase. Tests pass a real UseCase over a fake repository.
type StatsUseCase interface {
	Build(ctx context.Context, instanceFilter string) (stats.Report, error)
}

// StatsHandler serves GET /api/v1/insights/stats — the read-only library
// statistics report.
type StatsHandler struct {
	uc     StatsUseCase
	logger *slog.Logger
}

func NewStatsHandler(uc StatsUseCase, logger *slog.Logger) *StatsHandler {
	if logger == nil {
		logger = slog.Default()
	}
	return &StatsHandler{uc: uc, logger: logger}
}

// Get handles GET /api/v1/insights/stats.
//
// @Summary     Library statistics
// @Description Read-only library statistics per Sonarr instance: catalog
// @Description totals (series / episodes on disk / size), top genres and
// @Description networks by size on disk, grab success breakdown, and qBit
// @Description torrent upload/download/ratio totals. Optional ?instance=
// @Description scopes the report to a single instance.
// @Tags        insights
// @Produce     json
// @Param       instance query string false "scope the report to a single Sonarr instance"
// @Success     200 {object} dto.StatsReportDTO
// @Failure     401 {object} dto.ErrorResponse
// @Failure     500 {object} dto.ErrorResponse
// @Security    CookieAuth
// @Security    ApiKeyAuth
// @Router      /insights/stats [get]
func (h *StatsHandler) Get(c *gin.Context) {
	rep, err := h.uc.Build(c.Request.Context(), c.Query("instance"))
	if err != nil {
		h.logger.ErrorContext(c.Request.Context(), "stats_query_failed",
			slog.String("error", err.Error()))
		c.JSON(http.StatusInternalServerError, dto.ErrorResponse{Error: "stats unavailable"})
		return
	}
	c.JSON(http.StatusOK, toStatsReportDTO(rep))
}

func toStatsReportDTO(rep stats.Report) dto.StatsReportDTO {
	instances := make([]dto.StatsInstanceDTO, 0, len(rep.Instances))
	for _, si := range rep.Instances {
		instances = append(instances, dto.StatsInstanceDTO{
			InstanceName: si.InstanceName,
			Totals: dto.StatsTotalsDTO{
				SeriesCount:    si.Totals.SeriesCount,
				EpisodesOnDisk: si.Totals.EpisodesOnDisk,
				TotalSizeBytes: si.Totals.TotalSizeBytes,
			},
			ByGenre:   statsGenreDTO(si.ByGenre),
			ByNetwork: statsNetworkDTO(si.ByNetwork),
			GrabSuccess: dto.StatsGrabSuccessDTO{
				Grabbed:     si.GrabSuccess.Grabbed,
				Imported:    si.GrabSuccess.Imported,
				Failed:      si.GrabSuccess.Failed,
				SuccessRate: si.GrabSuccess.SuccessRate,
			},
			TorrentTotals: dto.StatsTorrentTotalsDTO{
				TorrentCount:         si.TorrentTotals.TorrentCount,
				TotalUploadedBytes:   si.TorrentTotals.TotalUploadedBytes,
				TotalDownloadedBytes: si.TorrentTotals.TotalDownloadedBytes,
				AvgRatio:             si.TorrentTotals.AvgRatio,
			},
		})
	}
	return dto.StatsReportDTO{GeneratedAt: rep.GeneratedAt, Instances: instances}
}

func statsGenreDTO(in []stats.KindBucket) []dto.StatsKindDTO {
	out := make([]dto.StatsKindDTO, 0, len(in))
	for _, b := range in {
		out = append(out, dto.StatsKindDTO{
			Genre:       b.Name,
			SeriesCount: b.SeriesCount,
			SizeBytes:   b.SizeBytes,
		})
	}
	return out
}

func statsNetworkDTO(in []stats.KindBucket) []dto.StatsKindDTO {
	out := make([]dto.StatsKindDTO, 0, len(in))
	for _, b := range in {
		out = append(out, dto.StatsKindDTO{
			Network:     b.Name,
			SeriesCount: b.SeriesCount,
			SizeBytes:   b.SizeBytes,
		})
	}
	return out
}
