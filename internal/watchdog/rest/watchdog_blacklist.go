package rest

import (
	"context"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/alexmorbo/seasonfill/interface/http/handlers"
	"github.com/alexmorbo/seasonfill/internal/catalog/domain/series"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
	"github.com/alexmorbo/seasonfill/internal/shared/http/dto"
	"github.com/alexmorbo/seasonfill/internal/watchdog/domain/regrab"
)

// SeriesTitleResolver looks up the cached series title for one
// (instance, series_id) pair. ports.SeriesCacheRepository.Get satisfies
// it directly; tests stub it.
type SeriesTitleResolver interface {
	Get(ctx context.Context, instanceName domain.InstanceName, sonarrSeriesID domain.SonarrSeriesID) (series.CacheEntry, error)
}

// BlacklistPager is the narrowed slice of WatchdogBlacklistRepository
// the handler reads. The production *repositories.WatchdogBlacklistRepository
// satisfies it via the new methods added by 047b.
type BlacklistPager interface {
	ListByInstanceWithLimit(ctx context.Context, instanceID uint, limit int, afterCreatedAt time.Time, afterID uint) ([]regrab.BlacklistEntry, error)
	DeleteByID(ctx context.Context, instanceID, id uint) error
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
// @Description Keyset-paginated by (created_at DESC, id DESC).
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

	id, ok, err := h.instanceLookup.IDByName(ctx, name)
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
	var afterID uint
	if raw := c.Query("cursor"); raw != "" {
		at, idu, perr := decodeBlacklistCursor(raw)
		if perr != nil {
			handlers.WriteError(c, http.StatusBadRequest, "invalid cursor")
			return
		}
		afterAt = at
		afterID = idu
	}

	rows, err := h.pager.ListByInstanceWithLimit(ctx, id, limit, afterAt, afterID)
	if err != nil {
		handlers.WriteInternalError(c, h.logger, "watchdog_blacklist_list_failed", err,
			slog.String("instance", name))
		return
	}

	out := dto.WatchdogBlacklistList{Items: make([]dto.WatchdogBlacklistItem, 0, len(rows))}
	for _, r := range rows {
		title := ""
		if entry, terr := h.titles.Get(ctx, domain.InstanceName(name), r.SeriesID); terr == nil {
			title = entry.Title
		}
		out.Items = append(out.Items, dto.WatchdogBlacklistItem{
			ID:           r.ID,
			InstanceName: domain.InstanceName(name),
			SeriesID:     r.SeriesID,
			SeriesTitle:  title,
			SeasonNumber: r.SeasonNumber,
			Reason:       string(r.Reason),
			Source:       deriveSource(r.Reason),
			Consecutive:  r.Consecutive,
			CreatedAt:    r.CreatedAt,
			ExpiresAt:    r.ExpiresAt,
		})
	}

	if len(rows) == limit && len(rows) > 0 {
		out.NextCursor = encodeBlacklistCursor(rows[len(rows)-1].CreatedAt, rows[len(rows)-1].ID)
	}

	c.JSON(http.StatusOK, out)
}

// Delete handles DELETE /api/v1/instances/:name/watchdog/blacklist/:id.
//
// @Summary     Un-blacklist a Watchdog row
// @Tags        watchdog
// @Produce     json
// @Param       name  path  string  true  "Instance name"
// @Param       id    path  int     true  "Blacklist row id"
// @Success     204
// @Failure     404   {object}  dto.ErrorResponse
// @Failure     500   {object}  dto.ErrorResponse
// @Security    CookieAuth
// @Security    ApiKeyAuth
// @Router      /instances/{name}/watchdog/blacklist/{id} [delete]
func (h *WatchdogBlacklistHandler) Delete(c *gin.Context) {
	name := c.Param("name")
	rawID := c.Param("id")
	ctx := c.Request.Context()

	id, err := strconv.ParseUint(rawID, 10, 64)
	if err != nil {
		handlers.WriteError(c, http.StatusBadRequest, "invalid id")
		return
	}

	instanceID, ok, err := h.instanceLookup.IDByName(ctx, name)
	if err != nil {
		handlers.WriteInternalError(c, h.logger, "watchdog_blacklist_delete_lookup_failed", err,
			slog.String("instance", name))
		return
	}
	if !ok {
		handlers.WriteError(c, http.StatusNotFound, "unknown instance: "+name)
		return
	}

	if err := h.pager.DeleteByID(ctx, instanceID, uint(id)); err != nil {
		_ = c.Error(err)
		return
	}

	h.logger.InfoContext(ctx, "watchdog_blacklist_unparked",
		slog.String("instance", name),
		slog.Uint64("id", id),
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
