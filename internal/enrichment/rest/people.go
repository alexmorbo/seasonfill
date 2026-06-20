package rest

import (
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/alexmorbo/seasonfill/interface/http/dto"
	apppeople "github.com/alexmorbo/seasonfill/internal/enrichment/app/people"
	domenrich "github.com/alexmorbo/seasonfill/internal/enrichment/domain/enrichment"
	dompeople "github.com/alexmorbo/seasonfill/internal/enrichment/domain/people"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
)

// PeopleHandler serves the H-2 person detail payload (Story 217 /
// PRD §5.7 row 1).
//
// GET /api/v1/people/:tmdbId?lang=&sort=recent|episodes|title
type PeopleHandler struct {
	uc     *apppeople.UseCase
	logger *slog.Logger
}

func NewPeopleHandler(uc *apppeople.UseCase, logger *slog.Logger) *PeopleHandler {
	if logger == nil {
		logger = slog.Default()
	}
	return &PeopleHandler{uc: uc, logger: logger}
}

// Get handles GET /api/v1/people/:tmdbId.
//
// @Summary     Person detail page
// @Description Returns the canonical person row, biography
// @Description (resolved via the `(person_id, language)` fallback
// @Description helper), and the per-person filmography
// @Description partitioned into `library_credits[]` (rows whose
// @Description `tmdb_media_id` resolves to a canon `series` row
// @Description with at least one live `series_cache` reference)
// @Description and `other_credits[]` (TMDB-only — movies, TMDB
// @Description titles we don't carry, canon stubs without
// @Description library references).
// @Description
// @Description `sort` controls `library_credits[]` ordering: `recent`
// @Description (default, `series.last_aired_at DESC`), `episodes`
// @Description (`person_credits.episode_count DESC`), `title`
// @Description (`series.title ASC`).
// @Description
// @Description Stub-person handling: when `people.hydration='stub'`
// @Description the endpoint enqueues a `PriorityHot` enrichment
// @Description job AND returns 200 with `degraded: ["tmdb_person"]`.
// @Description The frontend re-polls; this story does NOT block
// @Description for hydration.
// @Tags        people
// @Produce     json
// @Param       tmdbId  path      int     true   "TMDB person id"
// @Param       lang    query     string  false  "BCP-47 language tag (default en-US)"
// @Param       sort    query     string  false  "library_credits sort: recent|episodes|title (default recent)"
// @Success     200     {object}  dto.PersonDetailResponse
// @Failure     400     {object}  dto.ErrorResponse
// @Failure     401     {object}  dto.ErrorResponse
// @Failure     404     {object}  dto.ErrorResponse
// @Failure     500     {object}  dto.ErrorResponse
// @Security    CookieAuth
// @Security    ApiKeyAuth
// @Router      /people/{tmdbId} [get]
func (h *PeopleHandler) Get(c *gin.Context) {
	idStr := c.Param("tmdbId")
	tmdbID, err := strconv.Atoi(idStr)
	if err != nil || tmdbID <= 0 {
		c.JSON(http.StatusBadRequest, dto.ErrorResponse{Error: "invalid tmdb id"})
		return
	}
	lang := strings.TrimSpace(c.Query("lang"))
	sortKey := strings.TrimSpace(c.Query("sort"))

	ctx := c.Request.Context()
	detail, err := h.uc.Get(ctx, domain.TMDBID(tmdbID), lang, sortKey)
	if err != nil {
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusOK, toPersonDetailResponse(detail))
}

// toPersonDetailResponse projects the use case domain object onto
// the wire DTO. No DB / network calls — pure mapping.
func toPersonDetailResponse(d *apppeople.PersonDetail) dto.PersonDetailResponse {
	resp := dto.PersonDetailResponse{
		Person:         mapPersonInfo(d.Person),
		Biography:      d.Biography,
		BioLanguage:    d.BioLanguage,
		Sync:           mapSyncInfo(d.Sync),
		LibraryCredits: make([]dto.LibraryCreditEntry, 0, len(d.LibraryCredits)),
		OtherCredits:   make([]dto.OtherCreditEntry, 0, len(d.OtherCredits)),
		Degraded:       sourceStringSlice(d.Degraded),
	}
	for _, lc := range d.LibraryCredits {
		resp.LibraryCredits = append(resp.LibraryCredits, mapLibraryCredit(lc))
	}
	for _, oc := range d.OtherCredits {
		resp.OtherCredits = append(resp.OtherCredits, mapOtherCredit(oc))
	}
	return resp
}

func mapPersonInfo(p dompeople.Person) dto.PersonInfo {
	return dto.PersonInfo{
		ID:                 p.ID,
		TMDBID:             p.TMDBID,
		Name:               p.Name,
		OriginalName:       p.OriginalName,
		Birthday:           dateString(p.Birthday),
		Deathday:           dateString(p.Deathday),
		PlaceOfBirth:       p.PlaceOfBirth,
		KnownForDepartment: p.KnownForDepartment,
		ProfileAsset:       p.ProfileAsset,
		Popularity:         p.Popularity,
	}
}

func mapSyncInfo(log *domenrich.SyncLog) *dto.SyncInfo {
	if log == nil || log.SyncedAt == nil {
		return nil
	}
	return &dto.SyncInfo{
		Source:   string(log.Source),
		SyncedAt: log.SyncedAt.UTC().Format("2006-01-02T15:04:05Z07:00"),
	}
}

func mapLibraryCredit(lc apppeople.LibraryCredit) dto.LibraryCreditEntry {
	instances := make([]dto.LibraryCreditInstance, 0, len(lc.Instances))
	for _, inst := range lc.Instances {
		instances = append(instances, dto.LibraryCreditInstance{
			Instance:       inst.InstanceName,
			SonarrSeriesID: inst.SonarrSeriesID,
		})
	}
	return dto.LibraryCreditEntry{
		SeriesID:      lc.Canon.ID,
		TMDBID:        lc.Canon.TMDBID,
		Title:         lc.Canon.Title,
		Year:          lc.Canon.Year,
		CharacterName: lc.Credit.CharacterName,
		EpisodeCount:  lc.Credit.EpisodeCount,
		Kind:          string(lc.Credit.Kind),
		RoleLabel:     deriveRoleLabel(lc.Credit),
		PosterAsset:   lc.Canon.PosterAsset,
		Instances:     instances,
	}
}

func mapOtherCredit(oc apppeople.OtherCredit) dto.OtherCreditEntry {
	return dto.OtherCreditEntry{
		TMDBMediaID:   int(oc.Credit.TMDBMediaID),
		MediaType:     oc.Credit.MediaType,
		Title:         oc.Credit.Title,
		OriginalTitle: oc.Credit.OriginalTitle,
		Year:          yearFromReleaseDate(oc.Credit.ReleaseDate),
		CharacterName: oc.Credit.CharacterName,
		Kind:          string(oc.Credit.Kind),
		Department:    oc.Credit.Department,
		RoleLabel:     deriveRoleLabel(oc.Credit),
		PosterAsset:   oc.Credit.PosterAsset,
		VoteAverage:   oc.Credit.TMDBRating,
		VoteCount:     oc.Credit.TMDBVotes,
	}
}

// deriveRoleLabel picks the display label per kind:
//
//	kind=cast → character_name (empty when nil)
//	kind=crew → job (empty when nil)
func deriveRoleLabel(pc dompeople.PersonCredit) string {
	switch pc.Kind {
	case dompeople.SeriesCreditCast:
		if pc.CharacterName != nil {
			return *pc.CharacterName
		}
	case dompeople.SeriesCreditCrew:
		if pc.Job != nil {
			return *pc.Job
		}
	}
	return ""
}

// dateString formats a *time.Time as ISO 8601 date-only (UTC). nil
// passes through.
func dateString(t *time.Time) *string {
	if t == nil {
		return nil
	}
	s := t.UTC().Format("2006-01-02")
	return &s
}

// yearFromReleaseDate extracts the year from a *time.Time release
// date. nil → nil.
func yearFromReleaseDate(t *time.Time) *int {
	if t == nil {
		return nil
	}
	y := t.UTC().Year()
	return &y
}
