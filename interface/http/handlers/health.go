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

// Ready reports pod-local readiness — currently DB connectivity.
// External Sonarr instance availability is operational status and is
// exposed via GET /api/v1/instances and the seasonfill_instance_health
// Prometheus gauge; it deliberately does NOT gate this probe so a
// misconfigured upstream cannot evict the pod from the K8s Service
// and lock the operator out of the Settings UI.
//
// @Summary     Readiness probe
// @Description 200 when pod-local dependencies (DB) are healthy.
// @Description External Sonarr health is reported via /api/v1/instances.
// @Tags        health
// @Produce     json
// @Success     200  {object}  dto.ReadyStatus
// @Failure     503  {object}  dto.ReadyStatus
// @Router      /readyz [get]
func (h *HealthHandler) Ready(c *gin.Context) {
	dbOK := h.checker.DatabaseUp(c.Request.Context())
	if !dbOK {
		c.JSON(http.StatusServiceUnavailable, dto.ReadyStatus{
			Status:   "unavailable",
			Database: false,
		})
		return
	}
	c.JSON(http.StatusOK, dto.ReadyStatus{
		Status:   "ok",
		Database: true,
	})
}
