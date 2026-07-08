// Package rest — seriesdetail HTTP handlers.
//
// global_series_cast_handler.go (Story 492 / N-1b + Story 535). GET
// /api/v1/series/:id/cast resolves the canonical series.id to the
// preferred Sonarr instance via the cacheLookup (lex-first instance
// that carries this series), then delegates to the existing
// per-instance SeriesCastHandler. When NO instance carries the series
// (TMDB-only canon row), Story 535 dispatches to
// TMDBFallbackUseCase.GetCanonicalCast instead of returning 404 —
// mirrors the /overview + /recommendations fallback (Story 532).
// True unknown-id (no canon row at all) → 404 with body
// `{"error":"series_not_found"}`.
package rest

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/alexmorbo/seasonfill/internal/catalog/domain/series"
	seriesdetail "github.com/alexmorbo/seasonfill/internal/seriesdetail/app"
	ports "github.com/alexmorbo/seasonfill/internal/shared/dataports"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
	"github.com/alexmorbo/seasonfill/internal/shared/http/dto"
)

// TMDBFallbackCastPort is the narrow port the wrapper consumes for the
// TMDB-only fallback. *seriesdetail.TMDBFallbackUseCase satisfies it.
// nil-OK at construction — when nil, the wrapper falls back to the
// legacy 404 "series not in any library" response.
type TMDBFallbackCastPort interface {
	GetCanonicalCast(ctx context.Context, seriesID domain.SeriesID, lang string, limit int) (*seriesdetail.CastFallbackResult, error)
}

// GlobalSeriesCastHandler exposes GET /api/v1/series/:id/cast.
type GlobalSeriesCastHandler struct {
	inner        *SeriesCastHandler
	cacheLookup  seriesdetail.SeriesCacheLookupPort
	tmdbFallback TMDBFallbackCastPort
	logger       *slog.Logger
}

// NewGlobalSeriesCastHandler constructs the wrapper. inner is the
// existing per-instance handler (its route registration drops in this
// story; only this wrapper reaches it). tmdbFallback nil-OK — when nil
// the wrapper reverts to the pre-535 behaviour (404 when not in any
// library). logger nil-OK.
func NewGlobalSeriesCastHandler(
	inner *SeriesCastHandler,
	cacheLookup seriesdetail.SeriesCacheLookupPort,
	tmdbFallback TMDBFallbackCastPort,
	logger *slog.Logger,
) *GlobalSeriesCastHandler {
	if logger == nil {
		logger = slog.Default()
	}
	return &GlobalSeriesCastHandler{
		inner:        inner,
		cacheLookup:  cacheLookup,
		tmdbFallback: tmdbFallback,
		logger:       logger,
	}
}

// Get handles GET /api/v1/series/:id/cast.
//
// @Summary     Full series cast & crew (global)
// @Description Cast list for a series keyed by canonical series.id.
// @Description Resolves the preferred Sonarr instance automatically
// @Description (lex-first instance that carries the series in
// @Description series_cache). When the series is TMDB-only (no library
// @Description carries it), returns a canon-only cast list with
// @Description degraded=["tmdb_series"] and instance="". 404 only when
// @Description the canonical id is truly unknown.
// @Tags        series
// @Produce     json
// @Param       id    path      int     true   "Canonical series.id"
// @Param       lang  query     string  false  "BCP-47 language tag (default en-US)"
// @Success     200   {object}  dto.SeriesCastResponse
// @Failure     400   {object}  dto.ErrorResponse
// @Failure     401   {object}  dto.ErrorResponse
// @Failure     404   {object}  dto.ErrorResponse
// @Failure     500   {object}  dto.ErrorResponse
// @Security    CookieAuth
// @Security    ApiKeyAuth
// @Router      /series/{id}/cast [get]
func (h *GlobalSeriesCastHandler) Get(c *gin.Context) {
	parsedID, err := strconv.Atoi(c.Param("id"))
	if err != nil || parsedID <= 0 {
		c.JSON(http.StatusBadRequest, dto.ErrorResponse{Error: "invalid series id"})
		return
	}
	seriesID := domain.SeriesID(parsedID)
	lang := strings.TrimSpace(c.Query("lang"))
	// Story 1087a — optional ?limit=N. 0 (absent/invalid/<=0) means the full
	// cast page: the fallback path then defaults to CastFullPageDefaultLimit
	// (200) below, preserving Story 541 behaviour.
	limit := parseCastLimit(c)

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
		// Story 541 + 1087a — absent ?limit keeps the full-page default (200)
		// so the cast PAGE returns the full DB list; an explicit ?limit=N caps
		// it (top-N by episode_count, applied inside GetCanonicalCast). The
		// hero-carousel inside /series/:id keeps CastDefaultLimit=10 via
		// Composer.loadTopCast — distinct UX surface.
		fallbackLimit := limit
		if fallbackLimit <= 0 {
			fallbackLimit = seriesdetail.CastFullPageDefaultLimit
		}
		cast, ferr := h.tmdbFallback.GetCanonicalCast(ctx, seriesID, lang, fallbackLimit)
		if ferr != nil {
			if errors.Is(ferr, ports.ErrNotFound) {
				c.JSON(http.StatusNotFound, dto.ErrorResponse{Error: "series_not_found"})
				return
			}
			_ = c.Error(ferr)
			return
		}
		h.logger.InfoContext(ctx, "global_series_cast_tmdb_fallback",
			slog.Int64("series_id", int64(seriesID)),
			slog.String("lang", lang),
			slog.Int("cast_count", len(cast.Cast)),
			slog.Int("degraded_count", len(cast.Degraded)))
		c.JSON(http.StatusOK, toSeriesCastResponseFromFallback(cast))
		return
	}
	if h.inner == nil {
		c.JSON(http.StatusInternalServerError, dto.ErrorResponse{Error: "cast handler not wired"})
		return
	}

	// Splice :name + :id into c.Params so the inner per-instance
	// handler reads them via its existing c.Param lookups. setParam
	// REPLACES :id (gin's Params.Get returns the FIRST match so a plain
	// append on `id` would leave the inner handler reading the canonical
	// series.id from the URL instead of the per-instance Sonarr id).
	c.Params = setParam(c.Params, "name", string(preferred.InstanceName))
	c.Params = setParam(c.Params, "id", strconv.Itoa(int(preferred.SonarrSeriesID)))
	h.inner.Get(c)
}

// toSeriesCastResponseFromFallback projects a CastFallbackResult onto
// dto.SeriesCastResponse. Instance="" and SonarrSeriesID=0 mark the
// canon-only origin; SeriesSummary is built from the Canon row.
// TotalEpisodeCount is 0 — TMDB-only series have no Sonarr-side
// episodes; the FE renders the Main/Recurring/Guest badges as N/A.
func toSeriesCastResponseFromFallback(r *seriesdetail.CastFallbackResult) dto.SeriesCastResponse {
	resp := dto.SeriesCastResponse{
		Instance:       "",
		SonarrSeriesID: 0,
		SeriesID:       r.SeriesID,
		Lang:           r.Lang,
		SeriesSummary:  buildFallbackSeriesSummary(r.Canon, r.Title, r.PosterAsset),
		Cast:           make([]dto.CastPageMember, 0, len(r.Cast)),
		Crew:           []dto.CrewPageMember{},
		Degraded:       append([]string{}, r.Degraded...),
	}
	for _, e := range r.Cast {
		resp.Cast = append(resp.Cast, dto.CastPageMember{
			PersonID:      e.Person.ID,
			TMDBID:        e.Person.TMDBID,
			Name:          e.Person.Name,
			ProfileAsset:  e.Person.ProfileAsset,
			CharacterName: e.Credit.CharacterName,
			CreditOrder:   e.Credit.CreditOrder,
			EpisodeCount:  e.Credit.EpisodeCount,
			InLibrary:     false, // canon-only: no probe (TMDB-only series cannot be in_library)
		})
	}
	return resp
}

// buildFallbackSeriesSummary mirrors buildSeriesSummary in
// internal/seriesdetail/app/cast.go (project-private). We don't import
// across the layer — instead inline the projection here. Status uses the
// same token mapping as the composer (continuing/ended/canceled/
// in_production/upcoming/unknown).
// S-E3a — title + posterURL are staged upstream (series_texts /
// series_media_texts → en-US, Title falls back to OriginalTitle) since canon
// no longer carries them; passed in rather than read off the canon row.
func buildFallbackSeriesSummary(c series.Canon, title string, posterURL *string) dto.SeriesSummary {
	s := dto.SeriesSummary{
		Title:     title,
		PosterURL: posterURL,
		Status:    mapStatusTokenForFallback(c.Status, c.InProduction),
	}
	if c.Year != nil {
		ys := *c.Year
		s.FirstAiredYear = &ys
	}
	if c.LastAirDate != nil {
		ye := c.LastAirDate.Year()
		s.LastAiredYear = &ye
	}
	return s
}

// mapStatusTokenForFallback duplicates the composer-side mapping (cast.go
// in package seriesdetail). Lifting it to a shared helper is a separate
// refactor — kept inline so this story stays scoped to the bug fix.
// TODO: extract to a shared helper alongside the composer-side copy.
func mapStatusTokenForFallback(status *string, inProduction bool) string {
	raw := ""
	if status != nil {
		raw = strings.ToLower(strings.TrimSpace(*status))
	}
	switch {
	case strings.Contains(raw, "cancel"):
		return "canceled"
	case strings.Contains(raw, "ended"):
		return "ended"
	case strings.Contains(raw, "upcoming") || strings.Contains(raw, "planned"):
		return "upcoming"
	case strings.Contains(raw, "production") && !strings.Contains(raw, "post"):
		return "in_production"
	case strings.Contains(raw, "continu") || strings.Contains(raw, "ongoing") || strings.Contains(raw, "returning"):
		return "continuing"
	case inProduction:
		return "in_production"
	case raw == "":
		return "unknown"
	}
	return "unknown"
}
