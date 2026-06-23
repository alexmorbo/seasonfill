// Package rest — catalog HTTP handlers.
//
// global_season_episodes_handler.go (Story 492 / N-1b). GET
// /api/v1/series/:id/seasons/:season/episodes resolves the canonical
// series.id to the preferred Sonarr instance via the cache lookup
// (lex-first instance that carries this series), then delegates to the
// existing InstancesHandler.SeasonEpisodes after rewriting :id from
// the canonical series.id to the per-instance sonarr_series_id and
// splicing :name into c.Params. 404 when the series is in zero
// libraries. The legacy
// /api/v1/instances/:name/series/:id/seasons/:season/episodes route
// is DELETED in this same story.
package rest

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"github.com/alexmorbo/seasonfill/internal/catalog/domain/series"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
	"github.com/alexmorbo/seasonfill/internal/shared/http/dto"
)

// SeasonEpisodesCacheLookupPort is the narrow interface this wrapper
// needs from series_cache — the SeriesCacheRepository.ListBySeriesID
// projection. Defined locally to avoid an import cycle on
// seriesdetail/app from the catalog/rest package and to keep test
// stubs minimal.
type SeasonEpisodesCacheLookupPort interface {
	ListBySeriesID(ctx context.Context, seriesID domain.SeriesID) ([]series.CacheEntry, error)
}

// GlobalSeasonEpisodesHandler exposes
// GET /api/v1/series/:id/seasons/:season/episodes.
type GlobalSeasonEpisodesHandler struct {
	inner       *InstancesHandler
	cacheLookup SeasonEpisodesCacheLookupPort
	logger      *slog.Logger
}

// NewGlobalSeasonEpisodesHandler constructs the wrapper. inner is the
// existing per-instance handler (its route registration drops in this
// story; only this wrapper reaches it). logger nil-OK.
func NewGlobalSeasonEpisodesHandler(
	inner *InstancesHandler,
	cacheLookup SeasonEpisodesCacheLookupPort,
	logger *slog.Logger,
) *GlobalSeasonEpisodesHandler {
	if logger == nil {
		logger = slog.Default()
	}
	return &GlobalSeasonEpisodesHandler{inner: inner, cacheLookup: cacheLookup, logger: logger}
}

// Get handles GET /api/v1/series/:id/seasons/:season/episodes.
//
// @Summary     Sonarr-side season episodes (global)
// @Description Same shape as the per-instance
// @Description /api/v1/instances/{name}/series/{id}/seasons/{season}/episodes,
// @Description but resolves the preferred Sonarr instance
// @Description automatically from the canonical series.id. 404 when no
// @Description library carries the series.
// @Tags        series
// @Produce     json
// @Param       id      path  int  true  "Canonical series.id"
// @Param       season  path  int  true  "Season number (0 = Specials)"
// @Success     200     {object} dto.SeasonEpisodeList
// @Failure     400     {object} dto.ErrorResponse
// @Failure     401     {object} dto.ErrorResponse
// @Failure     404     {object} dto.ErrorResponse
// @Failure     500     {object} dto.ErrorResponse
// @Security    CookieAuth
// @Security    ApiKeyAuth
// @Router      /series/{id}/seasons/{season}/episodes [get]
func (h *GlobalSeasonEpisodesHandler) Get(c *gin.Context) {
	parsedID, err := strconv.Atoi(c.Param("id"))
	if err != nil || parsedID <= 0 {
		c.JSON(http.StatusBadRequest, dto.ErrorResponse{Error: "invalid series id"})
		return
	}
	seriesID := domain.SeriesID(parsedID)
	if s, err := strconv.Atoi(c.Param("season")); err != nil || s < 0 {
		c.JSON(http.StatusBadRequest, dto.ErrorResponse{Error: "invalid season number"})
		return
	}

	ctx := c.Request.Context()
	if h.cacheLookup == nil {
		c.JSON(http.StatusInternalServerError, dto.ErrorResponse{Error: "season episodes handler not wired"})
		return
	}
	entries, err := h.cacheLookup.ListBySeriesID(ctx, seriesID)
	if err != nil {
		_ = c.Error(fmt.Errorf("resolve preferred cache entry: %w", err))
		return
	}
	if len(entries) == 0 {
		c.JSON(http.StatusNotFound, dto.ErrorResponse{Error: "series not in any library"})
		return
	}
	preferred := entries[0]
	for _, e := range entries[1:] {
		if e.InstanceName < preferred.InstanceName {
			preferred = e
		}
	}
	if h.inner == nil {
		c.JSON(http.StatusInternalServerError, dto.ErrorResponse{Error: "season episodes handler not wired"})
		return
	}
	// Replace :id (gin's Params.Get returns the FIRST match — append
	// alone would leave the inner handler reading the canonical
	// series.id from the URL instead of the per-instance Sonarr id).
	// :season already in c.Params from gin — leave untouched.
	c.Params = setSeasonEpisodesParam(c.Params, "name", string(preferred.InstanceName))
	c.Params = setSeasonEpisodesParam(c.Params, "id", strconv.Itoa(int(preferred.SonarrSeriesID)))
	h.inner.SeasonEpisodes(c)
}

// setSeasonEpisodesParam replaces an existing c.Params entry by key,
// or appends it when absent. Local to catalog/rest because we don't
// share a helpers file with seriesdetail/rest (different package).
func setSeasonEpisodesParam(params gin.Params, key, value string) gin.Params {
	for i := range params {
		if params[i].Key == key {
			params[i].Value = value
			return params
		}
	}
	return append(params, gin.Param{Key: key, Value: value})
}
