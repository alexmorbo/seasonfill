package rest

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"github.com/alexmorbo/seasonfill/internal/catalog/app/torrentsync"
	"github.com/alexmorbo/seasonfill/internal/catalog/domain/movie"
	"github.com/alexmorbo/seasonfill/internal/shared/clients/qbit"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
	"github.com/alexmorbo/seasonfill/internal/shared/http/dto"
)

// MovieStateReader resolves one movie_states row by its INSTANCE-LOCAL key.
// Impl: *catalogpersistence.MovieStatesRepository.Get. Used by the inner
// handler to recover the canonical movies.id for the response envelope —
// the exact mirror of SeriesTorrentsHandler's series_cache lookup.
type MovieStateReader interface {
	Get(ctx context.Context, instanceName domain.InstanceName, radarrMovieID int) (movie.StateEntry, error)
}

// MovieTorrentsHandler serves the per-instance movie torrents document
// (ADR-0023 B1.4) — the movie twin of seriesdetail's SeriesTorrentsHandler.
//
// It is NOT routed directly: GlobalMovieTorrentsHandler resolves the
// canonical movie to a (radarr instance, radarr_movie_id) pair, splices them
// into c.Params as :name / :id and invokes Get. Same wrapper/inner split as
// the series pair, so the merge + ETag + projection logic lives in exactly
// one place per vertical.
type MovieTorrentsHandler struct {
	query  *torrentsync.Query
	states MovieStateReader
	logger *slog.Logger
}

// NewMovieTorrentsHandler wires the inner handler. logger nil-OK.
func NewMovieTorrentsHandler(
	query *torrentsync.Query,
	states MovieStateReader,
	logger *slog.Logger,
) *MovieTorrentsHandler {
	if logger == nil {
		logger = slog.Default()
	}
	return &MovieTorrentsHandler{query: query, states: states, logger: logger}
}

// Get serves the merged inventory for the spliced (:name, :id) pair, where
// :id is the INSTANCE-LOCAL radarr_movie_id. Not registered as a route.
func (h *MovieTorrentsHandler) Get(c *gin.Context) {
	name := c.Param("name")
	parsedID, err := strconv.Atoi(c.Param("id"))
	if err != nil || parsedID <= 0 {
		c.JSON(http.StatusBadRequest, dto.ErrorResponse{Error: "invalid radarr movie id"})
		return
	}
	instance := domain.InstanceName(name)

	ctx := c.Request.Context()

	// Step 1 — resolve movie_states → canon movies.id. Unknown
	// (instance, radarrMovieID) bubbles MovieNotFoundError + ErrNotFound
	// through the typed-error middleware → 404. A row without a canon
	// movie_id (pre-Ф6-R-3 legacy) is also a 404.
	state, err := h.states.Get(ctx, instance, parsedID)
	if err != nil {
		_ = c.Error(err)
		return
	}
	if state.MovieID == 0 {
		c.JSON(http.StatusNotFound, dto.ErrorResponse{Error: "movie not found"})
		return
	}

	// Step 2 — merge live store + torrent_movie_map/qbit_torrents fallback.
	result, err := h.query.ByMovieID(ctx, instance, domain.RadarrMovieID(parsedID))
	if err != nil {
		writeMovieInternalError(c, h.logger, "movie_torrents_query_failed", err,
			slog.String("instance_name", name),
			slog.Int("radarr_movie_id", parsedID),
			slog.Int64("movie_id", int64(state.MovieID)))
		return
	}

	// Step 3 — per-second ETag over synced_at + row count, identical
	// granularity to the series endpoint.
	etag := computeMovieTorrentsETag(result.SyncedAt.Unix(), len(result.Rows))
	if match := c.GetHeader("If-None-Match"); match != "" && match == etag {
		c.Header("ETag", etag)
		c.Status(http.StatusNotModified)
		return
	}

	tmdbID, _ := strconv.Atoi(c.Param("tmdb_id"))
	resp := toMovieTorrentsResponse(instance, parsedID, state.MovieID, tmdbID, result)
	c.Header("ETag", etag)
	c.Header("Cache-Control", "no-cache")
	h.logger.DebugContext(ctx, "movie_torrents_served",
		slog.String("instance_name", name),
		slog.Int("radarr_movie_id", parsedID),
		slog.Int64("movie_id", int64(state.MovieID)),
		slog.Int("torrent_count", resp.TotalCount),
		slog.Int("live_count", resp.LiveCount))
	c.JSON(http.StatusOK, resp)
}

// computeMovieTorrentsETag mirrors seriesdetail's computeTorrentsETag —
// quoted hex per RFC 7232 §2.3, per-second granularity with the row count
// folded in so add/remove inside one wall second still rotates the tag.
func computeMovieTorrentsETag(syncedAtUnix int64, count int) string {
	payload := fmt.Sprintf("%d:%d", syncedAtUnix, count)
	sum := sha256.Sum256([]byte(payload))
	return `"` + hex.EncodeToString(sum[:]) + `"`
}

// writeMovieInternalError logs at ERROR with the caller's attrs, then writes
// a stable generic 5xx body so DB internals never reach the client. Local
// twin of seriesdetail/rest.writeInternalError (unexported there).
func writeMovieInternalError(c *gin.Context, log *slog.Logger, event string, err error, attrs ...slog.Attr) {
	if log == nil {
		log = slog.Default()
	}
	full := make([]slog.Attr, 0, len(attrs)+1)
	full = append(full, slog.String("error", err.Error()))
	full = append(full, attrs...)
	log.LogAttrs(c.Request.Context(), slog.LevelError, event, full...)
	c.AbortWithStatusJSON(http.StatusInternalServerError, dto.ErrorResponse{Error: "internal server error"})
}

// toMovieTorrentsResponse projects the merged query result onto the DTO.
// Pure mapping — no DB / network calls.
func toMovieTorrentsResponse(
	instance domain.InstanceName,
	radarrMovieID int,
	movieID domain.MovieID,
	tmdbID int,
	result torrentsync.QueryResult,
) dto.MovieTorrentsResponse {
	resp := dto.MovieTorrentsResponse{
		Instance:       instance,
		RadarrMovieID:  radarrMovieID,
		MovieID:        movieID,
		TMDBID:         tmdbID,
		Torrents:       make([]dto.TorrentRow, 0, len(result.Rows)),
		LiveCount:      result.LiveCount,
		TotalCount:     len(result.Rows),
		SyncedAt:       result.SyncedAt,
		SyncAgeSeconds: 0,
	}
	for _, r := range result.Rows {
		resp.Torrents = append(resp.Torrents, mapMovieTorrentRow(r))
	}
	return resp
}

// mapMovieTorrentRow projects one QueryRow → DTO row. Movie twin of
// seriesdetail's mapTorrentRow with two deliberate differences:
//   - season_number is NEVER emitted (movies have no seasons; a stray SxxExx
//     parse on a movie release filename must not surface an "S05" chip)
//   - provenance IS emitted when the bridge row carried one
func mapMovieTorrentRow(r torrentsync.QueryRow) dto.TorrentRow {
	info := r.Entry.Info
	row := dto.TorrentRow{
		Hash:         domain.QbitHash(info.Hash),
		Name:         info.Name,
		StateRaw:     info.StateRaw,
		StateGroup:   string(r.Entry.StateGroup),
		Health:       string(qbit.HealthFor(r.Entry.StateGroup)),
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
	if info.SavePath != "" {
		v := info.SavePath
		row.SavePath = &v
	}
	if info.ContentPath != "" {
		v := info.ContentPath
		row.ContentPath = &v
	}
	if r.Provenance != "" {
		v := r.Provenance
		row.Provenance = &v
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
