package rest

import (
	"log/slog"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"github.com/alexmorbo/seasonfill/internal/enrichment/rest/seriesrefresh"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
	"github.com/alexmorbo/seasonfill/internal/shared/http/dto"
)

// SeriesRefreshHandler powers POST /api/v1/instances/:name/series/:id/refresh.
//
// Operator-facing trigger to re-enrich a series when the local entity
// model looks stale. Returns 202 immediately — the actual TMDB / OMDb
// calls happen on the enrichment dispatcher's goroutines (story 211)
// at PriorityHot, which jumps every cold-start job in the queue.
//
// Idempotent at the dispatcher level: a second hit while the first
// is still pending dedups on (kind, entity_id) — the operator can
// mash the button without queueing N copies.
type SeriesRefreshHandler struct {
	uc     *seriesrefresh.UseCase
	logger *slog.Logger
}

// NewSeriesRefreshHandler constructs the handler. logger=nil falls
// back to slog.Default().
func NewSeriesRefreshHandler(uc *seriesrefresh.UseCase, logger *slog.Logger) *SeriesRefreshHandler {
	if logger == nil {
		logger = slog.Default()
	}
	return &SeriesRefreshHandler{uc: uc, logger: logger}
}

// DEAD: per-instance route deleted at N-1b cutover (story 492). Function retained for future cleanup sweep.
func (h *SeriesRefreshHandler) Refresh(c *gin.Context) {
	name := c.Param("name")
	idStr := c.Param("id")
	parsedID, err := strconv.Atoi(idStr)
	if err != nil || parsedID <= 0 {
		c.JSON(http.StatusBadRequest, dto.ErrorResponse{Error: "invalid series id"})
		return
	}
	sonarrID := domain.SonarrSeriesID(parsedID)

	res, err := h.uc.Refresh(c.Request.Context(), domain.InstanceName(name), sonarrID)
	if err != nil {
		_ = c.Error(err)
		return
	}

	c.JSON(http.StatusAccepted, dto.SeriesRefreshResponse{
		SeriesID:     res.SeriesID,
		SeriesQueued: res.SeriesQueued,
		Persons:      res.Persons,
		OMDbQueued:   res.OMDbQueued,
	})
}
