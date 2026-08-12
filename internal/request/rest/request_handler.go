// Package rest ships the Ф8-U-2 request-workflow endpoints:
// GET /api/v1/requests, POST /api/v1/requests/:id/approve, .../deny.
package rest

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	admin "github.com/alexmorbo/seasonfill/internal/admin/domain"
	reqapp "github.com/alexmorbo/seasonfill/internal/request/app"
	reqdomain "github.com/alexmorbo/seasonfill/internal/request/domain"
	"github.com/alexmorbo/seasonfill/internal/shared/http/dto"
	"github.com/alexmorbo/seasonfill/internal/shared/http/middleware"
	sharedports "github.com/alexmorbo/seasonfill/internal/shared/ports"
)

// RequestService is the narrow use-case surface. *request/app.UseCase satisfies it.
type RequestService interface {
	List(ctx context.Context, caller admin.User) ([]reqdomain.Request, error)
	Approve(ctx context.Context, id int64, approver admin.User) (reqdomain.Request, error)
	Deny(ctx context.Context, id int64, approver admin.User) (reqdomain.Request, error)
}

// UserDirectory resolves users for the request routes: GetByUsername keeps the
// caller lookup (mirrors RequirePermission), UsernamesByIDs batch-resolves the
// display username per queue row (Ф8-U-6a) in a single query — no N+1.
// *admin/persistence.UserRepository satisfies it. Kept narrow (not
// ports.UserRepository) so the batch method lives only on the concrete
// repository and never forces a stub onto the shared port's many test fakes.
type UserDirectory interface {
	GetByUsername(ctx context.Context, username string) (admin.User, error)
	UsernamesByIDs(ctx context.Context, ids []uint) (map[uint]string, error)
}

// TitleReader resolves human-readable catalog titles for queue rows from the
// LOCAL catalog only — no external TMDB call (Ф8-U-6a). Movie titles key on
// TMDB id, series titles key on TVDB id (Request.TMDBID carries a TVDB id for
// tv rows). *request/persistence.TitleReader satisfies it.
type TitleReader interface {
	MovieTitlesByTMDB(ctx context.Context, tmdbIDs []int64) (map[int64]string, error)
	SeriesTitlesByTVDB(ctx context.Context, tvdbIDs []int64) (map[int64]string, error)
}

type requestItem struct {
	ID         int64   `json:"id" example:"7"`
	UserID     int64   `json:"user_id" example:"2"`
	Username   string  `json:"username" example:"alice"`
	MediaType  string  `json:"media_type" example:"tv"`
	TMDBID     int64   `json:"tmdb_id" example:"1399"`
	Title      *string `json:"title,omitempty" example:"Breaking Bad"`
	Seasons    *[]int  `json:"seasons,omitempty"`
	Status     string  `json:"status" example:"pending"`
	ApproverID *int64  `json:"approver_id,omitempty" example:"1"`
	CreatedAt  string  `json:"created_at" example:"2026-08-12T12:00:00Z"`
}

type requestListResponse struct {
	Items []requestItem `json:"items"`
}

// RequestHandler exposes the request-workflow routes. users resolves the caller
// from the auth-context username (mirrors RequirePermission's lookup) and
// batch-resolves per-row usernames; titles resolves per-row catalog titles.
type RequestHandler struct {
	svc    RequestService
	users  UserDirectory
	titles TitleReader
	logger *slog.Logger
}

// NewRequestHandler panics on nil svc/users. titles + logger are nil-OK
// (nil titles → rows carry no resolved title, never blocks the list).
func NewRequestHandler(svc RequestService, users UserDirectory, titles TitleReader, logger *slog.Logger) *RequestHandler {
	if svc == nil {
		panic("NewRequestHandler: svc required")
	}
	if users == nil {
		panic("NewRequestHandler: users required")
	}
	if logger == nil {
		logger = sharedports.DomainLogger(slog.Default(), "http")
	}
	return &RequestHandler{svc: svc, users: users, titles: titles, logger: logger}
}

// caller resolves the authenticated user from context; 401 on miss.
func (h *RequestHandler) caller(c *gin.Context) (admin.User, bool) {
	username := c.GetString(middleware.UsernameContextKey)
	if username == "" || username == "api-key" {
		// api-key automation is admin-equivalent (see RequirePermission);
		// synthesize an admin so it sees all rows.
		if username == "api-key" {
			return admin.User{Role: admin.RoleAdmin, ManageRequests: true}, true
		}
		c.AbortWithStatusJSON(http.StatusUnauthorized, dto.ErrorResponse{Error: "unauthorized"})
		return admin.User{}, false
	}
	u, err := h.users.GetByUsername(c.Request.Context(), username)
	if err != nil {
		c.AbortWithStatusJSON(http.StatusUnauthorized, dto.ErrorResponse{Error: "unauthorized"})
		return admin.User{}, false
	}
	return u, true
}

// List handles GET /api/v1/requests.
//
// @Summary     List requests
// @Description Returns request-workflow rows. A manager (manage_requests) or
// @Description admin sees every request; a plain user sees only their own.
// @Tags        requests
// @Produce     json
// @Success     200 {object} requestListResponse
// @Failure     401 {object} dto.ErrorResponse
// @Security    CookieAuth
// @Security    ApiKeyAuth
// @Router      /requests [get]
func (h *RequestHandler) List(c *gin.Context) {
	caller, ok := h.caller(c)
	if !ok {
		return
	}
	items, err := h.svc.List(c.Request.Context(), caller)
	if err != nil {
		_ = c.Error(err)
		c.Abort()
		return
	}
	usernames, movieTitles, seriesTitles := h.enrich(c.Request.Context(), items)
	resp := requestListResponse{Items: make([]requestItem, 0, len(items))}
	for _, it := range items {
		resp.Items = append(resp.Items, toItemEnriched(it, usernames, movieTitles, seriesTitles))
	}
	c.JSON(http.StatusOK, resp)
}

// Approve handles POST /api/v1/requests/:id/approve.
//
// @Summary     Approve a request
// @Description Replays the stored add via the discovery add use case, sets
// @Description status=approved, and emits request.approved. Idempotent.
// @Tags        requests
// @Produce     json
// @Param       id path int true "Request id"
// @Success     200 {object} requestItem
// @Failure     400 {object} dto.ErrorResponse
// @Failure     401 {object} dto.ErrorResponse
// @Failure     404 {object} dto.ErrorResponse
// @Security    CookieAuth
// @Security    ApiKeyAuth
// @Router      /requests/{id}/approve [post]
func (h *RequestHandler) Approve(c *gin.Context) { h.transition(c, true) }

// Deny handles POST /api/v1/requests/:id/deny.
//
// @Summary     Deny a request
// @Description Sets status=denied and emits request.denied. Idempotent.
// @Tags        requests
// @Produce     json
// @Param       id path int true "Request id"
// @Success     200 {object} requestItem
// @Failure     400 {object} dto.ErrorResponse
// @Failure     401 {object} dto.ErrorResponse
// @Failure     404 {object} dto.ErrorResponse
// @Security    CookieAuth
// @Security    ApiKeyAuth
// @Router      /requests/{id}/deny [post]
func (h *RequestHandler) Deny(c *gin.Context) { h.transition(c, false) }

func (h *RequestHandler) transition(c *gin.Context, approve bool) {
	caller, ok := h.caller(c)
	if !ok {
		return
	}
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		c.JSON(http.StatusBadRequest, dto.ErrorResponse{Error: "invalid request id"})
		return
	}
	var r reqdomain.Request
	if approve {
		r, err = h.svc.Approve(c.Request.Context(), id, caller)
	} else {
		r, err = h.svc.Deny(c.Request.Context(), id, caller)
	}
	switch {
	case err == nil:
		usernames, movieTitles, seriesTitles := h.enrich(c.Request.Context(), []reqdomain.Request{r})
		c.JSON(http.StatusOK, toItemEnriched(r, usernames, movieTitles, seriesTitles))
	case errors.Is(err, reqapp.ErrRequestNotFound):
		c.JSON(http.StatusNotFound, dto.ErrorResponse{Error: "request not found"})
	default:
		_ = c.Error(err)
		c.Abort()
	}
}

func toItem(it reqdomain.Request) requestItem {
	var approver *int64
	if it.ApproverID != nil {
		v := int64(*it.ApproverID)
		approver = &v
	}
	return requestItem{
		ID:         int64(it.ID),
		UserID:     int64(it.UserID),
		MediaType:  it.MediaType,
		TMDBID:     it.TMDBID,
		Status:     it.Status,
		ApproverID: approver,
		CreatedAt:  it.CreatedAt.UTC().Format("2006-01-02T15:04:05Z07:00"),
	}
}

// toItemEnriched wraps toItem with the batch-resolved display fields: the
// requester username, the local-catalog title (movie-by-TMDB / series-by-TVDB),
// and the tv seasons. Unresolved lookups leave the base zero value (username
// "", title nil → omitted, seasons nil for movie rows).
func toItemEnriched(it reqdomain.Request, usernames map[uint]string, movieTitles, seriesTitles map[int64]string) requestItem {
	item := toItem(it)
	item.Username = usernames[it.UserID]
	switch it.MediaType {
	case reqdomain.MediaTypeMovie:
		if t, ok := movieTitles[it.TMDBID]; ok {
			item.Title = &t
		}
	case reqdomain.MediaTypeTV:
		if t, ok := seriesTitles[it.TMDBID]; ok {
			item.Title = &t
		}
		item.Seasons = it.Seasons
	}
	return item
}

// enrich batch-resolves the display data for a page of requests with as few
// queries as practical: one username lookup, one movie-title lookup, one
// series-title lookup — each over the distinct ids of the page. Resolution is
// best-effort: a lookup error is logged and its map left empty so the list
// still renders (ids fall back to labels on the FE).
func (h *RequestHandler) enrich(ctx context.Context, items []reqdomain.Request) (map[uint]string, map[int64]string, map[int64]string) {
	userIDs, movieIDs, tvIDs := distinctRequestIDs(items)

	uNames := map[uint]string{}
	if len(userIDs) > 0 {
		if m, err := h.users.UsernamesByIDs(ctx, userIDs); err != nil {
			h.logger.WarnContext(ctx, "request_enrich_usernames_failed", slog.String("error", err.Error()))
		} else {
			uNames = m
		}
	}

	mTitles := map[int64]string{}
	sTitles := map[int64]string{}
	if h.titles != nil {
		if len(movieIDs) > 0 {
			if m, err := h.titles.MovieTitlesByTMDB(ctx, movieIDs); err != nil {
				h.logger.WarnContext(ctx, "request_enrich_movie_titles_failed", slog.String("error", err.Error()))
			} else {
				mTitles = m
			}
		}
		if len(tvIDs) > 0 {
			if m, err := h.titles.SeriesTitlesByTVDB(ctx, tvIDs); err != nil {
				h.logger.WarnContext(ctx, "request_enrich_series_titles_failed", slog.String("error", err.Error()))
			} else {
				sTitles = m
			}
		}
	}
	return uNames, mTitles, sTitles
}

// distinctRequestIDs returns the deduplicated user ids, movie TMDB ids, and tv
// TVDB ids for a page of requests (the batch-lookup key sets).
func distinctRequestIDs(items []reqdomain.Request) (userIDs []uint, movieIDs, tvIDs []int64) {
	seenUsers := make(map[uint]struct{}, len(items))
	seenMovies := make(map[int64]struct{}, len(items))
	seenTV := make(map[int64]struct{}, len(items))
	for _, it := range items {
		if _, ok := seenUsers[it.UserID]; !ok {
			seenUsers[it.UserID] = struct{}{}
			userIDs = append(userIDs, it.UserID)
		}
		switch it.MediaType {
		case reqdomain.MediaTypeMovie:
			if _, ok := seenMovies[it.TMDBID]; !ok {
				seenMovies[it.TMDBID] = struct{}{}
				movieIDs = append(movieIDs, it.TMDBID)
			}
		case reqdomain.MediaTypeTV:
			if _, ok := seenTV[it.TMDBID]; !ok {
				seenTV[it.TMDBID] = struct{}{}
				tvIDs = append(tvIDs, it.TMDBID)
			}
		}
	}
	return userIDs, movieIDs, tvIDs
}
