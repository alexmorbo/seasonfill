// Package rest — grab HTTP handlers.
//
// global_grab_episode_files_handler.go (Story 492 / N-1b). GET
// /api/v1/grabs/:id/episode-files replaces the per-instance
// /api/v1/instances/:name/grabs/:id/episode-files. Because GrabID is
// globally unique (UUID v4), the path no longer needs the instance
// scope — the handler reads InstanceName off the persisted Record
// itself, so a cross-instance mismatch path is impossible by
// construction. Same upstream-fetch + projection logic as the legacy
// handler. Legacy route + constructor are DELETED in this same story.
package rest

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	catalogrest "github.com/alexmorbo/seasonfill/internal/catalog/rest"
	grab "github.com/alexmorbo/seasonfill/internal/grab/domain"
	ports "github.com/alexmorbo/seasonfill/internal/shared/dataports"
	sharedErrors "github.com/alexmorbo/seasonfill/internal/shared/errors"
	"github.com/alexmorbo/seasonfill/internal/shared/http/dto"
)

// GlobalGrabEpisodeFilesHandler exposes
// GET /api/v1/grabs/:id/episode-files.
type GlobalGrabEpisodeFilesHandler struct {
	grabs  ports.GrabRepository
	reg    catalogrest.InstanceRegistry
	logger *slog.Logger
}

// NewGlobalGrabEpisodeFilesHandler constructs the handler. grabs +
// reg are required (handler panics on nil grabs at call time; nil reg
// is honoured via the registry's Load=nil → empty-map fall-back).
// logger nil-OK.
func NewGlobalGrabEpisodeFilesHandler(
	grabs ports.GrabRepository,
	reg catalogrest.InstanceRegistry,
	logger *slog.Logger,
) *GlobalGrabEpisodeFilesHandler {
	if logger == nil {
		logger = slog.Default()
	}
	return &GlobalGrabEpisodeFilesHandler{grabs: grabs, reg: reg, logger: logger}
}

// List handles GET /api/v1/grabs/:id/episode-files.
//
// @Summary     List on-disk files for a grab (global)
// @Description Lazy fetch of Sonarr's episodeFile + episode rows for
// @Description the grab's (series_id, season_number). The instance is
// @Description resolved from the persisted grab_records row — no
// @Description path scope. Returns 200 with empty items when
// @Description status != imported.
// @Tags        grabs
// @Produce     json
// @Param       id   path     string  true  "Grab UUID"
// @Success     200  {object} dto.EpisodeFileList
// @Failure     400  {object} dto.ErrorResponse
// @Failure     404  {object} dto.ErrorResponse
// @Failure     502  {object} dto.ErrorResponse
// @Security    CookieAuth
// @Security    ApiKeyAuth
// @Router      /grabs/{id}/episode-files [get]
func (h *GlobalGrabEpisodeFilesHandler) List(c *gin.Context) {
	ctx := c.Request.Context()
	idRaw := c.Param("id")

	id, err := uuid.Parse(idRaw)
	if err != nil {
		c.JSON(http.StatusBadRequest, dto.ErrorResponse{Error: "invalid id"})
		return
	}

	rec, err := h.grabs.GetByID(ctx, id)
	if err != nil {
		_ = c.Error(err)
		return
	}

	instName := string(rec.InstanceName)
	inst, ok := h.reg.Snapshot()[instName]
	if !ok || inst.Client == nil {
		c.JSON(http.StatusNotFound, dto.ErrorResponse{Error: "unknown instance: " + instName})
		return
	}

	// status != imported → 200 with empty items (drawer renders
	// "не импортирован" cleanly without a separate 404 branch).
	if rec.Status != grab.StatusImported {
		c.JSON(http.StatusOK, dto.EpisodeFileList{Items: []dto.EpisodeFileDetail{}})
		return
	}

	all, err := inst.Client.ListEpisodeFilesBySeason(ctx, rec.SeriesID, rec.SeasonNumber)
	if err != nil {
		if errors.Is(err, sharedErrors.ErrInstanceUnauthorized) {
			h.logger.WarnContext(ctx, "global_grab_episode_files_upstream_unauthorized",
				slog.String("instance", instName),
				slog.String("grab_id", id.String()),
				slog.String("error", err.Error()))
			c.JSON(http.StatusBadGateway, dto.ErrorResponse{Error: "sonarr unauthorized"})
			return
		}
		h.logger.ErrorContext(ctx, "global_grab_episode_files_upstream_failed",
			slog.String("instance", instName),
			slog.String("grab_id", id.String()),
			slog.String("error", err.Error()))
		c.JSON(http.StatusBadGateway, dto.ErrorResponse{Error: "sonarr unavailable"})
		return
	}

	items := make([]dto.EpisodeFileDetail, 0, len(all))
	for _, f := range all {
		items = append(items, dto.EpisodeFileDetail{
			ID:             f.ID,
			RelativePath:   f.RelativePath,
			SeasonNumber:   f.SeasonNumber,
			EpisodeNumbers: f.EpisodeNumbers,
			SizeBytes:      f.SizeBytes,
			Quality:        f.Quality,
		})
	}

	h.logger.DebugContext(ctx, "global_grab_episode_files_returned",
		slog.String("instance", instName),
		slog.String("grab_id", id.String()),
		slog.Int("count", len(items)))
	c.JSON(http.StatusOK, dto.EpisodeFileList{Items: items})
}
