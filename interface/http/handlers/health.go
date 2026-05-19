package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/alexmorbo/seasonfill/interface/healthcheck"
)

type HealthHandler struct {
	checker *healthcheck.Checker
}

func NewHealthHandler(checker *healthcheck.Checker) *HealthHandler {
	return &HealthHandler{checker: checker}
}

func (h *HealthHandler) Live(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

func (h *HealthHandler) Ready(c *gin.Context) {
	dbOK := h.checker.DatabaseUp(c.Request.Context())
	anyInstance := h.checker.AnyInstanceAvailable()
	if !dbOK || !anyInstance {
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"status":   "unavailable",
			"database": dbOK,
			"sonarr":   anyInstance,
		})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"status":    "ok",
		"database":  true,
		"instances": h.checker.Snapshot(),
	})
}
