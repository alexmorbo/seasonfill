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

// Live always 200 — the process is responding.
func (h *HealthHandler) Live(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

// Ready returns 200 iff DB is up AND at least one Sonarr instance is
// Available. Otherwise 503 with a `reasons` array body that enumerates
// every failed predicate.
func (h *HealthHandler) Ready(c *gin.Context) {
	ctx := c.Request.Context()
	dbOK := h.checker.DatabaseUp(ctx)
	anyInstance := h.checker.AnyInstanceAvailable()
	reasons := []string{}
	if !dbOK {
		reasons = append(reasons, "database unreachable")
	}
	if !anyInstance {
		reasons = append(reasons, "no sonarr instance available")
	}
	if len(reasons) > 0 {
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"status":    "unavailable",
			"database":  dbOK,
			"sonarr":    anyInstance,
			"instances": h.checker.Snapshot(),
			"reasons":   reasons,
		})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"status":    "ok",
		"database":  true,
		"sonarr":    true,
		"instances": h.checker.Snapshot(),
	})
}
