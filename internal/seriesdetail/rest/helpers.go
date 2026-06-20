package rest

import (
	"log/slog"
	"net/http"

	"github.com/gin-gonic/gin"
)

// writeError aborts the request with a JSON error envelope at the
// given HTTP status. Local copy of the same helper that lives in
// interface/http/handlers/pagination.go — story 446 (A-1-20) carried
// the seriesdetail rest handlers out of the catch-all handlers
// package. Mirrors the same duplication pattern adopted by
// internal/catalog/rest/helpers.go (story 444): two duplicates avoid
// the import cycle that seriesdetailrest → handlers (helpers) and
// handlers → seriesdetailrest (handler types) would otherwise create.
func writeError(c *gin.Context, status int, msg string) {
	c.AbortWithStatusJSON(status, gin.H{"error": msg})
}

// writeInternalError is the single boundary at which a 5xx HTTP
// response is written. It (a) logs the underlying error + caller
// attrs at ERROR level so operators can correlate, then (b) writes
// a stable generic body so DB/driver internals never leak to the
// client. `log` may be nil; slog.Default() is used in that case.
// Local copy mirrors the helper in interface/http/handlers/errors_response.go;
// see writeError's godoc above for the cycle-avoidance rationale.
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
