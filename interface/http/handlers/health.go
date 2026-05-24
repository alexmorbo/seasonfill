package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/alexmorbo/seasonfill/interface/healthcheck"
	"github.com/alexmorbo/seasonfill/interface/http/dto"
)

type HealthHandler struct {
	checker *healthcheck.Checker
}

func NewHealthHandler(checker *healthcheck.Checker) *HealthHandler {
	return &HealthHandler{checker: checker}
}

// Live always 200 — the process is responding.
//
// @Summary     Liveness probe
// @Tags        health
// @Produce     json
// @Success     200  {object}  dto.HealthStatus
// @Router      /healthz [get]
func (h *HealthHandler) Live(c *gin.Context) {
	c.JSON(http.StatusOK, dto.HealthStatus{Status: "ok"})
}

// Ready returns 200 iff DB is up AND at least one Sonarr instance is
// Available. Otherwise 503 with a `reasons` array body that enumerates
// every failed predicate.
//
// @Summary     Readiness probe
// @Description 200 only when DB AND ≥1 Sonarr instance are healthy.
// @Tags        health
// @Produce     json
// @Success     200  {object}  dto.ReadyStatus
// @Failure     503  {object}  dto.ReadyStatus
// @Router      /readyz [get]
func (h *HealthHandler) Ready(c *gin.Context) {
	ctx := c.Request.Context()
	dbOK := h.checker.DatabaseUp(ctx)
	anyInstance := h.checker.AnyInstanceAvailable()
	snap := h.checker.Snapshot()
	dtos := make([]dto.Instance, 0, len(snap))
	for _, s := range snap {
		dtos = append(dtos, snapshotToDTO(s, nil, nil))
	}
	reasons := []string{}
	if !dbOK {
		reasons = append(reasons, "database unreachable")
	}
	if !anyInstance {
		reasons = append(reasons, "no sonarr instance available")
	}
	if len(reasons) > 0 {
		c.JSON(http.StatusServiceUnavailable, dto.ReadyStatus{
			Status:    "unavailable",
			Database:  dbOK,
			Sonarr:    anyInstance,
			Instances: dtos,
			Reasons:   reasons,
		})
		return
	}
	c.JSON(http.StatusOK, dto.ReadyStatus{
		Status:    "ok",
		Database:  true,
		Sonarr:    true,
		Instances: dtos,
	})
}
