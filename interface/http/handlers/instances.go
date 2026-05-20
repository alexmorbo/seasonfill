package handlers

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/alexmorbo/seasonfill/domain/instance"
	"github.com/alexmorbo/seasonfill/interface/healthcheck"
)

type InstancesHandler struct {
	checker *healthcheck.Checker
}

func NewInstancesHandler(checker *healthcheck.Checker) *InstancesHandler {
	return &InstancesHandler{checker: checker}
}

type instanceView struct {
	Name             string     `json:"name"`
	Health           string     `json:"health"`
	LastCheckAt      *time.Time `json:"last_check_at,omitempty"`
	LastError        string     `json:"last_error,omitempty"`
	TransitionsCount int        `json:"transitions_count"`
}

// List returns the current health snapshot for every configured instance.
// Behind X-Api-Key (mounted under `/api/v1`).
func (h *InstancesHandler) List(c *gin.Context) {
	snap := h.checker.Snapshot()
	out := make([]instanceView, 0, len(snap))
	for _, s := range snap {
		out = append(out, toInstanceView(s))
	}
	c.JSON(http.StatusOK, gin.H{"instances": out})
}

func toInstanceView(s instance.Snapshot) instanceView {
	var lastCheckAt *time.Time
	if !s.LastCheckAt.IsZero() {
		t := s.LastCheckAt
		lastCheckAt = &t
	}
	return instanceView{
		Name:             s.Name,
		Health:           string(s.Health),
		LastCheckAt:      lastCheckAt,
		LastError:        s.LastError,
		TransitionsCount: s.TransitionsCount,
	}
}
