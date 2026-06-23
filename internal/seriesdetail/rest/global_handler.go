// Package rest — seriesdetail HTTP handlers.
//
// global_handler.go (Story 491 / N-1a). GlobalSeriesHandler powers
// GET /api/v1/series/:id and POST /api/v1/series/:id/regrab. Both
// resolve via the canonical series.id (no instance in the URL path).
// The Get path delegates to GlobalComposerUseCase; the Regrab path
// resolves the preferred (instance, sonarr_id) from cache and
// delegates to the existing SeriesRefresher.
package rest

import (
	"context"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/alexmorbo/seasonfill/internal/enrichment/rest/seriesrefresh"
	seriesdetail "github.com/alexmorbo/seasonfill/internal/seriesdetail/app"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
	"github.com/alexmorbo/seasonfill/internal/shared/http/dto"
)

// SeriesRefresher is the narrow port the GlobalSeriesHandler needs for
// the /regrab path. *seriesrefresh.UseCase already satisfies it; tests
// inject a fake. Story 491 / N-1a.
type SeriesRefresher interface {
	Refresh(ctx context.Context, instanceName domain.InstanceName, sonarrSeriesID domain.SonarrSeriesID) (seriesrefresh.Result, error)
}

// GlobalSeriesHandler powers GET /api/v1/series/:id and POST
// /api/v1/series/:id/regrab (story 491 / N-1a).
type GlobalSeriesHandler struct {
	composer    *seriesdetail.GlobalComposerUseCase
	cacheLookup seriesdetail.SeriesCacheLookupPort
	refresher   SeriesRefresher
	logger      *slog.Logger
}

// NewGlobalSeriesHandler constructs the handler. logger=nil falls back
// to slog.Default. composer + cacheLookup + refresher are required (no
// nil-OK paths for production wiring).
func NewGlobalSeriesHandler(
	composer *seriesdetail.GlobalComposerUseCase,
	cacheLookup seriesdetail.SeriesCacheLookupPort,
	refresher SeriesRefresher,
	logger *slog.Logger,
) *GlobalSeriesHandler {
	if logger == nil {
		logger = slog.Default()
	}
	return &GlobalSeriesHandler{
		composer:    composer,
		cacheLookup: cacheLookup,
		refresher:   refresher,
		logger:      logger,
	}
}

// Get handles GET /api/v1/series/:id.
//
// @Summary     Composite series detail document (global)
// @Description Same shape as the per-instance /api/v1/instances/{name}/series/{id}
// @Description endpoint, but resolves the preferred instance automatically
// @Description from the canonical series.id. When the series is in zero
// @Description libraries (TMDB-only) the response carries `in_library_instances=[]`
// @Description and the per-instance branches (Library / Download / Seasons /
// @Description Cast) are empty — the Hero block is populated from the
// @Description canon row.
// @Tags        series
// @Produce     json
// @Param       id    path      int     true   "Canonical series.id"
// @Param       lang  query     string  false  "BCP-47 language tag (default en-US)"
// @Success     200   {object}  dto.SeriesDetailResponse
// @Failure     400   {object}  dto.ErrorResponse
// @Failure     401   {object}  dto.ErrorResponse
// @Failure     404   {object}  dto.ErrorResponse
// @Failure     500   {object}  dto.ErrorResponse
// @Security    CookieAuth
// @Security    ApiKeyAuth
// @Router      /series/{id} [get]
func (h *GlobalSeriesHandler) Get(c *gin.Context) {
	parsedID, err := strconv.Atoi(c.Param("id"))
	if err != nil || parsedID <= 0 {
		c.JSON(http.StatusBadRequest, dto.ErrorResponse{Error: "invalid series id"})
		return
	}
	seriesID := domain.SeriesID(parsedID)
	lang := strings.TrimSpace(c.Query("lang"))

	ctx := c.Request.Context()
	detail, err := h.composer.Get(ctx, seriesID, lang)
	if err != nil {
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusOK, toSeriesDetailResponse(detail))
}

// Regrab handles POST /api/v1/series/:id/regrab.
//
// @Summary     Re-enrich a series (global)
// @Description Resolves the canonical series.id to the preferred Sonarr
// @Description instance (lexicographically-first instance that carries
// @Description this series), then enqueues series + cast + OMDb re-enrich
// @Description at PriorityHot — same semantics as the per-instance
// @Description /series/{id}/refresh endpoint.
// @Tags        series
// @Produce     json
// @Param       id    path      int     true   "Canonical series.id"
// @Success     202   {object}  dto.SeriesRefreshResponse
// @Failure     400   {object}  dto.ErrorResponse
// @Failure     401   {object}  dto.ErrorResponse
// @Failure     404   {object}  dto.ErrorResponse
// @Failure     500   {object}  dto.ErrorResponse
// @Security    CookieAuth
// @Security    ApiKeyAuth
// @Router      /series/{id}/regrab [post]
func (h *GlobalSeriesHandler) Regrab(c *gin.Context) {
	parsedID, err := strconv.Atoi(c.Param("id"))
	if err != nil || parsedID <= 0 {
		c.JSON(http.StatusBadRequest, dto.ErrorResponse{Error: "invalid series id"})
		return
	}
	seriesID := domain.SeriesID(parsedID)
	ctx := c.Request.Context()

	entries, err := h.cacheLookup.ListBySeriesID(ctx, seriesID)
	if err != nil {
		_ = c.Error(err)
		return
	}
	if len(entries) == 0 {
		c.JSON(http.StatusNotFound, dto.ErrorResponse{Error: "series not in any library"})
		return
	}
	// Pick lexicographically-first instance — same preference rule as
	// GlobalComposerUseCase.Get for symmetric behaviour.
	preferred := entries[0]
	for _, e := range entries {
		if e.InstanceName < preferred.InstanceName {
			preferred = e
		}
	}
	res, err := h.refresher.Refresh(ctx, preferred.InstanceName, preferred.SonarrSeriesID)
	if err != nil {
		_ = c.Error(err)
		return
	}
	h.logger.InfoContext(ctx, "global_regrab_dispatched",
		slog.Int64("series_id", int64(seriesID)),
		slog.String("instance", string(preferred.InstanceName)),
		slog.Int("sonarr_series_id", int(preferred.SonarrSeriesID)),
		slog.Bool("series_queued", res.SeriesQueued),
		slog.Int("persons", res.Persons),
		slog.Bool("omdb_queued", res.OMDbQueued),
	)
	c.JSON(http.StatusAccepted, dto.SeriesRefreshResponse{
		SeriesID:     res.SeriesID,
		SeriesQueued: res.SeriesQueued,
		Persons:      res.Persons,
		OMDbQueued:   res.OMDbQueued,
	})
}
