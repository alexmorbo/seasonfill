package middleware

import (
	"log/slog"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/alexmorbo/seasonfill/internal/logger"
	"github.com/alexmorbo/seasonfill/internal/shared/ports"
)

// RequestLoggerMiddleware attaches a request-scoped *slog.Logger and
// trace_id to each request, then logs the completed request as a single
// JSON record shaped per PRD §6.5: top-level domain="http" + trace_id,
// nested http group.
//
// Replaces the previous middleware.RequestLogging. Keeps the X-Request-ID
// header round-trip and the internal/logger.WithTraceID injection so
// unmigrated call sites that emit logs via Logger.LogAttrs(ctx, ...) keep
// getting trace_id stamped automatically by internal/logger.contextHandler.
func RequestLoggerMiddleware(base *slog.Logger) gin.HandlerFunc {
	domained := ports.DomainLogger(base, "http")
	return func(c *gin.Context) {
		traceID := c.GetHeader("X-Request-ID")
		if traceID == "" {
			traceID = uuid.New().String()
		}
		c.Writer.Header().Set("X-Request-ID", traceID)

		reqLogger := domained.With(slog.String("trace_id", traceID))
		rc := ports.RequestContext{Logger: reqLogger, TraceID: traceID}

		ctx := ports.WithRequestContext(c.Request.Context(), rc)
		ctx = logger.WithTraceID(ctx, traceID) // bridge: keeps contextHandler injection working
		c.Request = c.Request.WithContext(ctx)

		start := time.Now()
		c.Next()

		reqLogger.LogAttrs(ctx, slog.LevelInfo, "http_request",
			slog.Group("http",
				slog.String("method", c.Request.Method),
				slog.String("path", c.Request.URL.Path),
				slog.Int("status", c.Writer.Status()),
				slog.Int64("duration_ms", time.Since(start).Milliseconds()),
				slog.Int("bytes_out", c.Writer.Size()),
				slog.String("remote_ip", c.ClientIP()),
				slog.String("request_id", traceID),
			),
		)
	}
}
