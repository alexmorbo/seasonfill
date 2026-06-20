package handlers

import (
	"log/slog"
	"net/http"

	"github.com/gin-gonic/gin"
)

// WriteError is the exported alias of writeError. Story 431 (A-1-5)
// added it so the new internal/grab/rest package can dispatch the same
// JSON error envelope without forking the helper. The unexported name
// stays so the catch-all handlers package's existing 20-ish call sites
// don't churn.
func WriteError(c *gin.Context, status int, msg string) {
	writeError(c, status, msg)
}

// WriteInternalError is the exported alias of writeInternalError. See
// WriteError above for the rationale (story 431 vertical-slice carve).
func WriteInternalError(c *gin.Context, log *slog.Logger, event string, err error, attrs ...slog.Attr) {
	writeInternalError(c, log, event, err, attrs...)
}

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
