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

const followBodyLimit = 1 << 10

// FollowService is the narrow use-case surface the handler needs (Ф8-U-5
// per-user). *followapp.FollowUseCase satisfies it; tests inject a fake.
type FollowService interface {
	Follow(ctx context.Context, userID int64, seriesID domain.SeriesID) error
	Unfollow(ctx context.Context, userID int64, seriesID domain.SeriesID) error
	ListFollowed(ctx context.Context, userID int64, lang string) ([]followapp.FollowedItem, error)
}

type followRequest struct {
	SeriesID int64 `json:"series_id"`
}

type followedItemResponse struct {
	SeriesID    int64   `json:"series_id" example:"140"`
	TMDBID      *int64  `json:"tmdb_id,omitempty" example:"1399"`
	Title       string  `json:"title" example:"Game of Thrones"`
	PosterAsset *string `json:"poster_asset,omitempty" example:"/abc.jpg"`
	Year        *int    `json:"year,omitempty" example:"2011"`
	FollowedAt  string  `json:"followed_at" example:"2026-08-09T12:00:00Z"`
}

type followListResponse struct {
	Items []followedItemResponse `json:"items"`
}

// FollowHandler exposes POST/DELETE/GET /api/v1/follow.
type FollowHandler struct {
	svc    FollowService
	users  ports.UserRepository
	logger *slog.Logger
}

// NewFollowHandler constructs the handler. users resolves the authenticated
// caller (Ф8-U-5 per-user). logger nil-OK.
func NewFollowHandler(svc FollowService, users ports.UserRepository, logger *slog.Logger) *FollowHandler {
	if logger == nil {
		logger = sharedports.DomainLogger(slog.Default(), "http")
	}
	return &FollowHandler{svc: svc, users: users, logger: logger}
}

// callerID resolves the authenticated user id from context; 401 on miss. The
// api-key automation principal has no user row — it resolves to the seed-admin
// id (the SAME row mig-058 backfills to), mirroring request_handler.
func (h *FollowHandler) callerID(c *gin.Context) (int64, bool) {
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

// Post handles POST /api/v1/follow.
//
// @Summary     Follow a series (watchlist)
// @Description Adds the canonical series to the global follow/watchlist and
// @Description enrolls it into full enrichment. Idempotent — following an
// @Description already-followed series returns 200. The series must already
// @Description exist as canon (resolve a TMDB-only card via GET /series/resolve first).
// @Tags        follow
// @Accept      json
// @Produce     json
// @Param       body  body  followRequest  true  "{\"series_id\": 140}"
// @Success     200 {object} dto.OKResponse
// @Failure     400 {object} dto.ErrorResponse
// @Failure     401 {object} dto.ErrorResponse
// @Failure     404 {object} dto.ErrorResponse
// @Security    CookieAuth
// @Security    ApiKeyAuth
// @Router      /follow [post]
func (h *FollowHandler) Post(c *gin.Context) {
	if h.svc == nil {
		c.JSON(http.StatusInternalServerError, dto.ErrorResponse{Error: "follow handler not wired"})
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, followBodyLimit)
	var body followRequest
	if derr := json.NewDecoder(c.Request.Body).Decode(&body); derr != nil && !errors.Is(derr, io.EOF) {
		c.JSON(http.StatusBadRequest, dto.ErrorResponse{Error: "malformed body"})
		return
	}
	if body.SeriesID <= 0 {
		c.JSON(http.StatusBadRequest, dto.ErrorResponse{Error: "series_id must be a positive integer"})
		return
	}
	uid, ok := h.callerID(c)
	if !ok {
		return
	}
	err := h.svc.Follow(c.Request.Context(), uid, domain.SeriesID(body.SeriesID))
	switch {
	case err == nil:
		c.JSON(http.StatusOK, dto.OKResponse{OK: true})
	case errors.Is(err, followapp.ErrInvalidUser):
		c.JSON(http.StatusUnauthorized, dto.ErrorResponse{Error: "unauthorized"})
	case errors.Is(err, followapp.ErrInvalidSeriesID):
		c.JSON(http.StatusBadRequest, dto.ErrorResponse{Error: err.Error()})
	case errors.Is(err, followapp.ErrSeriesNotFound):
		c.JSON(http.StatusNotFound, dto.ErrorResponse{Error: "series not found"})
	default:
		_ = c.Error(err)
		c.Abort()
	}
}

// Delete handles DELETE /api/v1/follow/:series_id.
//
// @Summary     Unfollow a series
// @Description Removes the series from the follow/watchlist. Idempotent —
// @Description unfollowing a non-followed series returns 200.
// @Tags        follow
// @Produce     json
// @Param       series_id  path  int  true  "Canonical series.id"
// @Success     200 {object} dto.OKResponse
// @Failure     400 {object} dto.ErrorResponse
// @Failure     401 {object} dto.ErrorResponse
// @Security    CookieAuth
// @Security    ApiKeyAuth
// @Router      /follow/{series_id} [delete]
func (h *FollowHandler) Delete(c *gin.Context) {
	if h.svc == nil {
		c.JSON(http.StatusInternalServerError, dto.ErrorResponse{Error: "follow handler not wired"})
		return
	}
	id, err := strconv.Atoi(c.Param("series_id"))
	if err != nil || id <= 0 {
		c.JSON(http.StatusBadRequest, dto.ErrorResponse{Error: "invalid series id"})
		return
	}
	uid, ok := h.callerID(c)
	if !ok {
		return
	}
	if uerr := h.svc.Unfollow(c.Request.Context(), uid, domain.SeriesID(id)); uerr != nil {
		_ = c.Error(uerr)
		c.Abort()
		return
	}
	c.JSON(http.StatusOK, dto.OKResponse{OK: true})
}

// List handles GET /api/v1/follow.
//
// @Summary     List followed series (watchlist)
// @Description Returns the follow/watchlist as minimal cards, newest first.
// @Description The FE derives per-series follow-state from the returned series_ids.
// @Tags        follow
// @Produce     json
// @Param       lang  query  string  false  "Preferred language tag (e.g. ru-RU); falls back to en-US then canon"
// @Success     200 {object} followListResponse
// @Failure     401 {object} dto.ErrorResponse
// @Security    CookieAuth
// @Security    ApiKeyAuth
// @Router      /follow [get]
func (h *FollowHandler) List(c *gin.Context) {
	if h.svc == nil {
		c.JSON(http.StatusInternalServerError, dto.ErrorResponse{Error: "follow handler not wired"})
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
	resp := followListResponse{Items: make([]followedItemResponse, 0, len(items))}
	for _, it := range items {
		var tmdb *int64
		if it.TMDBID != nil {
			v := int64(*it.TMDBID)
			tmdb = &v
		}
		resp.Items = append(resp.Items, followedItemResponse{
			SeriesID:    int64(it.SeriesID),
			TMDBID:      tmdb,
			Title:       it.Title,
			PosterAsset: it.PosterAsset,
			Year:        it.Year,
			FollowedAt:  it.FollowedAt.UTC().Format("2006-01-02T15:04:05Z07:00"),
		})
	}
	c.JSON(http.StatusOK, resp)
}
