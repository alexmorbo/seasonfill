package rest

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	authapp "github.com/alexmorbo/seasonfill/internal/admin/app"
	admin "github.com/alexmorbo/seasonfill/internal/admin/domain"
	"github.com/alexmorbo/seasonfill/internal/shared/http/dto"
	"github.com/alexmorbo/seasonfill/internal/shared/http/middleware"
)

// userItem is one row of GET /api/v1/admin/users and the body of a successful
// PATCH. password_hash is NEVER serialized. auth_source mirrors /me's
// derivation (forms|oidc|jellyfin). permissions reuses the /me nested shape.
type userItem struct {
	ID          uint              `json:"id" example:"1"`
	Username    string            `json:"username" example:"admin"`
	Email       *string           `json:"email" example:"admin@example.com"`
	Role        string            `json:"role" example:"admin" enums:"admin,user"`
	AuthSource  string            `json:"auth_source" example:"forms" enums:"forms,oidc,jellyfin"`
	Permissions dto.MePermissions `json:"permissions"`
	LastLoginAt *time.Time        `json:"last_login_at"`
	CreatedAt   time.Time         `json:"created_at"`
}

// userListResponse is the body of GET /api/v1/admin/users.
type userListResponse struct {
	Items []userItem `json:"items"`
}

// userPatchRequest is the body of PATCH /api/v1/admin/users/:id. Every field
// is optional (pointer): nil means "omitted, do not change". Unknown fields
// are rejected at decode time.
type userPatchRequest struct {
	Role           *string `json:"role,omitempty" example:"user" enums:"admin,user"`
	AutoApprove    *bool   `json:"auto_approve,omitempty"`
	Request        *bool   `json:"request,omitempty"`
	ManageRequests *bool   `json:"manage_requests,omitempty"`
	ManageUsers    *bool   `json:"manage_users,omitempty"`
	Request4K      *bool   `json:"request_4k,omitempty"`
}

// CallerDirectory is the narrow caller-lookup surface (GetByUsername) the
// handler needs to resolve the authenticated principal for self-lockout
// checks. *admin/persistence.UserRepository satisfies it structurally — kept
// narrow (not ports.UserRepository) mirroring the request handler's
// UserDirectory.
type CallerDirectory interface {
	GetByUsername(ctx context.Context, username string) (admin.User, error)
}

// UsersHandler serves the Ф8-U-6b admin user-management routes:
// GET /api/v1/admin/users, PATCH /api/v1/admin/users/:id,
// DELETE /api/v1/admin/users/:id. All three sit behind the manage_users
// permission guard (registered in edge/server.go).
type UsersHandler struct {
	uc     *authapp.UsersUseCase
	users  CallerDirectory
	logger *slog.Logger
}

// NewUsersHandler panics on nil uc/users (init-time wiring bug).
func NewUsersHandler(uc *authapp.UsersUseCase, users CallerDirectory, logger *slog.Logger) *UsersHandler {
	if uc == nil {
		panic("rest.NewUsersHandler: uc must not be nil")
	}
	if users == nil {
		panic("rest.NewUsersHandler: users must not be nil")
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &UsersHandler{uc: uc, users: users, logger: logger}
}

// List is GET /api/v1/admin/users.
//
// @Summary     List all users (admin)
// @Tags        admin-users
// @Produce     json
// @Success     200  {object}  userListResponse
// @Failure     401  {object}  dto.ErrorResponse
// @Failure     403  {object}  dto.ErrorResponse
// @Security    CookieAuth
// @Security    ApiKeyAuth
// @Router      /admin/users [get]
func (h *UsersHandler) List(c *gin.Context) {
	users, err := h.uc.List(c.Request.Context())
	if err != nil {
		h.logger.ErrorContext(c.Request.Context(), "admin.users.list.failed",
			slog.String("error", err.Error()))
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": "internal server error"})
		return
	}
	items := make([]userItem, 0, len(users))
	for _, u := range users {
		items = append(items, toUserItem(u))
	}
	c.JSON(http.StatusOK, userListResponse{Items: items})
}

// Patch is PATCH /api/v1/admin/users/:id.
//
// @Summary     Patch a user's role and/or RBAC permissions (admin)
// @Tags        admin-users
// @Accept      json
// @Produce     json
// @Param       id    path      int              true  "User id"
// @Param       body  body      userPatchRequest  true  "Partial role/permission patch"
// @Success     200   {object}  userItem
// @Failure     400   {object}  dto.ErrorResponse
// @Failure     401   {object}  dto.ErrorResponse
// @Failure     403   {object}  dto.ErrorResponse
// @Failure     404   {object}  dto.ErrorResponse
// @Failure     409   {object}  dto.ErrorResponse
// @Security    CookieAuth
// @Security    ApiKeyAuth
// @Router      /admin/users/{id} [patch]
func (h *UsersHandler) Patch(c *gin.Context) {
	id, ok := parseUserID(c)
	if !ok {
		return
	}
	caller, ok := h.caller(c)
	if !ok {
		return
	}

	ct := c.GetHeader("Content-Type")
	if !strings.HasPrefix(ct, jsonPrefix) {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{
			"error": "content-type must be application/json", "code": "BAD_REQUEST",
		})
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, meBodyLimit)
	dec := json.NewDecoder(c.Request.Body)
	dec.DisallowUnknownFields()
	var body userPatchRequest
	if err := dec.Decode(&body); err != nil && !errors.Is(err, io.EOF) {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": "malformed body", "code": "BAD_REQUEST"})
		return
	}

	patch := authapp.UsersPatch{
		Role:           body.Role,
		AutoApprove:    body.AutoApprove,
		Request:        body.Request,
		ManageRequests: body.ManageRequests,
		ManageUsers:    body.ManageUsers,
		Request4K:      body.Request4K,
	}

	updated, err := h.uc.Patch(c.Request.Context(), caller, id, patch)
	if err != nil {
		h.writePatchError(c, err)
		return
	}
	c.JSON(http.StatusOK, toUserItem(updated))
}

// Delete is DELETE /api/v1/admin/users/:id.
//
// @Summary     Delete a user (admin)
// @Tags        admin-users
// @Produce     json
// @Param       id  path  int  true  "User id"
// @Success     204
// @Failure     401  {object}  dto.ErrorResponse
// @Failure     403  {object}  dto.ErrorResponse
// @Failure     404  {object}  dto.ErrorResponse
// @Failure     409  {object}  dto.ErrorResponse
// @Security    CookieAuth
// @Security    ApiKeyAuth
// @Router      /admin/users/{id} [delete]
func (h *UsersHandler) Delete(c *gin.Context) {
	id, ok := parseUserID(c)
	if !ok {
		return
	}
	caller, ok := h.caller(c)
	if !ok {
		return
	}
	if err := h.uc.Delete(c.Request.Context(), caller, id); err != nil {
		h.writeDeleteError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

// caller resolves the authenticated principal. The api-key automation
// principal has no stored row and is admin-equivalent (see RequirePermission)
// — synthesize an admin with ID 0 so it is exempt from self-lockout while the
// last-admin guard still applies. A missing/unknown username 401s (defensive:
// the manage_users guard already ran).
func (h *UsersHandler) caller(c *gin.Context) (admin.User, bool) {
	username := c.GetString(middleware.UsernameContextKey)
	if username == "api-key" {
		return admin.User{Role: admin.RoleAdmin, ManageUsers: true}, true
	}
	if username == "" {
		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
			"error": "unauthorized", "code": "UNAUTHORIZED",
		})
		return admin.User{}, false
	}
	u, err := h.users.GetByUsername(c.Request.Context(), username)
	if err != nil {
		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
			"error": "unauthorized", "code": "UNAUTHORIZED",
		})
		return admin.User{}, false
	}
	return u, true
}

func (h *UsersHandler) writePatchError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, authapp.ErrInvalidRole):
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{
			"error": "role must be admin or user", "code": "INVALID_ROLE",
		})
	case errors.Is(err, authapp.ErrUserNotFound):
		c.AbortWithStatusJSON(http.StatusNotFound, gin.H{
			"error": "user not found", "code": "NOT_FOUND",
		})
	case errors.Is(err, authapp.ErrSelfLockout):
		c.AbortWithStatusJSON(http.StatusConflict, gin.H{
			"error": "cannot change your own admin role", "code": "SELF_LOCKOUT",
		})
	case errors.Is(err, authapp.ErrLastAdmin):
		c.AbortWithStatusJSON(http.StatusConflict, gin.H{
			"error": "cannot remove the last administrator", "code": "LAST_ADMIN",
		})
	default:
		h.logger.ErrorContext(c.Request.Context(), "admin.users.patch.failed",
			slog.String("error", err.Error()))
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": "internal server error"})
	}
}

func (h *UsersHandler) writeDeleteError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, authapp.ErrUserNotFound):
		c.AbortWithStatusJSON(http.StatusNotFound, gin.H{
			"error": "user not found", "code": "NOT_FOUND",
		})
	case errors.Is(err, authapp.ErrSelfLockout):
		c.AbortWithStatusJSON(http.StatusConflict, gin.H{
			"error": "cannot delete yourself", "code": "SELF_LOCKOUT",
		})
	case errors.Is(err, authapp.ErrLastAdmin):
		c.AbortWithStatusJSON(http.StatusConflict, gin.H{
			"error": "cannot remove the last administrator", "code": "LAST_ADMIN",
		})
	default:
		h.logger.ErrorContext(c.Request.Context(), "admin.users.delete.failed",
			slog.String("error", err.Error()))
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": "internal server error"})
	}
}

// parseUserID reads the :id path param as a positive uint. Writes a 400 and
// returns false on a non-numeric or zero id.
func parseUserID(c *gin.Context) (uint, bool) {
	raw := c.Param("id")
	n, err := strconv.ParseUint(raw, 10, 64)
	if err != nil || n == 0 {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{
			"error": "invalid user id", "code": "BAD_REQUEST",
		})
		return 0, false
	}
	return uint(n), true
}

// toUserItem projects a domain user onto the wire item, deriving auth_source
// via the shared userAuthMode helper (me_handler.go). password_hash is never
// copied.
func toUserItem(u admin.User) userItem {
	return userItem{
		ID:         u.ID,
		Username:   u.Username,
		Email:      u.Email,
		Role:       u.Role,
		AuthSource: userAuthMode(u),
		Permissions: dto.MePermissions{
			AutoApprove:    u.AutoApprove,
			Request:        u.Request,
			ManageRequests: u.ManageRequests,
			ManageUsers:    u.ManageUsers,
			Request4K:      u.Request4K,
		},
		LastLoginAt: u.LastLoginAt,
		CreatedAt:   u.CreatedAt,
	}
}
