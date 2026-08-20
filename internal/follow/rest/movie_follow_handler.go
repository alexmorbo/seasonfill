package rest

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	followapp "github.com/alexmorbo/seasonfill/internal/follow/app"
	ports "github.com/alexmorbo/seasonfill/internal/shared/dataports"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
	"github.com/alexmorbo/seasonfill/internal/shared/http/dto"
	"github.com/alexmorbo/seasonfill/internal/shared/http/middleware"
	sharedports "github.com/alexmorbo/seasonfill/internal/shared/ports"
)

// MovieFollowService is the narrow use-case surface the movie handler needs.
// *followapp.MovieFollowUseCase satisfies it; tests inject a fake.
type MovieFollowService interface {
	Follow(ctx context.Context, userID int64, tmdbID domain.TMDBID) error
	Unfollow(ctx context.Context, userID int64, tmdbID domain.TMDBID) error
	ListFollowed(ctx context.Context, userID int64, lang string) ([]followapp.FollowedMovieItem, error)
}

type movieFollowRequest struct {
	TMDBID int64 `json:"tmdb_id"`
}

type followedMovieItemResponse struct {
	MovieID     int64   `json:"movie_id" example:"42"`
	TMDBID      *int64  `json:"tmdb_id,omitempty" example:"550"`
	Title       string  `json:"title" example:"Fight Club"`
	PosterAsset *string `json:"poster_asset,omitempty" example:"/abc.jpg"`
	Year        *int    `json:"year,omitempty" example:"1999"`
	FollowedAt  string  `json:"followed_at" example:"2026-08-20T12:00:00Z"`
}

type followedMovieListResponse struct {
	Items []followedMovieItemResponse `json:"items"`
}

// MovieFollowHandler exposes POST/DELETE/GET /api/v1/follow/movies — the movie
// mirror of FollowHandler. Keyed by TMDB id because the whole movie API
// surface (GET /movies/:tmdb_id and its sub-endpoints) is TMDB-keyed; the
// canon movies.id never crosses the wire.
type MovieFollowHandler struct {
	svc    MovieFollowService
	users  ports.UserRepository
	logger *slog.Logger
}

// NewMovieFollowHandler constructs the handler. users resolves the
// authenticated caller (owner scoping). logger nil-OK.
func NewMovieFollowHandler(svc MovieFollowService, users ports.UserRepository, logger *slog.Logger) *MovieFollowHandler {
	if logger == nil {
		logger = sharedports.DomainLogger(slog.Default(), "http")
	}
	return &MovieFollowHandler{svc: svc, users: users, logger: logger}
}

// callerID resolves the authenticated user id from context; 401 on miss. The
// api-key automation principal has no user row — it resolves to the seed-admin
// id, mirroring FollowHandler.callerID.
func (h *MovieFollowHandler) callerID(c *gin.Context) (int64, bool) {
	username := c.GetString(middleware.UsernameContextKey)
	if username == "" {
		c.AbortWithStatusJSON(http.StatusUnauthorized, dto.ErrorResponse{Error: "unauthorized"})
		return 0, false
	}
	if username == "api-key" {
		id, err := h.users.FirstAdminID(c.Request.Context())
		if err != nil {
			c.AbortWithStatusJSON(http.StatusUnauthorized, dto.ErrorResponse{Error: "unauthorized"})
			return 0, false
		}
		return id, true
	}
	u, err := h.users.GetByUsername(c.Request.Context(), username)
	if err != nil {
		c.AbortWithStatusJSON(http.StatusUnauthorized, dto.ErrorResponse{Error: "unauthorized"})
		return 0, false
	}
	return int64(u.ID), true
}

// Post handles POST /api/v1/follow/movies.
//
// @Summary     Follow a movie (watchlist)
// @Description Adds the movie to the caller's follow/watchlist and kicks the
// @Description Hot enrichment lane. Idempotent — following an already-followed
// @Description movie returns 200. The movie must already exist as canon
// @Description (opening GET /movies/{tmdb_id} creates the stub).
// @Tags        follow
// @Accept      json
// @Produce     json
// @Param       body  body  movieFollowRequest  true  "{\"tmdb_id\": 550}"
// @Success     200 {object} dto.OKResponse
// @Failure     400 {object} dto.ErrorResponse
// @Failure     401 {object} dto.ErrorResponse
// @Failure     404 {object} dto.ErrorResponse
// @Security    CookieAuth
// @Security    ApiKeyAuth
// @Router      /follow/movies [post]
func (h *MovieFollowHandler) Post(c *gin.Context) {
	if h.svc == nil {
		c.JSON(http.StatusInternalServerError, dto.ErrorResponse{Error: "movie follow handler not wired"})
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, followBodyLimit)
	var body movieFollowRequest
	if derr := json.NewDecoder(c.Request.Body).Decode(&body); derr != nil && !errors.Is(derr, io.EOF) {
		c.JSON(http.StatusBadRequest, dto.ErrorResponse{Error: "malformed body"})
		return
	}
	if body.TMDBID <= 0 {
		c.JSON(http.StatusBadRequest, dto.ErrorResponse{Error: "tmdb_id must be a positive integer"})
		return
	}
	uid, ok := h.callerID(c)
	if !ok {
		return
	}
	err := h.svc.Follow(c.Request.Context(), uid, domain.TMDBID(body.TMDBID))
	switch {
	case err == nil:
		c.JSON(http.StatusOK, dto.OKResponse{OK: true})
	case errors.Is(err, followapp.ErrInvalidUser):
		c.JSON(http.StatusUnauthorized, dto.ErrorResponse{Error: "unauthorized"})
	case errors.Is(err, followapp.ErrInvalidTMDBID):
		c.JSON(http.StatusBadRequest, dto.ErrorResponse{Error: err.Error()})
	case errors.Is(err, followapp.ErrMovieNotFound):
		c.JSON(http.StatusNotFound, dto.ErrorResponse{Error: "movie not found"})
	default:
		_ = c.Error(err)
		c.Abort()
	}
}

// Delete handles DELETE /api/v1/follow/movies/:tmdb_id.
//
// @Summary     Unfollow a movie
// @Description Removes the movie from the caller's follow/watchlist.
// @Description Idempotent — unfollowing a non-followed movie returns 200.
// @Tags        follow
// @Produce     json
// @Param       tmdb_id  path  int  true  "TMDB movie id"
// @Success     200 {object} dto.OKResponse
// @Failure     400 {object} dto.ErrorResponse
// @Failure     401 {object} dto.ErrorResponse
// @Security    CookieAuth
// @Security    ApiKeyAuth
// @Router      /follow/movies/{tmdb_id} [delete]
func (h *MovieFollowHandler) Delete(c *gin.Context) {
	if h.svc == nil {
		c.JSON(http.StatusInternalServerError, dto.ErrorResponse{Error: "movie follow handler not wired"})
		return
	}
	id, err := strconv.Atoi(c.Param("tmdb_id"))
	if err != nil || id <= 0 {
		c.JSON(http.StatusBadRequest, dto.ErrorResponse{Error: "invalid tmdb id"})
		return
	}
	uid, ok := h.callerID(c)
	if !ok {
		return
	}
	if uerr := h.svc.Unfollow(c.Request.Context(), uid, domain.TMDBID(id)); uerr != nil {
		_ = c.Error(uerr)
		c.Abort()
		return
	}
	c.JSON(http.StatusOK, dto.OKResponse{OK: true})
}

// List handles GET /api/v1/follow/movies.
//
// @Summary     List followed movies (watchlist)
// @Description Returns the caller's movie follow/watchlist as minimal cards,
// @Description newest first. The FE derives per-movie follow-state from the
// @Description returned tmdb_ids.
// @Tags        follow
// @Produce     json
// @Param       lang  query  string  false  "Preferred language tag (e.g. ru-RU); falls back to en-US then canon"
// @Success     200 {object} followedMovieListResponse
// @Failure     401 {object} dto.ErrorResponse
// @Security    CookieAuth
// @Security    ApiKeyAuth
// @Router      /follow/movies [get]
func (h *MovieFollowHandler) List(c *gin.Context) {
	if h.svc == nil {
		c.JSON(http.StatusInternalServerError, dto.ErrorResponse{Error: "movie follow handler not wired"})
		return
	}
	lang := c.Query("lang")
	if lang == "" {
		lang = "en-US"
	}
	uid, ok := h.callerID(c)
	if !ok {
		return
	}
	items, err := h.svc.ListFollowed(c.Request.Context(), uid, lang)
	if err != nil {
		_ = c.Error(err)
		c.Abort()
		return
	}
	resp := followedMovieListResponse{Items: make([]followedMovieItemResponse, 0, len(items))}
	for _, it := range items {
		var tmdb *int64
		if it.TMDBID != nil {
			v := int64(*it.TMDBID)
			tmdb = &v
		}
		resp.Items = append(resp.Items, followedMovieItemResponse{
			MovieID:     int64(it.MovieID),
			TMDBID:      tmdb,
			Title:       it.Title,
			PosterAsset: it.PosterAsset,
			Year:        it.Year,
			FollowedAt:  it.FollowedAt.UTC().Format("2006-01-02T15:04:05Z07:00"),
		})
	}
	c.JSON(http.StatusOK, resp)
}
