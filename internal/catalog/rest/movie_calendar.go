package rest

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/alexmorbo/seasonfill/internal/catalog/app/moviecalendar"
	"github.com/alexmorbo/seasonfill/internal/shared/http/dto"
	"github.com/alexmorbo/seasonfill/internal/shared/media"
)

// MovieCalendarHandler serves GET /api/v1/movies/calendar (Ф6-R-6a).
type MovieCalendarHandler struct {
	uc       *moviecalendar.UseCase
	resolver *media.Resolver // nil-OK: raw TMDB paths flow through unchanged
	logger   *slog.Logger
}

func NewMovieCalendarHandler(uc *moviecalendar.UseCase, resolver *media.Resolver, logger *slog.Logger) *MovieCalendarHandler {
	if logger == nil {
		logger = slog.Default()
	}
	return &MovieCalendarHandler{uc: uc, resolver: resolver, logger: logger}
}

// Get handles GET /api/v1/movies/calendar.
//
// @Summary     Movie release calendar
// @Description Theatrical/digital/physical release milestones for library
// @Description movies over a window (default ±3 months). Days group by UTC date.
// @Tags        movies
// @Produce     json
// @Param       from query string false "window start (YYYY-MM-DD)"
// @Param       to   query string false "window end (YYYY-MM-DD, inclusive)"
// @Success     200 {object} dto.MovieCalendarDTO
// @Failure     400 {object} dto.ErrorResponse
// @Failure     401 {object} dto.ErrorResponse
// @Failure     500 {object} dto.ErrorResponse
// @Security    CookieAuth
// @Security    ApiKeyAuth
// @Router      /movies/calendar [get]
func (h *MovieCalendarHandler) Get(c *gin.Context) {
	const layout = "2006-01-02"
	var q moviecalendar.Query
	if v := c.Query("from"); v != "" {
		t, err := time.Parse(layout, v)
		if err != nil {
			c.JSON(http.StatusBadRequest, dto.ErrorResponse{Error: "invalid from date (want YYYY-MM-DD)"})
			return
		}
		q.From = t.UTC()
	}
	if v := c.Query("to"); v != "" {
		t, err := time.Parse(layout, v)
		if err != nil {
			c.JSON(http.StatusBadRequest, dto.ErrorResponse{Error: "invalid to date (want YYYY-MM-DD)"})
			return
		}
		q.To = t.UTC().Add(24*time.Hour - time.Nanosecond)
	}
	ctx := c.Request.Context()
	rep, err := h.uc.Build(ctx, q)
	if err != nil {
		h.logger.ErrorContext(ctx, "movie_calendar_failed", slog.String("error", err.Error()))
		c.JSON(http.StatusInternalServerError, dto.ErrorResponse{Error: "calendar unavailable"})
		return
	}
	out := dto.MovieCalendarDTO{GeneratedAt: rep.GeneratedAt, From: rep.From, To: rep.To}
	for _, d := range rep.Days {
		day := dto.MovieCalendarDayDTO{Date: d.Date}
		for _, e := range d.Events {
			poster := e.Poster
			if h.resolver != nil {
				if hash := h.resolver.Resolve(ctx, e.Poster, "w342", "poster_w342"); hash != nil {
					poster = hash
				}
			}
			day.Events = append(day.Events, dto.MovieCalendarEventDTO{MovieID: e.MovieID, TMDBID: e.TMDBID, Title: e.Title, Poster: poster, Milestone: e.Milestone, Date: e.Date})
		}
		out.Days = append(out.Days, day)
	}
	c.JSON(http.StatusOK, out)
}
