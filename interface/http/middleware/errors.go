package middleware

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/alexmorbo/seasonfill/application/ports"
	sharedErrors "github.com/alexmorbo/seasonfill/internal/shared/errors"
)

// ErrorResponseMiddleware converts errors pushed via c.Error(...) into a
// structured JSON response. Dormant when no handler emits typed errors —
// F-2c migrates handlers; until then this middleware is a no-op on every
// successful request.
//
// Behavior:
//   - if c.Errors is empty → no-op.
//   - if c.Writer.Written() → no-op (handler already responded).
//   - else take c.Errors.Last().Err, derive status via
//     sharedErrors.StatusCode, respond
//     {"error": "<slug>", "message": "<err.Error()>"}.
//   - log the failure: Info for 4xx, Error for 5xx.
//
// Sentinel fallback (F-2c-1): when the typed-error catalog does not
// match but the chain still carries ports.ErrNotFound — e.g. an
// application-layer use case that has not yet migrated to typed errors,
// or a test fixture returning the bare sentinel — the middleware
// downgrades the response to 404 with a generic "not_found" slug.
// Typed errors always win the dispatch; this is a safety net.
//
// The logger is enriched with trace_id automatically because
// internal/logger.contextHandler reads it off c.Request.Context().
func ErrorResponseMiddleware(log *slog.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Next()
		if len(c.Errors) == 0 {
			return
		}
		if c.Writer.Written() {
			return
		}
		err := c.Errors.Last().Err
		status := sharedErrors.StatusCode(err)
		code := sharedErrors.ErrorCode(err)
		if status == http.StatusInternalServerError &&
			code == "internal_error" &&
			errors.Is(err, ports.ErrNotFound) {
			status = http.StatusNotFound
			code = "not_found"
		}
		ctx := c.Request.Context()

		level := slog.LevelInfo
		if status >= 500 {
			level = slog.LevelError
		}
		log.LogAttrs(ctx, level, "http_error",
			slog.Group("http",
				slog.String("method", c.Request.Method),
				slog.String("path", c.Request.URL.Path),
				slog.Int("status", status),
				slog.String("code", code),
				slog.String("error", err.Error()),
			),
		)

		c.JSON(status, gin.H{
			"error":   code,
			"message": err.Error(),
		})
	}
}
