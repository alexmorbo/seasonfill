package rest

import (
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	seriesdetail "github.com/alexmorbo/seasonfill/internal/seriesdetail/app"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
	"github.com/alexmorbo/seasonfill/internal/shared/http/dto"
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

// parseCastLimit reads the optional ?limit=N query param shared by the
// /series/:id/cast handlers. Absent, non-numeric, or <=0 ⇒ 0 (unlimited =
// full cast page). MUST stay in lockstep with the ETag middleware's own
// limit parse (internal/shared/http/edge/etag.go) — the two packages cannot
// import each other (edge -> seriesdetailrest cycle), so the parse is
// duplicated intentionally. Story 1087a.
func parseCastLimit(c *gin.Context) int {
	n, err := strconv.Atoi(strings.TrimSpace(c.Query("limit")))
	if err != nil || n <= 0 {
		return 0
	}
	return n
}

// DEAD: per-instance route deleted at N-1b cutover (story 492). Function retained for future cleanup sweep.
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
	limit := parseCastLimit(c) // Story 1087a — optional cast cap; 0 = full page.

	ctx := c.Request.Context()
	detail, err := h.composer.Get(ctx, domain.InstanceName(name), sonarrID, lang, limit)
	if err != nil {
		_ = c.Error(err)
		return
	}
	resp := toSeriesCastResponse(detail)
	// Story 1087b — server-side cast sort (?sort=episodes|credit|name; default
	// episodes). resp.Lang is the resolved BCP-47 tag used for name collation.
	sortCastMembers(resp.Cast, parseCastSort(c), resp.Lang)
	c.JSON(http.StatusOK, resp)
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
		ServedLanguage:    d.ServedLanguage,
		Degraded:          seriesdetail.AppendMissingLang([]string{}, d.ServedLanguage, d.Lang),
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
