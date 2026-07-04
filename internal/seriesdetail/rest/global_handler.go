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
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/alexmorbo/seasonfill/internal/enrichment/rest/seriesrefresh"
	seriesdetail "github.com/alexmorbo/seasonfill/internal/seriesdetail/app"
	ports "github.com/alexmorbo/seasonfill/internal/shared/dataports"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
	"github.com/alexmorbo/seasonfill/internal/shared/http/dto"
)

// SeriesRefresher is the narrow port the GlobalSeriesHandler needs for
// the /regrab path. Refresh keys off a library (instance, sonarr_id);
// RefreshByCanonical keys directly off the canonical series.id for
// TMDB-only (non-library) series. *seriesrefresh.UseCase already
// satisfies both; tests inject a fake. Story 491 / N-1a.
type SeriesRefresher interface {
	Refresh(ctx context.Context, instanceName domain.InstanceName, sonarrSeriesID domain.SonarrSeriesID) (seriesrefresh.Result, error)
	RefreshByCanonical(ctx context.Context, seriesID domain.SeriesID) (seriesrefresh.Result, error)
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
// @Summary     Above-fold canon series skeleton (global)
// @Description Above-fold canon skeleton keyed by canonical series.id: hero +
// @Description sidebar + season_count + in_library_instances. Sonarr library
// @Description state, torrents, seasons list, cast, overview and
// @Description recommendations are separate endpoints (§7.0). TMDB-only series
// @Description return the same shape with in_library_instances=[].
// @Tags        series
// @Produce     json
// @Param       id    path      int     true   "Canonical series.id"
// @Param       lang  query     string  false  "BCP-47 language tag (default en-US)"
// @Success     200   {object}  seriesdetail.SkeletonDTO
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
	skeleton, err := h.composer.Get(ctx, seriesID, lang)
	if err != nil {
		_ = c.Error(err) // middleware maps ports.ErrNotFound → 404
		return
	}
	c.JSON(http.StatusOK, skeleton)
}

// Regrab handles POST /api/v1/series/:id/regrab.
//
// @Summary     Re-enrich a series (global)
// @Description Resolves the canonical series.id to the preferred Sonarr
// @Description instance (lex-first instance that carries this series in
// @Description series_cache), then enqueues series + cast + OMDb
// @Description re-enrichment at PriorityHot. Returns 202 with the
// @Description scan_run_id of the spawned refresh. Non-library
// @Description (TMDB-only) series are re-enriched directly by canonical
// @Description id; 404 only when the id maps to no canonical series.
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
		// TMDB-only (non-library) series: re-enrich by canonical id.
		res, cerr := h.refresher.RefreshByCanonical(ctx, seriesID)
		if cerr != nil {
			if errors.Is(cerr, ports.ErrNotFound) {
				c.JSON(http.StatusNotFound, dto.ErrorResponse{Error: "series_not_found"})
				return
			}
			_ = c.Error(cerr)
			return
		}
		h.logger.InfoContext(ctx, "global_regrab_dispatched_canonical",
			slog.Int64("series_id", int64(seriesID)),
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
