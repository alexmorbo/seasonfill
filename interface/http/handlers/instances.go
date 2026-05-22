package handlers

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/alexmorbo/seasonfill/domain/instance"
	"github.com/alexmorbo/seasonfill/interface/healthcheck"
	"github.com/alexmorbo/seasonfill/interface/http/dto"
)

type InstancesHandler struct {
	checker *healthcheck.Checker
}

func NewInstancesHandler(checker *healthcheck.Checker) *InstancesHandler {
	return &InstancesHandler{checker: checker}
}

// List returns the current health snapshot for every configured instance.
// Behind X-Api-Key (mounted under `/api/v1`).
//
// @Summary     List Sonarr instance health
// @Description Latest snapshot from the in-memory checker.
// @Tags        instances
// @Produce     json
// @Success     200  {object}  dto.InstanceList
// @Failure     401  {object}  dto.ErrorResponse
// @Security    CookieAuth
// @Security    ApiKeyAuth
// @Router      /instances [get]
func (h *InstancesHandler) List(c *gin.Context) {
	snap := h.checker.Snapshot()
	out := make([]dto.Instance, 0, len(snap))
	for _, s := range snap {
		out = append(out, toInstanceDTO(s))
	}
	c.JSON(http.StatusOK, dto.InstanceList{Instances: out})
}

func toInstanceDTO(s instance.Snapshot) dto.Instance {
	var lastCheckAt *time.Time
	if !s.LastCheckAt.IsZero() {
		t := s.LastCheckAt
		lastCheckAt = &t
	}
	return dto.Instance{
		Name:             s.Name,
		Health:           string(s.Health),
		LastCheckAt:      lastCheckAt,
		LastError:        s.LastError,
		TransitionsCount: s.TransitionsCount,
	}
}
