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

// GrabEpisodeFilesHandler — legacy per-instance handler retained for
// internal tests after the route was deleted in N-1b cutover
// (story 492). The live entry point for the same use case is now
// GlobalGrabEpisodeFilesHandler at GET /api/v1/grabs/:id/episode-files
// (`global_grab_episode_files_handler.go`). Kept here so the test
// surface in `grab_episode_files_test.go` still compiles; no
// production route registers `List` after N-1b.
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

// List was reachable at GET /api/v1/instances/:name/grabs/:id/episode-files
// pre-N-1b. The route is deleted; the function survives for the
// test surface. NOT documented in OpenAPI on purpose — swag
// annotations were stripped in story 498 to clear the phantom
// /instances/{name}/grabs/{id}/episode-files path from
// docs/swagger.yaml.
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
		if errors.Is(err, sharedErrors.ErrInstanceUnauthorized) {
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
