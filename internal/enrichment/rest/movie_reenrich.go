package rest

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
)

// MovieChangeMarker is the narrow write port the backfill handler needs: stamp
// tmdb_changed_at = now on every tmdb_id-bearing movie, returning the number of
// rows marked. *enrichment/persistence.MovieRepository satisfies it via
// MarkAllMoviesChanged (kept narrow, not the whole repo, mirroring the other
// rest-package ports).
type MovieChangeMarker interface {
	MarkAllMoviesChanged(ctx context.Context, now time.Time) (int64, error)
}

// movieReenrichResponse is the body of POST /api/v1/admin/movies/reenrich.
type movieReenrichResponse struct {
	Marked int64 `json:"marked" example:"411"`
}

// MovieReenrichHandler serves the one-shot movie re-enrichment backfill trigger
// (audit F-Ф1-07). It marks EVERY movie carrying a tmdb_id as changed so the
// throttled MovieRefreshScheduler re-enriches all of them once (the new Ф1.1
// sections + ru-RU overview). Mark-and-drain: the handler only stamps rows and
// returns the count — it never enriches inline.
type MovieReenrichHandler struct {
	marker MovieChangeMarker
	logger *slog.Logger
}

// NewMovieReenrichHandler panics on a nil marker (init-time wiring bug). A nil
// logger falls back to slog.Default().
func NewMovieReenrichHandler(marker MovieChangeMarker, logger *slog.Logger) *MovieReenrichHandler {
	if marker == nil {
		panic("rest.NewMovieReenrichHandler: marker must not be nil")
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &MovieReenrichHandler{marker: marker, logger: logger}
}

// Trigger handles POST /api/v1/admin/movies/reenrich.
//
// @Summary     Backfill: re-enrich all movies (admin)
// @Description Marks every movie carrying a tmdb_id as changed so the movie
// @Description refresh scheduler re-enriches all of them once (cast, genres,
// @Description keywords, companies, videos, recommendations + ru-RU overview).
// @Description Idempotent + throttled: the scheduler drains the marked set over
// @Description multiple ticks at its tier LIMIT + 15m race guard — the endpoint
// @Description returns immediately with the number of movies marked.
// @Tags        admin-movies
// @Produce     json
// @Success     200  {object}  movieReenrichResponse
// @Failure     401  {object}  dto.ErrorResponse
// @Failure     403  {object}  dto.ErrorResponse
// @Failure     500  {object}  dto.ErrorResponse
// @Security    CookieAuth
// @Security    ApiKeyAuth
// @Router      /admin/movies/reenrich [post]
func (h *MovieReenrichHandler) Trigger(c *gin.Context) {
	ctx := c.Request.Context()
	marked, err := h.marker.MarkAllMoviesChanged(ctx, time.Now().UTC())
	if err != nil {
		h.logger.ErrorContext(ctx, "admin.movies.reenrich.failed",
			slog.String("error", err.Error()))
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": "internal server error"})
		return
	}
	h.logger.InfoContext(ctx, "admin.movies.reenrich.marked", slog.Int64("marked", marked))
	c.JSON(http.StatusOK, movieReenrichResponse{Marked: marked})
}
