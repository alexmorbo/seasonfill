package rest

import (
	"errors"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	notifapp "github.com/alexmorbo/seasonfill/internal/notification/app"
	ports "github.com/alexmorbo/seasonfill/internal/shared/dataports"
	"github.com/alexmorbo/seasonfill/internal/shared/http/dto"
	"github.com/alexmorbo/seasonfill/internal/shared/http/middleware"
	sharedports "github.com/alexmorbo/seasonfill/internal/shared/ports"
)

type AgentsHandler struct {
	uc     *notifapp.AgentsUseCase
	users  ports.UserRepository
	logger *slog.Logger
}

func NewAgentsHandler(uc *notifapp.AgentsUseCase, users ports.UserRepository, logger *slog.Logger) *AgentsHandler {
	if logger == nil {
		logger = sharedports.DomainLogger(slog.Default(), "http")
	}
	return &AgentsHandler{uc: uc, users: users, logger: logger}
}

// callerID resolves the authenticated admin id from context; 401 on miss. The
// api-key automation principal has no user row — it resolves to the seed-admin
// id (the SAME row mig-058 backfills to). Owns any agent it creates (Ф8-U-5).
func (h *AgentsHandler) callerID(c *gin.Context) (int64, bool) {
	username := c.GetString(middleware.UsernameContextKey)
	if username == "" {
		c.AbortWithStatusJSON(http.StatusUnauthorized, dto.ErrorResponse{Error: "unauthorized", Code: "UNAUTHORIZED"})
		return 0, false
	}
	if username == "api-key" {
		id, err := h.users.FirstAdminID(c.Request.Context())
		if err != nil {
			c.AbortWithStatusJSON(http.StatusUnauthorized, dto.ErrorResponse{Error: "unauthorized", Code: "UNAUTHORIZED"})
			return 0, false
		}
		return id, true
	}
	u, err := h.users.GetByUsername(c.Request.Context(), username)
	if err != nil {
		c.AbortWithStatusJSON(http.StatusUnauthorized, dto.ErrorResponse{Error: "unauthorized", Code: "UNAUTHORIZED"})
		return 0, false
	}
	return int64(u.ID), true
}

// List returns the caller's agents (masked).
// @Summary     List notification agents (masked)
// @Tags        notifications
// @Produce     json
// @Success     200 {object} dto.NotificationAgentListResponse
// @Failure     401 {object} dto.ErrorResponse
// @Security    CookieAuth
// @Security    ApiKeyAuth
// @Router      /notification-agents [get]
func (h *AgentsHandler) List(c *gin.Context) {
	ownerID, ok := h.callerID(c)
	if !ok {
		return
	}
	views, err := h.uc.ListByOwner(c.Request.Context(), ownerID)
	if err != nil {
		h.writeError(c, err)
		return
	}
	out := dto.NotificationAgentListResponse{Agents: make([]dto.NotificationAgentView, 0, len(views))}
	for _, v := range views {
		out.Agents = append(out.Agents, toDTO(v))
	}
	c.JSON(http.StatusOK, out)
}

// Get returns one of the caller's agents (masked).
// @Summary     Get one notification agent (masked)
// @Tags        notifications
// @Produce     json
// @Param       id path int true "Agent id"
// @Success     200 {object} dto.NotificationAgentView
// @Failure     404 {object} dto.ErrorResponse
// @Security    CookieAuth
// @Security    ApiKeyAuth
// @Router      /notification-agents/{id} [get]
func (h *AgentsHandler) Get(c *gin.Context) {
	id, ok := h.parseID(c)
	if !ok {
		return
	}
	ownerID, ok := h.callerID(c)
	if !ok {
		return
	}
	v, err := h.uc.Get(c.Request.Context(), id, ownerID)
	if err != nil {
		h.writeError(c, err)
		return
	}
	c.JSON(http.StatusOK, toDTO(v))
}

// Create persists a new agent (URL encrypted).
// @Summary     Create a notification agent
// @Tags        notifications
// @Accept      json
// @Produce     json
// @Param       body body dto.NotificationAgentCreateRequest true "Agent fields"
// @Success     201 {object} dto.NotificationAgentView
// @Failure     400 {object} dto.ErrorResponse
// @Security    CookieAuth
// @Security    ApiKeyAuth
// @Router      /notification-agents [post]
func (h *AgentsHandler) Create(c *gin.Context) {
	var req dto.NotificationAgentCreateRequest
	if !middleware.BindAndValidateJSON(c, &req) {
		return
	}
	ownerID, ok := h.callerID(c)
	if !ok {
		return
	}
	id, err := h.uc.Create(c.Request.Context(), ownerID, req.Name, req.URL, req.Enabled, req.EventTypes)
	if err != nil {
		h.writeError(c, err)
		return
	}
	v, err := h.uc.Get(c.Request.Context(), id, ownerID)
	if err != nil {
		h.writeError(c, err)
		return
	}
	c.JSON(http.StatusCreated, toDTO(v))
}

// Update mutates an agent; empty url = keep existing config.
// @Summary     Update a notification agent
// @Tags        notifications
// @Accept      json
// @Produce     json
// @Param       id path int true "Agent id"
// @Param       body body dto.NotificationAgentUpdateRequest true "Agent fields"
// @Success     200 {object} dto.NotificationAgentView
// @Failure     400 {object} dto.ErrorResponse
// @Failure     404 {object} dto.ErrorResponse
// @Security    CookieAuth
// @Security    ApiKeyAuth
// @Router      /notification-agents/{id} [put]
func (h *AgentsHandler) Update(c *gin.Context) {
	id, ok := h.parseID(c)
	if !ok {
		return
	}
	var req dto.NotificationAgentUpdateRequest
	if !middleware.BindAndValidateJSON(c, &req) {
		return
	}
	ownerID, ok := h.callerID(c)
	if !ok {
		return
	}
	if err := h.uc.Update(c.Request.Context(), id, ownerID, req.Name, req.URL, req.Enabled, req.EventTypes); err != nil {
		h.writeError(c, err)
		return
	}
	v, err := h.uc.Get(c.Request.Context(), id, ownerID)
	if err != nil {
		h.writeError(c, err)
		return
	}
	c.JSON(http.StatusOK, toDTO(v))
}

// Delete removes an agent.
// @Summary     Delete a notification agent
// @Tags        notifications
// @Param       id path int true "Agent id"
// @Success     204
// @Failure     404 {object} dto.ErrorResponse
// @Security    CookieAuth
// @Security    ApiKeyAuth
// @Router      /notification-agents/{id} [delete]
func (h *AgentsHandler) Delete(c *gin.Context) {
	id, ok := h.parseID(c)
	if !ok {
		return
	}
	ownerID, ok := h.callerID(c)
	if !ok {
		return
	}
	if err := h.uc.Delete(c.Request.Context(), id, ownerID); err != nil {
		h.writeError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

// Test sends a fixed test notification synchronously.
// @Summary     Send a test notification via an agent
// @Tags        notifications
// @Produce     json
// @Param       id path int true "Agent id"
// @Success     200 {object} dto.NotificationTestResponse
// @Failure     404 {object} dto.ErrorResponse
// @Failure     502 {object} dto.ErrorResponse "SEND_FAILED"
// @Security    CookieAuth
// @Security    ApiKeyAuth
// @Router      /notification-agents/{id}/test [post]
func (h *AgentsHandler) Test(c *gin.Context) {
	id, ok := h.parseID(c)
	if !ok {
		return
	}
	ownerID, ok := h.callerID(c)
	if !ok {
		return
	}
	if err := h.uc.Test(c.Request.Context(), id, ownerID); err != nil {
		if errors.Is(err, ports.ErrNotFound) {
			c.AbortWithStatusJSON(http.StatusNotFound, dto.ErrorResponse{Error: "agent not found", Code: "NOT_FOUND"})
			return
		}
		// Do NOT echo err.Error() verbatim (may include scheme); generic + logged.
		h.logger.WarnContext(c.Request.Context(), "notification_agent_test_failed",
			slog.Int64("agent_id", id), slog.String("error", err.Error()))
		c.AbortWithStatusJSON(http.StatusBadGateway, dto.ErrorResponse{Error: "notification send failed", Code: "SEND_FAILED"})
		return
	}
	c.JSON(http.StatusOK, dto.NotificationTestResponse{OK: true})
}

func (h *AgentsHandler) parseID(c *gin.Context) (int64, bool) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		c.AbortWithStatusJSON(http.StatusBadRequest, dto.ErrorResponse{Error: "invalid id", Code: "BAD_REQUEST"})
		return 0, false
	}
	return id, true
}

func (h *AgentsHandler) writeError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, ports.ErrNotFound):
		c.AbortWithStatusJSON(http.StatusNotFound, dto.ErrorResponse{Error: "agent not found", Code: "NOT_FOUND"})
	case errors.Is(err, notifapp.ErrInvalidAgent):
		c.AbortWithStatusJSON(http.StatusBadRequest, dto.ErrorResponse{Error: err.Error(), Code: "BAD_REQUEST"})
	default:
		h.logger.ErrorContext(c.Request.Context(), "notification_agents_handler_error", slog.String("error", err.Error()))
		c.AbortWithStatusJSON(http.StatusInternalServerError, dto.ErrorResponse{Error: "internal error", Code: "INTERNAL"})
	}
}

func toDTO(v notifapp.AgentView) dto.NotificationAgentView {
	return dto.NotificationAgentView{
		ID: v.ID, Name: v.Name, Enabled: v.Enabled, EventTypes: v.EventTypes,
		Configured: v.Configured, Scheme: v.Scheme,
	}
}
