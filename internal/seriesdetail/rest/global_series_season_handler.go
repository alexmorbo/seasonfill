// Package rest — seriesdetail HTTP handlers.
//
// global_series_season_handler.go (Story 492 / N-1b). GET
// /api/v1/series/:id/season/:n resolves the canonical series.id to the
// preferred Sonarr instance via the cacheLookup (lex-first instance
// that carries this series), then delegates to the existing
// per-instance SeriesSeasonHandler by splicing :name + :id into
// c.Params and invoking it. The legacy
// /api/v1/instances/:name/series/:id/season/:n route is DELETED in this
// same story.
//
// TMDB-only fallback: viewing a series must work WITHOUT a Sonarr
// instance — Sonarr only overlays library/on-disk state. When NO
// instance carries the series (in_library_instances=[]), the wrapper
// dispatches to TMDBFallbackSeasonPort.GetSeason instead of returning
// 404 — mirrors the sibling /overview + /cast fallbacks. The season's
// canon episodes (episodes + episode_texts) render with
// degraded=["tmdb_series"], instance="", sonarr_series_id=0, and no
// on-disk badges. True unknown-id (no canon row) → 404 series_not_found.
package rest

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	seriesdetail "github.com/alexmorbo/seasonfill/internal/seriesdetail/app"
	ports "github.com/alexmorbo/seasonfill/internal/shared/dataports"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
	"github.com/alexmorbo/seasonfill/internal/shared/http/dto"
)

// TMDBFallbackSeasonPort is the narrow port the wrapper consumes for the
// TMDB-only season fallback. *seriesdetail.TMDBFallbackUseCase satisfies
// it. nil-OK at construction — when nil, the wrapper falls back to the
// legacy 404 "series not in any library" response.
type TMDBFallbackSeasonPort interface {
	GetSeason(ctx context.Context, seriesID domain.SeriesID, seasonNumber int, lang string) (*seriesdetail.Detail, error)
}

// GlobalSeriesSeasonHandler exposes GET /api/v1/series/:id/season/:n.
type GlobalSeriesSeasonHandler struct {
	inner        *SeriesSeasonHandler
	cacheLookup  seriesdetail.SeriesCacheLookupPort
	tmdbFallback TMDBFallbackSeasonPort
	logger       *slog.Logger
}

// NewGlobalSeriesSeasonHandler constructs the wrapper. inner is the
// existing per-instance handler (its route registration drops in this
// story; only this wrapper reaches it). tmdbFallback is nil-OK — when
// nil, a non-library series keeps the legacy 404. logger nil-OK.
func NewGlobalSeriesSeasonHandler(
	inner *SeriesSeasonHandler,
	cacheLookup seriesdetail.SeriesCacheLookupPort,
	tmdbFallback TMDBFallbackSeasonPort,
	logger *slog.Logger,
) *GlobalSeriesSeasonHandler {
	if logger == nil {
		logger = slog.Default()
	}
	return &GlobalSeriesSeasonHandler{
		inner:        inner,
		cacheLookup:  cacheLookup,
		tmdbFallback: tmdbFallback,
		logger:       logger,
	}
}

// Get handles GET /api/v1/series/:id/season/:n.
//
// @Summary     Series season detail (global)
// @Description Season detail document for a series keyed by canonical
// @Description series.id. Resolves the preferred Sonarr instance
// @Description automatically (lex-first instance that carries the series
// @Description in series_cache). When the series is TMDB-only (no library
// @Description carries it), returns a canon-only season detail (episodes
// @Description from episodes + episode_texts) with degraded=["tmdb_series"],
// @Description instance="" and no on-disk state. 404 only when the
// @Description canonical id is truly unknown.
// @Tags        series
// @Produce     json
// @Param       id    path      int     true   "Canonical series.id"
// @Param       n     path      int     true   "Season number (0 = Specials)"
// @Param       lang  query     string  false  "BCP-47 language tag (default en-US)"
// @Success     200   {object}  dto.SeasonDetailResponse
// @Failure     400   {object}  dto.ErrorResponse
// @Failure     401   {object}  dto.ErrorResponse
// @Failure     404   {object}  dto.ErrorResponse
// @Failure     500   {object}  dto.ErrorResponse
// @Security    CookieAuth
// @Security    ApiKeyAuth
// @Router      /series/{id}/season/{n} [get]
func (h *GlobalSeriesSeasonHandler) Get(c *gin.Context) {
	parsedID, err := strconv.Atoi(c.Param("id"))
	if err != nil || parsedID <= 0 {
		c.JSON(http.StatusBadRequest, dto.ErrorResponse{Error: "invalid series id"})
		return
	}
	seriesID := domain.SeriesID(parsedID)
	// Pre-validate :n so the wrapper fails fast on a malformed season
	// number before doing a cache lookup. The inner handler validates
	// again — kept defensive on both sides.
	seasonNumber, err := strconv.Atoi(c.Param("n"))
	if err != nil || seasonNumber < 0 {
		c.JSON(http.StatusBadRequest, dto.ErrorResponse{Error: "invalid season number"})
		return
	}

	ctx := c.Request.Context()
	preferred, ok, err := resolvePreferredCacheEntry(ctx, h.cacheLookup, seriesID)
	if err != nil {
		_ = c.Error(err)
		return
	}
	if !ok {
		if h.tmdbFallback == nil {
			c.JSON(http.StatusNotFound, dto.ErrorResponse{Error: "series not in any library"})
			return
		}
		lang := strings.TrimSpace(c.Query("lang"))
		detail, ferr := h.tmdbFallback.GetSeason(ctx, seriesID, seasonNumber, lang)
		if ferr != nil {
			if errors.Is(ferr, ports.ErrNotFound) {
				c.JSON(http.StatusNotFound, dto.ErrorResponse{Error: "series_not_found"})
				return
			}
			_ = c.Error(ferr)
			return
		}
		if len(detail.Seasons) == 0 {
			c.JSON(http.StatusNotFound, dto.ErrorResponse{Error: "season not found"})
			return
		}
		seasons := mapSeasons(detail)
		h.logger.InfoContext(ctx, "global_series_season_tmdb_fallback",
			slog.Int64("series_id", int64(seriesID)),
			slog.Int("season_number", seasonNumber),
			slog.String("lang", lang))
		c.JSON(http.StatusOK, dto.SeasonDetailResponse{
			Instance:       detail.Instance,
			SonarrSeriesID: detail.SonarrSeriesID,
			SeriesID:       detail.SeriesID,
			Lang:           detail.Lang,
			Season:         seasons[0],
			ServedLanguage: detail.ServedLanguage,
			Degraded:       seriesdetail.AppendMissingLang(sourceStringSlice(detail.Degraded), detail.ServedLanguage, detail.Lang),
			SyncedAt:       detail.SyncedAt,
		})
		return
	}
	if h.inner == nil {
		c.JSON(http.StatusInternalServerError, dto.ErrorResponse{Error: "season handler not wired"})
		return
	}
	// :n already in c.Params from gin — leave untouched. :id needs
	// rewriting (canonical → per-instance sonarr id); :name needs
	// splicing.
	c.Params = setParam(c.Params, "name", string(preferred.InstanceName))
	c.Params = setParam(c.Params, "id", strconv.Itoa(int(preferred.SonarrSeriesID)))
	h.inner.Get(c)
}
