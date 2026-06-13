package handlers

import (
	"errors"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"github.com/alexmorbo/seasonfill/application/ports"
	"github.com/alexmorbo/seasonfill/application/seriesrefresh"
	"github.com/alexmorbo/seasonfill/interface/http/dto"
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

// Refresh handles POST /api/v1/instances/:name/series/:id/refresh.
//
// @Summary     Re-enrich a series
// @Description Re-enqueues the series, its top-10 cast persons, and
// @Description (when imdb_id is set) the OMDb rating refresh at
// @Description PriorityHot. Returns 202 immediately; the work happens
// @Description on the enrichment dispatcher.
// @Tags        instances
// @Produce     json
// @Param       name  path      string  true   "Instance name"
// @Param       id    path      int     true   "Sonarr series id"
// @Success     202   {object}  dto.SeriesRefreshResponse
// @Failure     400   {object}  dto.ErrorResponse
// @Failure     401   {object}  dto.ErrorResponse
// @Failure     404   {object}  dto.ErrorResponse
// @Failure     500   {object}  dto.ErrorResponse
// @Security    CookieAuth
// @Security    ApiKeyAuth
// @Router      /instances/{name}/series/{id}/refresh [post]
func (h *SeriesRefreshHandler) Refresh(c *gin.Context) {
	name := c.Param("name")
	idStr := c.Param("id")
	sonarrID, err := strconv.Atoi(idStr)
	if err != nil || sonarrID <= 0 {
		c.JSON(http.StatusBadRequest, dto.ErrorResponse{Error: "invalid series id"})
		return
	}

	res, err := h.uc.Refresh(c.Request.Context(), name, sonarrID)
	if err != nil {
		if errors.Is(err, ports.ErrNotFound) {
			c.JSON(http.StatusNotFound, dto.ErrorResponse{Error: "series not found"})
			return
		}
		writeInternalError(c, h.logger, "series_refresh_failed", err,
			slog.String("instance_name", name),
			slog.Int("sonarr_series_id", sonarrID))
		return
	}

	c.JSON(http.StatusAccepted, dto.SeriesRefreshResponse{
		SeriesID:     res.SeriesID,
		SeriesQueued: res.SeriesQueued,
		Persons:      res.Persons,
		OMDbQueued:   res.OMDbQueued,
	})
}
