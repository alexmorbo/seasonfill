// Package rest — seriesdetail HTTP handlers.
//
// global_series_library_handler.go (Story 577 / E-1-B2). GET
// /api/v1/series/:id/library[?instance=] resolves the canonical
// series.id to a per-instance series_cache row and returns the Sonarr
// library-state projection (monitored / on-disk / missing / recent /
// next-to-air / in-progress). Mirrors global_series_torrents_handler.go
// (qBit). 204 when the series is in zero libraries (TMDB-only); 404
// instance_not_found when the requested instance does not carry it.
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
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
	"github.com/alexmorbo/seasonfill/internal/shared/http/dto"
)

// LibraryComposerPort is the narrow use-case surface the handler needs. The
// concrete *seriesdetail.LibraryComposer satisfies it; tests inject a fake.
type LibraryComposerPort interface {
	Compose(ctx context.Context, seriesID domain.SeriesID, instanceName domain.InstanceName) (seriesdetail.LibraryView, error)
}

// GlobalSeriesLibraryHandler exposes GET /api/v1/series/:id/library.
type GlobalSeriesLibraryHandler struct {
	composer    LibraryComposerPort
	cacheLookup seriesdetail.SeriesCacheLookupPort
	logger      *slog.Logger
}

// NewGlobalSeriesLibraryHandler constructs the handler. logger nil-OK.
func NewGlobalSeriesLibraryHandler(
	composer LibraryComposerPort,
	cacheLookup seriesdetail.SeriesCacheLookupPort,
	logger *slog.Logger,
) *GlobalSeriesLibraryHandler {
	if logger == nil {
		logger = slog.Default()
	}
	return &GlobalSeriesLibraryHandler{composer: composer, cacheLookup: cacheLookup, logger: logger}
}

// Get handles GET /api/v1/series/:id/library.
//
// @Summary     Per-series Sonarr library state (global)
// @Description Per-instance Sonarr library state keyed by canonical series.id:
// @Description monitored flag, episodes-on-disk, missing count, recent grab
// @Description events, next-episode-to-air, in-progress download, and
// @Description last-grab / last-import stamps. The instance query param is
// @Description optional (defaults to the lex-first instance carrying the
// @Description series). 204 when the series is in zero libraries (TMDB-only);
// @Description 404 instance_not_found when the named instance does not carry it.
// @Tags        series
// @Produce     json
// @Param       id        path   int     true   "Canonical series.id"
// @Param       instance  query  string  false  "Sonarr instance name (default: lex-first)"
// @Success     200 {object} dto.SeriesLibraryResponse
// @Success     204 "no content — series is in zero libraries (TMDB-only)"
// @Failure     400 {object} dto.ErrorResponse
// @Failure     401 {object} dto.ErrorResponse
// @Failure     404 {object} dto.ErrorResponse
// @Failure     500 {object} dto.ErrorResponse
// @Security    CookieAuth
// @Security    ApiKeyAuth
// @Router      /series/{id}/library [get]
func (h *GlobalSeriesLibraryHandler) Get(c *gin.Context) {
	parsedID, err := strconv.Atoi(c.Param("id"))
	if err != nil || parsedID <= 0 {
		c.JSON(http.StatusBadRequest, dto.ErrorResponse{Error: "invalid series id"})
		return
	}
	seriesID := domain.SeriesID(parsedID)
	instanceParam := strings.TrimSpace(c.Query("instance"))

	ctx := c.Request.Context()
	entries, err := h.cacheLookup.ListBySeriesID(ctx, seriesID)
	if err != nil {
		_ = c.Error(err)
		return
	}
	if len(entries) == 0 {
		// TMDB-only canon — no Sonarr library surface. The FE learns this
		// from the skeleton's in_library_instances=[] and does not call
		// /library, but a direct call gets a clean 204.
		c.Status(http.StatusNoContent)
		return
	}

	target, ok := resolveLibraryInstance(entries, instanceParam)
	if !ok {
		c.JSON(http.StatusNotFound, dto.ErrorResponse{Error: "instance_not_found"})
		return
	}
	if h.composer == nil {
		c.JSON(http.StatusInternalServerError, dto.ErrorResponse{Error: "library composer not wired"})
		return
	}

	view, err := h.composer.Compose(ctx, seriesID, target)
	if err != nil {
		if errors.Is(err, seriesdetail.ErrSeriesNotInInstance) {
			c.JSON(http.StatusNotFound, dto.ErrorResponse{Error: "instance_not_found"})
			return
		}
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusOK, toSeriesLibraryResponse(view))
}

// resolveLibraryInstance picks the target instance: the requested param when
// present (404 if it does not carry the series), else the lex-first instance —
// the same deterministic preference rule as resolvePreferredCacheEntry.
func resolveLibraryInstance(entries []series.CacheEntry, param string) (domain.InstanceName, bool) {
	if len(entries) == 0 {
		return "", false
	}
	if param != "" {
		for _, e := range entries {
			if string(e.InstanceName) == param {
				return e.InstanceName, true
			}
		}
		return "", false
	}
	preferred := entries[0].InstanceName
	for _, e := range entries[1:] {
		if e.InstanceName < preferred {
			preferred = e.InstanceName
		}
	}
	return preferred, true
}

// toSeriesLibraryResponse projects LibraryView → wire DTO. Pure mapping; no DB
// / network calls. Reuses dto.LibraryStrip / RecentEvent / InProgress /
// NextEpisode from series_detail.go.
func toSeriesLibraryResponse(v seriesdetail.LibraryView) dto.SeriesLibraryResponse {
	resp := dto.SeriesLibraryResponse{
		Instance:       v.Instance,
		SonarrSeriesID: v.SonarrSeriesID,
		SeriesID:       v.SeriesID,
		Monitored:      v.Monitored,
		Library: dto.LibraryStrip{
			Monitored:       v.Strip.Monitored,
			EpisodesTotal:   v.Strip.EpisodesTotal,
			EpisodesOnDisk:  v.Strip.EpisodesOnDisk,
			EpisodesAired:   v.Strip.EpisodesAired,
			MissingCount:    v.Strip.MissingCount,
			SizeOnDiskBytes: v.Strip.SizeOnDiskBytes,
			DominantQuality: v.Strip.DominantQuality,
		},
		Recent:         mapLibraryRecent(v.Recent),
		LastGrabAt:     v.LastGrabAt,
		LastImportedAt: v.LastImportedAt,
		SyncedAt:       v.SyncedAt,
		Seasons:        mapLibrarySeasonCounts(v.SeasonCounts),
	}
	if v.InProgress != nil {
		ip := &dto.InProgress{
			SeasonNumber:  v.InProgress.SeasonNumber,
			EpisodeNumber: v.InProgress.EpisodeNumber,
			Title:         v.InProgress.Title,
			Percent:       v.InProgress.Percent,
		}
		resp.InProgress = ip
		resp.Library.InProgress = ip
	}
	if v.Download != nil {
		resp.Download = &dto.DownloadChip{
			QueueID:      v.Download.QueueID,
			EpisodeID:    int(v.Download.SonarrEpisodeID),
			SeasonNumber: v.Download.SeasonNumber,
			Title:        v.Download.Title,
			Status:       v.Download.Status,
			Protocol:     v.Download.Protocol,
			DownloadID:   v.Download.DownloadID,
		}
	}
	if v.NextEpisodeToAir != nil {
		resp.NextEpisodeToAir = &dto.NextEpisode{
			SeasonNumber:  v.NextEpisodeToAir.SeasonNumber,
			EpisodeNumber: v.NextEpisodeToAir.EpisodeNumber,
			Title:         v.NextEpisodeToAir.Title,
			AirDate:       v.NextEpisodeToAir.AirDate,
		}
	}
	return resp
}

// mapLibraryRecent projects the app RecentItem slice onto the DTO. Always a
// non-nil slice so the FE can iterate unconditionally.
func mapLibraryRecent(items []seriesdetail.RecentItem) []dto.RecentEvent {
	out := make([]dto.RecentEvent, 0, len(items))
	for _, it := range items {
		out = append(out, dto.RecentEvent{EventType: it.EventType, Subject: it.Subject, At: it.At})
	}
	return out
}

// mapLibrarySeasonCounts projects the app per-season tallies onto the DTO.
// Always a non-nil slice so the FE can iterate unconditionally.
func mapLibrarySeasonCounts(counts []seriesdetail.LibrarySeasonCountView) []dto.LibrarySeasonCount {
	out := make([]dto.LibrarySeasonCount, 0, len(counts))
	for _, c := range counts {
		out = append(out, dto.LibrarySeasonCount{
			SeasonNumber:   c.SeasonNumber,
			EpisodesOnDisk: c.EpisodesOnDisk,
			Downloading:    c.Downloading,
		})
	}
	return out
}
