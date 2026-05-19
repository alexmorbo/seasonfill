package handlers

import (
	"github.com/gin-gonic/gin"

	"github.com/alexmorbo/seasonfill/internal/observability"
)

func MetricsHandler() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Writer.Header().Set("Content-Type", "text/plain; version=0.0.4")
		observability.WritePrometheus(c.Writer)
	}
}
