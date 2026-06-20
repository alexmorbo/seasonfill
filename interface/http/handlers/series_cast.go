package handlers

import (
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/alexmorbo/seasonfill/interface/http/dto"
	seriesdetail "github.com/alexmorbo/seasonfill/internal/seriesdetail/app"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
)

// SeriesCastHandler serves the full cast & crew payload for the
// H-1 Cast & Crew page (Story 216 / PRD §5.7).
//
// GET /api/v1/instances/:name/series/:id/cast?lang=
type SeriesCastHandler struct {
	composer *seriesdetail.CastComposer
	logger   *slog.Logger
}

func NewSeriesCastHandler(composer *seriesdetail.CastComposer, logger *slog.Logger) *SeriesCastHandler {
	if logger == nil {
		logger = slog.Default()
	}
	return &SeriesCastHandler{composer: composer, logger: logger}
}

// Get handles GET /api/v1/instances/:name/series/:id/cast.
//
// @Summary     Full series cast & crew
// @Description Returns the complete cast and crew for one series —
// @Description cast sorted by TMDB billing order, crew grouped by
// @Description department then person name. Each row carries the
// @Description per-person `episode_count` (from TMDB
// @Description aggregate_credits[*].total_episode_count) and an
// @Description `in_library` flag derived from local
// @Description `person_credits` intersected with active
// @Description `series_cache` rows (excluding the current series so
// @Description the "what else are they in?" affordance never
// @Description renders a self-link).
// @Description
// @Description `total_episode_count` is the series-level divisor
// @Description the frontend uses to derive Main / Recurring /
// @Description Guest badges from `episode_count /
// @Description total_episode_count` (design-handoff Q3).
// @Description
// @Description `series_summary` carries the lightweight title +
// @Description poster + status + year-range block the cast page
// @Description hero renders — keeps the page to a single API call
// @Description (story 303).
// @Tags        instances
// @Produce     json
// @Param       name  path      string  true   "Instance name"
// @Param       id    path      int     true   "Sonarr series id (per-instance)"
// @Param       lang  query     string  false  "BCP-47 language tag (default en-US, reserved for H-2 parity — cast list has no per-language fields in v1)"
// @Success     200   {object}  dto.SeriesCastResponse
// @Failure     400   {object}  dto.ErrorResponse
// @Failure     401   {object}  dto.ErrorResponse
// @Failure     404   {object}  dto.ErrorResponse
// @Failure     500   {object}  dto.ErrorResponse
// @Security    CookieAuth
// @Security    ApiKeyAuth
// @Router      /instances/{name}/series/{id}/cast [get]
func (h *SeriesCastHandler) Get(c *gin.Context) {
	name := c.Param("name")
	idStr := c.Param("id")
	parsedID, err := strconv.Atoi(idStr)
	if err != nil || parsedID <= 0 {
		c.JSON(http.StatusBadRequest, dto.ErrorResponse{Error: "invalid series id"})
		return
	}
	sonarrID := domain.SonarrSeriesID(parsedID)
	lang := strings.TrimSpace(c.Query("lang"))

	ctx := c.Request.Context()
	detail, err := h.composer.Get(ctx, domain.InstanceName(name), sonarrID, lang)
	if err != nil {
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusOK, toSeriesCastResponse(detail))
}

// toSeriesCastResponse projects the composer's domain object onto
// the wire DTO. No DB / network calls here — pure mapping.
func toSeriesCastResponse(d *seriesdetail.CastPage) dto.SeriesCastResponse {
	resp := dto.SeriesCastResponse{
		Instance:       d.Instance,
		SonarrSeriesID: d.SonarrSeriesID,
		SeriesID:       d.SeriesID,
		Lang:           d.Lang,
		SeriesSummary: dto.SeriesSummary{
			Title:          d.Summary.Title,
			PosterURL:      d.Summary.PosterAsset,
			Status:         d.Summary.Status,
			FirstAiredYear: d.Summary.FirstAiredYear,
			LastAiredYear:  d.Summary.LastAiredYear,
		},
		TotalEpisodeCount: d.TotalEpisodeCount,
		Cast:              make([]dto.CastPageMember, 0, len(d.Cast)),
		Crew:              make([]dto.CrewPageMember, 0, len(d.Crew)),
		SyncedAt:          d.SyncedAt.Format("2006-01-02T15:04:05Z07:00"),
	}
	for _, e := range d.Cast {
		resp.Cast = append(resp.Cast, dto.CastPageMember{
			PersonID:      e.Person.ID,
			TMDBID:        e.Person.TMDBID,
			Name:          e.Person.Name,
			ProfileAsset:  e.Person.ProfileAsset,
			CharacterName: e.Credit.CharacterName,
			CreditOrder:   e.Credit.CreditOrder,
			EpisodeCount:  e.Credit.EpisodeCount,
			InLibrary:     e.InLibrary,
		})
	}
	for _, e := range d.Crew {
		resp.Crew = append(resp.Crew, dto.CrewPageMember{
			PersonID:     e.Person.ID,
			TMDBID:       e.Person.TMDBID,
			Name:         e.Person.Name,
			ProfileAsset: e.Person.ProfileAsset,
			Department:   e.Credit.Department,
			Job:          e.Credit.Job,
			EpisodeCount: e.Credit.EpisodeCount,
			InLibrary:    e.InLibrary,
		})
	}
	return resp
}
