package handlers

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"github.com/alexmorbo/seasonfill/interface/http/dto"
	"github.com/alexmorbo/seasonfill/internal/catalog/app/torrentsync"
	seriesdetail "github.com/alexmorbo/seasonfill/internal/seriesdetail/app"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
)

// SeriesTorrentsHandler serves the per-series torrents document
// powering the series-detail torrents tab (PRD §9 row 2, story
// 222 / PRD §12 row A-4).
//
// GET /api/v1/instances/:name/series/:id/torrents
//
// The handler merges the in-memory store with a durable
// qbit_torrents fallback for hashes that have disappeared from
// qBit, returns one row per torrent with a `live` discriminator,
// and short-circuits via If-None-Match against an ETag computed
// from synced_at + len(torrents).
type SeriesTorrentsHandler struct {
	query  *torrentsync.Query
	cache  seriesdetail.SeriesCachePort
	series seriesdetail.SeriesPort
	logger *slog.Logger
}

// NewSeriesTorrentsHandler wires the handler. `series` is the
// canon-row port — invoked to confirm the resolved series_id
// actually exists (parity with stories 215 / 216's 404 path).
func NewSeriesTorrentsHandler(
	query *torrentsync.Query,
	cache seriesdetail.SeriesCachePort,
	seriesRepo seriesdetail.SeriesPort,
	logger *slog.Logger,
) *SeriesTorrentsHandler {
	if logger == nil {
		logger = slog.Default()
	}
	return &SeriesTorrentsHandler{
		query:  query,
		cache:  cache,
		series: seriesRepo,
		logger: logger,
	}
}

// Get handles GET /api/v1/instances/:name/series/:id/torrents.
//
// @Summary     Per-series torrent inventory
// @Description Returns the merged torrent inventory for a single
// @Description series — live data from the in-memory torrentsync
// @Description store overlaid with a durable qbit_torrents
// @Description fallback for hashes that have disappeared from
// @Description qBit (e.g. deleted, qBit unreachable). Each row
// @Description carries the full qBit column set plus a `live`
// @Description discriminator the UI uses to grey out live cells
// @Description on DB-only rows. Default sort is `added_on DESC`.
// @Description
// @Description The endpoint short-circuits via `If-None-Match`:
// @Description ETag is `sha256(synced_at_unix + len(torrents))`
// @Description rendered as a quoted hex string. Granularity is
// @Description per-second — enough for the SPA's 3-second poll.
// @Tags        instances
// @Produce     json
// @Param       name  path      string  true   "Instance name"
// @Param       id    path      int     true   "Sonarr series id (per-instance)"
// @Success     200   {object}  dto.SeriesTorrentsResponse
// @Success     304   "not modified — If-None-Match matched the current ETag"
// @Failure     400   {object}  dto.ErrorResponse
// @Failure     401   {object}  dto.ErrorResponse
// @Failure     404   {object}  dto.ErrorResponse
// @Failure     500   {object}  dto.ErrorResponse
// @Security    CookieAuth
// @Security    ApiKeyAuth
// @Router      /instances/{name}/series/{id}/torrents [get]
func (h *SeriesTorrentsHandler) Get(c *gin.Context) {
	name := c.Param("name")
	idStr := c.Param("id")
	parsedID, err := strconv.Atoi(idStr)
	if err != nil || parsedID <= 0 {
		c.JSON(http.StatusBadRequest, dto.ErrorResponse{Error: "invalid series id"})
		return
	}
	sonarrID := domain.SonarrSeriesID(parsedID)

	ctx := c.Request.Context()

	// Step 1 — resolve series_cache → canon series_id. Same
	// invariant as 215 / 216: unknown (instance, sonarrID) →
	// 404; cache row without canon series_id (broken legacy
	// row) also → 404.
	cache, err := h.cache.Get(ctx, domain.InstanceName(name), sonarrID)
	if err != nil {
		_ = c.Error(err)
		return
	}
	if cache.SeriesID == nil || *cache.SeriesID == 0 {
		c.JSON(http.StatusNotFound, dto.ErrorResponse{Error: "series not found"})
		return
	}
	seriesID := *cache.SeriesID

	// Step 2 — confirm the canon row exists. Empty canon →
	// 404 via typed-error dispatch (middleware emits series_not_found).
	if _, gerr := h.series.Get(ctx, seriesID); gerr != nil {
		_ = c.Error(gerr)
		return
	}

	// Step 3 — merge live store + DB fallback for this series.
	result, err := h.query.BySeriesID(ctx, domain.InstanceName(name), sonarrID)
	if err != nil {
		writeInternalError(c, h.logger, "series_torrents_query_failed", err,
			slog.String("instance_name", name),
			slog.Int("sonarr_series_id", int(sonarrID)),
			slog.Int64("series_id", int64(seriesID)))
		return
	}

	// Step 4 — compute ETag from synced_at + len. Per PRD
	// §12 row A-4 the granularity is per-second; we use the
	// synced_at unix seconds + the row count so adding /
	// removing a torrent inside the same wall second still
	// produces a new hash.
	etag := computeTorrentsETag(result.SyncedAt.Unix(), len(result.Rows))
	if match := c.GetHeader("If-None-Match"); match != "" && match == etag {
		c.Header("ETag", etag)
		c.Status(http.StatusNotModified)
		return
	}

	resp := toSeriesTorrentsResponse(domain.InstanceName(name), sonarrID, seriesID, result)
	c.Header("ETag", etag)
	c.Header("Cache-Control", "no-cache")
	h.logger.DebugContext(ctx, "series_torrents_served",
		slog.String("instance_name", name),
		slog.Int("sonarr_series_id", int(sonarrID)),
		slog.Int64("series_id", int64(seriesID)),
		slog.Int("torrent_count", resp.TotalCount),
		slog.Int("live_count", resp.LiveCount))
	c.JSON(http.StatusOK, resp)
}

// computeTorrentsETag is the per-second ETag the handler returns
// in both the 200 + 304 paths. Quoted hex per RFC 7232 §2.3.
func computeTorrentsETag(syncedAtUnix int64, count int) string {
	payload := fmt.Sprintf("%d:%d", syncedAtUnix, count)
	sum := sha256.Sum256([]byte(payload))
	return `"` + hex.EncodeToString(sum[:]) + `"`
}

// toSeriesTorrentsResponse projects the merged query result onto
// the DTO. No DB / network calls here — pure mapping.
func toSeriesTorrentsResponse(
	instance domain.InstanceName,
	sonarrID domain.SonarrSeriesID,
	seriesID domain.SeriesID,
	result torrentsync.QueryResult,
) dto.SeriesTorrentsResponse {
	resp := dto.SeriesTorrentsResponse{
		Instance:       instance,
		SonarrSeriesID: sonarrID,
		SeriesID:       seriesID,
		Torrents:       make([]dto.TorrentRow, 0, len(result.Rows)),
		LiveCount:      result.LiveCount,
		TotalCount:     len(result.Rows),
		SyncedAt:       result.SyncedAt,
		SyncAgeSeconds: 0,
	}
	for _, r := range result.Rows {
		resp.Torrents = append(resp.Torrents, mapTorrentRow(r))
	}
	return resp
}

// mapTorrentRow projects one QueryRow → DTO row. Live cells are
// already zero for DB-only rows (the query layer rewrites them
// before handing the value up) — we copy them verbatim.
func mapTorrentRow(r torrentsync.QueryRow) dto.TorrentRow {
	info := r.Entry.Info
	row := dto.TorrentRow{
		Hash:         domain.QbitHash(info.Hash),
		Name:         info.Name,
		StateRaw:     info.StateRaw,
		StateGroup:   string(r.Entry.StateGroup),
		SizeBytes:    info.Size,
		TotalSize:    info.TotalSize,
		Downloaded:   info.Downloaded,
		Uploaded:     info.Uploaded,
		DLSpeed:      info.DlSpeed,
		UPSpeed:      info.UpSpeed,
		ETA:          info.ETA,
		NumSeeds:     info.NumSeeds,
		NumLeechs:    info.NumLeechs,
		Progress:     info.Progress,
		Ratio:        info.Ratio,
		Popularity:   info.Popularity,
		TimeActiveS:  int64(info.TimeActive.Seconds()),
		SeedingTimeS: int64(info.SeedingTime.Seconds()),
		Live:         r.Live,
		Present:      r.Present,
		SyncedAt:     r.Entry.SyncedAt,
	}
	if info.Category != "" {
		v := info.Category
		row.Category = &v
	}
	if info.Tags != "" {
		v := info.Tags
		row.Tags = &v
	}
	if info.TrackerHost != "" {
		v := info.TrackerHost
		row.TrackerHost = &v
	}
	if info.SeasonNumber != nil {
		v := *info.SeasonNumber
		row.SeasonNumber = &v
	}
	if info.SavePath != "" {
		v := info.SavePath
		row.SavePath = &v
	}
	if info.ContentPath != "" {
		v := info.ContentPath
		row.ContentPath = &v
	}
	if !info.AddedOn.IsZero() {
		t := info.AddedOn
		row.AddedOn = &t
	}
	if !info.CompletionOn.IsZero() {
		t := info.CompletionOn
		row.CompletionOn = &t
	}
	if !info.LastActivity.IsZero() {
		t := info.LastActivity
		row.LastActivity = &t
	}
	return row
}
