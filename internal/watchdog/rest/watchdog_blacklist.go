package rest

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/alexmorbo/seasonfill/internal/catalog/domain/series"
	ports "github.com/alexmorbo/seasonfill/internal/shared/dataports"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
	sharedErrors "github.com/alexmorbo/seasonfill/internal/shared/errors"
	"github.com/alexmorbo/seasonfill/internal/shared/http/dto"
	"github.com/alexmorbo/seasonfill/internal/shared/http/handlers"
	"github.com/alexmorbo/seasonfill/internal/watchdog/domain/regrab"
)

// SeriesTitleResolver looks up the cached series title for one
// (instance, series_id) pair. ports.SeriesCacheRepository.Get satisfies
// it directly; tests stub it.
type SeriesTitleResolver interface {
	Get(ctx context.Context, instanceName domain.InstanceName, sonarrSeriesID domain.SonarrSeriesID) (series.CacheEntry, error)
}

// BlacklistPager is the narrowed slice of WatchdogBlacklistRepository
// the handler reads. D-1 / 467b: composite PK on triple, no surrogate
// id. The keyset cursor is now three columns (blacklisted_at,
// sonarr_series_id, season_number).
type BlacklistPager interface {
	ListByInstanceWithLimit(ctx context.Context, instance domain.InstanceName, limit int, afterBlacklistedAt time.Time, afterSeriesID domain.SonarrSeriesID, afterSeason int) ([]regrab.BlacklistEntry, error)
	DeleteByTriple(ctx context.Context, instance domain.InstanceName, seriesID domain.SonarrSeriesID, season int) error
}

// WatchdogBlacklistHandler serves list + delete on the blacklist table.
type WatchdogBlacklistHandler struct {
	pager          BlacklistPager
	titles         SeriesTitleResolver
	instanceLookup InstanceIDLookup
	logger         *slog.Logger
}

// NewWatchdogBlacklistHandler wires the handler. logger=nil → slog.Default().
func NewWatchdogBlacklistHandler(
	pager BlacklistPager,
	titles SeriesTitleResolver,
	instanceLookup InstanceIDLookup,
	logger *slog.Logger,
) *WatchdogBlacklistHandler {
	if logger == nil {
		logger = slog.Default()
	}
	return &WatchdogBlacklistHandler{
		pager:          pager,
		titles:         titles,
		instanceLookup: instanceLookup,
		logger:         logger,
	}
}

// List handles GET /api/v1/instances/:name/watchdog/blacklist.
//
// @Summary     List Watchdog blacklist entries for an instance
// @Description Keyset-paginated by (blacklisted_at DESC, series_id DESC, season_number DESC).
// @Tags        watchdog
// @Produce     json
// @Param       name    path   string  true   "Instance name"
// @Param       limit   query  int     false  "Page size (default 50, max 200)"
// @Param       cursor  query  string  false  "Opaque next_cursor"
// @Success     200  {object}  dto.WatchdogBlacklistList
// @Failure     404  {object}  dto.ErrorResponse
// @Failure     500  {object}  dto.ErrorResponse
// @Security    CookieAuth
// @Security    ApiKeyAuth
// @Router      /instances/{name}/watchdog/blacklist [get]
func (h *WatchdogBlacklistHandler) List(c *gin.Context) {
	name := c.Param("name")
	ctx := c.Request.Context()

	// D-1 / 467b: instance lookup is now name-only — the repository keys
	// on instance_name, no surrogate id. The pre-flight check still
	// resolves the name to confirm the instance exists.
	_, ok, err := h.instanceLookup.IDByName(ctx, name)
	if err != nil {
		handlers.WriteInternalError(c, h.logger, "watchdog_blacklist_list_lookup_failed", err,
			slog.String("instance", name))
		return
	}
	if !ok {
		handlers.WriteError(c, http.StatusNotFound, "unknown instance: "+name)
		return
	}

	limit, err := handlers.ParseLimit(c)
	if handlers.HandleQueryErr(c, err) {
		return
	}

	var afterAt time.Time
	var afterSeries domain.SonarrSeriesID
	var afterSeason int
	if raw := c.Query("cursor"); raw != "" {
		at, sid, season, perr := decodeBlacklistCursor(raw)
		if perr != nil {
			handlers.WriteError(c, http.StatusBadRequest, "invalid cursor")
			return
		}
		afterAt = at
		afterSeries = sid
		afterSeason = season
	}

	instance := domain.InstanceName(name)
	rows, err := h.pager.ListByInstanceWithLimit(ctx, instance, limit, afterAt, afterSeries, afterSeason)
	if err != nil {
		handlers.WriteInternalError(c, h.logger, "watchdog_blacklist_list_failed", err,
			slog.String("instance", name))
		return
	}

	out := dto.WatchdogBlacklistList{Items: make([]dto.WatchdogBlacklistItem, 0, len(rows))}
	for _, r := range rows {
		title := ""
		if entry, terr := h.titles.Get(ctx, instance, r.SeriesID); terr == nil {
			title = entry.Title
		}
		out.Items = append(out.Items, dto.WatchdogBlacklistItem{
			InstanceName: instance,
			SeriesID:     r.SeriesID,
			SeriesTitle:  title,
			SeasonNumber: r.SeasonNumber,
			Reason:       string(r.Reason),
			Source:       deriveSource(r.Reason),
			Consecutive:  r.Consecutive,
			ReleaseTitle: r.ReleaseTitle,
			CreatedAt:    r.CreatedAt,
			TTLUntil:     r.TTLUntil,
		})
	}

	if len(rows) == limit && len(rows) > 0 {
		last := rows[len(rows)-1]
		out.NextCursor = encodeBlacklistCursor(last.CreatedAt, last.SeriesID, last.SeasonNumber)
	}

	c.JSON(http.StatusOK, out)
}

// Delete handles DELETE /api/v1/instances/:name/watchdog/blacklist/:series/:season.
//
// D-1 / 467b: URL pattern shifted from legacy `/{id}` to
// `/{series}/{season}` since the composite PK is now the lookup key.
//
// @Summary     Un-blacklist a Watchdog row
// @Tags        watchdog
// @Produce     json
// @Param       name    path  string  true  "Instance name"
// @Param       series  path  int     true  "Sonarr series id"
// @Param       season  path  int     true  "Season number"
// @Success     204
// @Failure     404   {object}  dto.ErrorResponse
// @Failure     500   {object}  dto.ErrorResponse
// @Security    CookieAuth
// @Security    ApiKeyAuth
// @Router      /instances/{name}/watchdog/blacklist/{series}/{season} [delete]
func (h *WatchdogBlacklistHandler) Delete(c *gin.Context) {
	name := c.Param("name")
	rawSeries := c.Param("series")
	rawSeason := c.Param("season")
	ctx := c.Request.Context()

	sid, err := strconv.ParseInt(rawSeries, 10, 64)
	if err != nil || sid <= 0 {
		handlers.WriteError(c, http.StatusBadRequest, "invalid series id")
		return
	}
	season, err := strconv.Atoi(rawSeason)
	if err != nil || season < 0 {
		handlers.WriteError(c, http.StatusBadRequest, "invalid season")
		return
	}

	_, ok, err := h.instanceLookup.IDByName(ctx, name)
	if err != nil {
		handlers.WriteInternalError(c, h.logger, "watchdog_blacklist_delete_lookup_failed", err,
			slog.String("instance", name))
		return
	}
	if !ok {
		handlers.WriteError(c, http.StatusNotFound, "unknown instance: "+name)
		return
	}

	instance := domain.InstanceName(name)
	seriesID := domain.SonarrSeriesID(sid)
	if err := h.pager.DeleteByTriple(ctx, instance, seriesID, season); err != nil {
		if errors.Is(err, ports.ErrNotFound) {
			_ = c.Error(&sharedErrors.WatchdogBlacklistNotFoundError{
				Instance: name,
				SeriesID: sid,
				Season:   season,
			})
			return
		}
		_ = c.Error(err)
		return
	}

	h.logger.InfoContext(ctx, "watchdog_blacklist_unparked",
		slog.String("instance", name),
		slog.Int64("series_id", sid),
		slog.Int("season", season),
	)
	c.Status(http.StatusNoContent)
}

// deriveSource maps the persisted reason enum onto the wire-level
// `source` discriminator. v1 has a single write path
// (ReasonConsecutiveNoBetter); future operator-driven blacklisting
// will introduce a separate reason value that maps to "manual" without
// breaking this rule.
func deriveSource(r regrab.Reason) string {
	if r == regrab.ReasonConsecutiveNoBetter {
		return "auto"
	}
	return "manual"
}
