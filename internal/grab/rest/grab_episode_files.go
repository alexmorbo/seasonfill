package rest

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/alexmorbo/seasonfill/application/ports"
	"github.com/alexmorbo/seasonfill/domain"
	"github.com/alexmorbo/seasonfill/interface/http/dto"
	catalogrest "github.com/alexmorbo/seasonfill/internal/catalog/rest"
	grab "github.com/alexmorbo/seasonfill/internal/grab/domain"
)

// GrabEpisodeFilesHandler exposes
// GET /api/v1/instances/:name/grabs/:id/episode-files — lazy on-demand
// fetch of the on-disk files Sonarr placed for the grab. PRD §3 B1
// item 5 / decision #6 (lazy, no persistence). 043c.
type GrabEpisodeFilesHandler struct {
	grabs  ports.GrabRepository
	reg    catalogrest.InstanceRegistry
	logger *slog.Logger
}

func NewGrabEpisodeFilesHandler(
	grabs ports.GrabRepository,
	reg catalogrest.InstanceRegistry,
	logger *slog.Logger,
) *GrabEpisodeFilesHandler {
	if logger == nil {
		logger = slog.Default()
	}
	return &GrabEpisodeFilesHandler{grabs: grabs, reg: reg, logger: logger}
}

// List handles GET /api/v1/instances/:name/grabs/:id/episode-files.
//
// @Summary     List on-disk files for a grab
// @Description Lazy fetch of Sonarr's episodeFile + episode rows for
// @Description the grab's (series_id, season_number). Returns 200 with
// @Description empty items when status != imported.
// @Tags        grabs
// @Produce     json
// @Param       name  path      string  true  "Instance name"
// @Param       id    path      string  true  "Grab UUID"
// @Success     200   {object}  dto.EpisodeFileList
// @Failure     400   {object}  dto.ErrorResponse
// @Failure     404   {object}  dto.ErrorResponse
// @Failure     502   {object}  dto.ErrorResponse
// @Security    CookieAuth
// @Security    ApiKeyAuth
// @Router      /instances/{name}/grabs/{id}/episode-files [get]
func (h *GrabEpisodeFilesHandler) List(c *gin.Context) {
	ctx := c.Request.Context()
	name := c.Param("name")
	idRaw := c.Param("id")

	inst, ok := h.reg.Snapshot()[name]
	if !ok || inst.Client == nil {
		c.JSON(http.StatusNotFound, dto.ErrorResponse{Error: "unknown instance: " + name})
		return
	}

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

	// Defence-in-depth: refuse to enumerate a grab from a different
	// instance via this path. 404 (not 403) to avoid leaking
	// existence.
	if string(rec.InstanceName) != name {
		h.logger.WarnContext(ctx, "grab_episode_files_instance_mismatch",
			slog.String("path_instance", name),
			slog.String("grab_instance", string(rec.InstanceName)),
			slog.String("grab_id", id.String()))
		c.JSON(http.StatusNotFound, dto.ErrorResponse{Error: "grab not found"})
		return
	}

	// status != imported → 200 with empty items (drawer renders "не
	// импортирован" cleanly without a separate 404 branch).
	if rec.Status != grab.StatusImported {
		c.JSON(http.StatusOK, dto.EpisodeFileList{Items: []dto.EpisodeFileDetail{}})
		return
	}

	all, err := inst.Client.ListEpisodeFilesBySeason(ctx, rec.SeriesID, rec.SeasonNumber)
	if err != nil {
		if errors.Is(err, domain.ErrInstanceUnauthorized) {
			h.logger.WarnContext(ctx, "grab_episode_files_upstream_unauthorized",
				slog.String("instance", name),
				slog.String("grab_id", id.String()),
				slog.String("error", err.Error()))
			c.JSON(http.StatusBadGateway, dto.ErrorResponse{Error: "sonarr unauthorized"})
			return
		}
		h.logger.ErrorContext(ctx, "grab_episode_files_upstream_failed",
			slog.String("instance", name),
			slog.String("grab_id", id.String()),
			slog.String("error", err.Error()))
		c.JSON(http.StatusBadGateway, dto.ErrorResponse{Error: "sonarr unavailable"})
		return
	}

	// Sonarr returned every file in the season; the row pins a specific
	// season already, so all items are season-matched. We don't have
	// a persisted per-episode list on the grab row, so we surface every
	// file in the season — the drawer's caller (operator) is judging
	// "did the import land for this grab" and seeing all season files
	// is the most useful answer. If the row's CoverageCount is small
	// (single-episode grab) the drawer can still see the wider context.
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

	h.logger.DebugContext(ctx, "grab_episode_files_returned",
		slog.String("instance", name),
		slog.String("grab_id", id.String()),
		slog.Int("count", len(items)))
	c.JSON(http.StatusOK, dto.EpisodeFileList{Items: items})
}
