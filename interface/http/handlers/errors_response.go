package handlers

import (
	"log/slog"
	"net/http"

	"github.com/gin-gonic/gin"
)

// writeInternalError is the single boundary at which a 5xx HTTP
// response is written. It (a) logs the underlying error + caller
// attrs at ERROR level so operators can correlate, then (b) writes
// a stable generic body so DB/driver internals never leak to the
// client. `log` may be nil; slog.Default() is used in that case.
// Callers should pass a stable `event` name (e.g.
// "audit_list_scans_failed") so grep over logs surfaces all
// instances of the failure mode.
func writeInternalError(c *gin.Context, log *slog.Logger, event string, err error, attrs ...slog.Attr) {
	if log == nil {
		log = slog.Default()
	}
	full := make([]slog.Attr, 0, len(attrs)+1)
	full = append(full, slog.String("error", err.Error()))
	full = append(full, attrs...)
	log.LogAttrs(c.Request.Context(), slog.LevelError, event, full...)
	writeError(c, http.StatusInternalServerError, "internal server error")
}
