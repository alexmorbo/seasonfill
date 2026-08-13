package rest

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/alexmorbo/seasonfill/internal/catalog/app/calendar"
	"github.com/alexmorbo/seasonfill/internal/shared/http/dto"
	"github.com/alexmorbo/seasonfill/internal/shared/media"
)

// CalendarUseCase is the narrow port the handler depends on. Production:
// *calendar.UseCase. Tests pass a real UseCase over a fake repository.
type CalendarUseCase interface {
	Build(ctx context.Context, q calendar.Query) (calendar.Report, error)
}

// CalendarHandler serves GET /api/v1/calendar — the read-only release calendar.
type CalendarHandler struct {
	uc       CalendarUseCase
	resolver *media.Resolver // nil-OK: raw TMDB paths flow through unchanged
	logger   *slog.Logger
}

func NewCalendarHandler(uc CalendarUseCase, resolver *media.Resolver, logger *slog.Logger) *CalendarHandler {
	if logger == nil {
		logger = slog.Default()
	}
	return &CalendarHandler{uc: uc, resolver: resolver, logger: logger}
}

const calendarDateLayout = "2006-01-02"

// Get handles GET /api/v1/calendar.
//
// @Summary     Release calendar
// @Description Release calendar over TMDB episode air dates (covers library
// @Description AND followed series). Days group events by UTC date; each
// @Description event carries a milestone (premiere/finale/return) and a
// @Description per-episode library status. Window defaults to ±3 months
// @Description around now when from/to are omitted.
// @Tags        insights
// @Produce     json
// @Param       from           query string false "window start (YYYY-MM-DD)"
// @Param       to             query string false "window end (YYYY-MM-DD, inclusive)"
// @Param       scope          query string false "library|followed|all (default all)"
// @Param       instance       query string false "narrow library scope to one Sonarr instance"
// @Param       only-library   query bool   false "shortcut for scope=library"
// @Param       only-premieres query bool   false "keep only season-premiere events"
// @Param       lang           query string false "preferred BCP-47 title/poster language"
// @Success     200 {object} dto.CalendarDTO
// @Failure     400 {object} dto.ErrorResponse
// @Failure     401 {object} dto.ErrorResponse
// @Failure     500 {object} dto.ErrorResponse
// @Security    CookieAuth
// @Security    ApiKeyAuth
// @Router      /calendar [get]
func (h *CalendarHandler) Get(c *gin.Context) {
	q := calendar.Query{
		Lang:          c.Query("lang"),
		Scope:         c.Query("scope"),
		Instance:      c.Query("instance"),
		OnlyPremieres: isTrue(c.Query("only-premieres")),
	}
	if isTrue(c.Query("only-library")) {
		q.Scope = "library"
	}
	if v := c.Query("from"); v != "" {
		t, err := time.Parse(calendarDateLayout, v)
		if err != nil {
			c.JSON(http.StatusBadRequest, dto.ErrorResponse{Error: "invalid from date (want YYYY-MM-DD)"})
			return
		}
		q.From = t.UTC()
	}
	if v := c.Query("to"); v != "" {
		t, err := time.Parse(calendarDateLayout, v)
		if err != nil {
			c.JSON(http.StatusBadRequest, dto.ErrorResponse{Error: "invalid to date (want YYYY-MM-DD)"})
			return
		}
		// Widen to end-of-day so the whole `to` date is inclusive.
		q.To = t.UTC().Add(24*time.Hour - time.Nanosecond)
	}

	ctx := c.Request.Context()
	rep, err := h.uc.Build(ctx, q)
	if err != nil {
		h.logger.ErrorContext(ctx, "calendar_query_failed",
			slog.String("error", err.Error()))
		c.JSON(http.StatusInternalServerError, dto.ErrorResponse{Error: "calendar unavailable"})
		return
	}
	c.JSON(http.StatusOK, h.toCalendarDTO(ctx, rep))
}

func isTrue(v string) bool { return v == "true" || v == "1" }

// toCalendarDTO maps the domain report to the wire DTO, resolving each event's
// raw TMDB poster path → sha256 media hash via the injected resolver (nil-OK:
// raw path flows through unchanged). Mirrors MovieCalendarHandler.Get.
func (h *CalendarHandler) toCalendarDTO(ctx context.Context, rep calendar.Report) dto.CalendarDTO {
	days := make([]dto.CalendarDayDTO, 0, len(rep.Days))
	for _, d := range rep.Days {
		events := make([]dto.CalendarEventDTO, 0, len(d.Events))
		for _, e := range d.Events {
			poster := e.Poster
			if h.resolver != nil {
				if hash := h.resolver.Resolve(ctx, e.Poster, "w342", "poster_w342"); hash != nil {
					poster = hash
				}
			}
			events = append(events, dto.CalendarEventDTO{
				SeriesID:           e.SeriesID,
				TMDBID:             e.TMDBID,
				Title:              e.Title,
				Season:             e.Season,
				Episode:            e.Episode,
				AirDate:            e.AirDate,
				State:              e.State,
				InLibraryInstances: e.InLibraryInstances,
				Poster:             poster,
				SeasonPremiere:     e.SeasonPremiere,
				Milestone:          e.Milestone,
				MediaType:          e.MediaType,
			})
		}
		days = append(days, dto.CalendarDayDTO{Date: d.Date, Events: events})
	}
	return dto.CalendarDTO{GeneratedAt: rep.GeneratedAt, From: rep.From, To: rep.To, Days: days}
}
