// Package rest — moviedetail HTTP handlers.
//
// global_movie_torrents_handler.go (ADR-0023 B1.4). GET
// /api/v1/movies/:tmdb_id/torrents resolves the TMDB id to the canonical
// movies.id, then to the preferred Radarr instance via movie_states, then
// delegates to the per-instance MovieTorrentsHandler by splicing :name + :id
// into c.Params. 404 when the movie is unknown OR in zero Radarr libraries —
// a TMDB-only movie has no torrent surface.
//
// Movie twin of seriesdetail/rest.GlobalSeriesTorrentsHandler, with one extra
// hop: the series route is keyed by the canonical series.id, but the whole
// movie surface is TMDB-keyed (/movies/:tmdb_id/...), so tmdb_id → canon must
// happen before canon → instance.
package rest

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"github.com/alexmorbo/seasonfill/internal/catalog/domain/movie"
	ports "github.com/alexmorbo/seasonfill/internal/shared/dataports"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
	"github.com/alexmorbo/seasonfill/internal/shared/http/dto"
)

// MovieCanonReader resolves a movie canon row by TMDB id.
// Impl: *enrichpersistence.MovieRepository.GetByTMDBID.
type MovieCanonReader interface {
	GetByTMDBID(ctx context.Context, tmdbID domain.TMDBID) (movie.Canon, error)
}

// MovieMembershipReader lists the ACTIVE per-instance Radarr states for a
// canonical movie id. Impl:
// *catalogpersistence.MovieStatesRepository.ListActiveByMovieID (already
// ordered instance_name ASC).
type MovieMembershipReader interface {
	ListActiveByMovieID(ctx context.Context, movieID domain.MovieID) ([]movie.StateEntry, error)
}

// GlobalMovieTorrentsHandler exposes GET /api/v1/movies/:tmdb_id/torrents.
type GlobalMovieTorrentsHandler struct {
	inner      *MovieTorrentsHandler
	canon      MovieCanonReader
	membership MovieMembershipReader
	logger     *slog.Logger
}

// NewGlobalMovieTorrentsHandler constructs the wrapper. inner is the
// per-instance handler (never routed on its own). logger nil-OK.
func NewGlobalMovieTorrentsHandler(
	inner *MovieTorrentsHandler,
	canon MovieCanonReader,
	membership MovieMembershipReader,
	logger *slog.Logger,
) *GlobalMovieTorrentsHandler {
	if logger == nil {
		logger = slog.Default()
	}
	return &GlobalMovieTorrentsHandler{
		inner:      inner,
		canon:      canon,
		membership: membership,
		logger:     logger,
	}
}

// Get handles GET /api/v1/movies/:tmdb_id/torrents.
//
// @Summary     Per-movie torrent inventory
// @Description Per-movie torrent inventory keyed by TMDB movie id — the movie
// @Description twin of /series/{id}/torrents. Resolves the canonical movie and
// @Description then the preferred Radarr instance automatically (lex-first
// @Description instance whose movie_states row is active), and merges the live
// @Description qBit snapshot with the durable qbit_torrents fallback over the
// @Description hashes bridged in torrent_movie_map. Each row additionally
// @Description carries `provenance` (radarr_search | manual_import); rows never
// @Description carry `season_number`. 404 when the movie is unknown or in no
// @Description Radarr library — TMDB-only movies have no torrent surface.
// @Tags        movies
// @Produce     json
// @Param       tmdb_id path int true "TMDB movie id"
// @Success     200 {object} dto.MovieTorrentsResponse
// @Success     304 "not modified — If-None-Match matched the current ETag"
// @Failure     400 {object} dto.ErrorResponse
// @Failure     401 {object} dto.ErrorResponse
// @Failure     404 {object} dto.ErrorResponse
// @Failure     500 {object} dto.ErrorResponse
// @Security    CookieAuth
// @Security    ApiKeyAuth
// @Router      /movies/{tmdb_id}/torrents [get]
func (h *GlobalMovieTorrentsHandler) Get(c *gin.Context) {
	parsedID, err := strconv.Atoi(c.Param("tmdb_id"))
	if err != nil || parsedID <= 0 {
		c.JSON(http.StatusBadRequest, dto.ErrorResponse{Error: "invalid tmdb id", Code: "BAD_REQUEST"})
		return
	}

	ctx := c.Request.Context()

	// Hop 1 — TMDB id → canonical movies.id.
	canon, err := h.canon.GetByTMDBID(ctx, domain.TMDBID(parsedID))
	if err != nil {
		if errors.Is(err, ports.ErrNotFound) {
			c.JSON(http.StatusNotFound, dto.ErrorResponse{Error: "movie_not_found", Code: "MOVIE_NOT_FOUND"})
			return
		}
		_ = c.Error(err)
		return
	}
	if canon.ID == 0 {
		c.JSON(http.StatusNotFound, dto.ErrorResponse{Error: "movie_not_found", Code: "MOVIE_NOT_FOUND"})
		return
	}

	// Hop 2 — canonical movie → preferred (lex-first) Radarr instance +
	// its INSTANCE-LOCAL radarr_movie_id, which is what torrent_movie_map
	// is keyed by.
	preferred, ok, err := resolvePreferredMovieState(ctx, h.membership, canon.ID)
	if err != nil {
		_ = c.Error(err)
		return
	}
	if !ok {
		c.JSON(http.StatusNotFound, dto.ErrorResponse{Error: "movie not in any library"})
		return
	}
	if h.inner == nil {
		c.JSON(http.StatusInternalServerError, dto.ErrorResponse{Error: "torrents handler not wired"})
		return
	}

	c.Params = setMovieParam(c.Params, "name", string(preferred.InstanceName))
	c.Params = setMovieParam(c.Params, "id", strconv.Itoa(preferred.RadarrMovieID))
	h.inner.Get(c)
}

// resolvePreferredMovieState picks the lex-first ACTIVE movie_states row for
// the canonical movie. ListActiveByMovieID already ORDERs instance_name ASC,
// but the min-scan is kept explicit so a repo ordering change cannot silently
// move which instance the endpoint reports — same defence as seriesdetail's
// resolvePreferredCacheEntry.
func resolvePreferredMovieState(
	ctx context.Context,
	repo MovieMembershipReader,
	movieID domain.MovieID,
) (movie.StateEntry, bool, error) {
	entries, err := repo.ListActiveByMovieID(ctx, movieID)
	if err != nil {
		return movie.StateEntry{}, false, err
	}
	if len(entries) == 0 {
		return movie.StateEntry{}, false, nil
	}
	preferred := entries[0]
	for _, e := range entries[1:] {
		if e.InstanceName < preferred.InstanceName {
			preferred = e
		}
	}
	return preferred, true, nil
}

// setMovieParam replaces an existing c.Params entry by key, or appends when
// absent. gin's Params.Get returns the FIRST match, so a plain append on a
// key already present in the URL path would be a no-op. Local twin of
// seriesdetail/rest.setParam (unexported there).
func setMovieParam(params gin.Params, key, value string) gin.Params {
	for i := range params {
		if params[i].Key == key {
			params[i].Value = value
			return params
		}
	}
	return append(params, gin.Param{Key: key, Value: value})
}
