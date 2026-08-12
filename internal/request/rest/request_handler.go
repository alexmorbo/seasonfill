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
	ports "github.com/alexmorbo/seasonfill/internal/shared/dataports"
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

type requestItem struct {
	ID         int64  `json:"id" example:"7"`
	UserID     int64  `json:"user_id" example:"2"`
	MediaType  string `json:"media_type" example:"tv"`
	TMDBID     int64  `json:"tmdb_id" example:"1399"`
	Status     string `json:"status" example:"pending"`
	ApproverID *int64 `json:"approver_id,omitempty" example:"1"`
	CreatedAt  string `json:"created_at" example:"2026-08-12T12:00:00Z"`
}

type requestListResponse struct {
	Items []requestItem `json:"items"`
}

// RequestHandler exposes the request-workflow routes. repo resolves the caller
// from the auth-context username (mirrors RequirePermission's lookup).
type RequestHandler struct {
	svc    RequestService
	repo   ports.UserRepository
	logger *slog.Logger
}

// NewRequestHandler panics on nil svc/repo. logger nil-OK.
func NewRequestHandler(svc RequestService, repo ports.UserRepository, logger *slog.Logger) *RequestHandler {
	if svc == nil {
		panic("NewRequestHandler: svc required")
	}
	if repo == nil {
		panic("NewRequestHandler: repo required")
	}
	if logger == nil {
		logger = sharedports.DomainLogger(slog.Default(), "http")
	}
	return &RequestHandler{svc: svc, repo: repo, logger: logger}
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
	u, err := h.repo.GetByUsername(c.Request.Context(), username)
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
	resp := requestListResponse{Items: make([]requestItem, 0, len(items))}
	for _, it := range items {
		resp.Items = append(resp.Items, toItem(it))
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
		c.JSON(http.StatusOK, toItem(r))
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
