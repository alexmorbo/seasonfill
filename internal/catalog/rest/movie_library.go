package rest

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	ports "github.com/alexmorbo/seasonfill/internal/shared/dataports"
	"github.com/alexmorbo/seasonfill/internal/shared/http/dto"
	"github.com/alexmorbo/seasonfill/internal/shared/media"
)

const (
	movieLibraryDefaultLimit = 24
	movieLibraryMaxLimit     = 100
	movieLibraryMaxSearchLen = 200
)

var (
	errMovieState     = errors.New("state must be one of: all, downloaded, missing")
	errMovieSort      = errors.New("sort must be one of: updated_desc, title_asc, release_desc")
	errMovieSearchLen = errors.New("q must be at most 200 characters")
	errMovieLimit     = errors.New("limit must be between 1 and 100")
	errMovieCursor    = errors.New("cursor must be a non-negative integer")
)

// MovieTitleLocalizer batch-loads localized movie titles by the never-empty
// ladder (requested → en-US → any). Production impl:
// *enrichpersistence.MovieI18nReadRepository.ListTitlesByTMDBIDsWithFallback.
// nil-OK: an unwired localizer or a blank ?lang keeps canon titles
// (pre-M-FIX-2 behavior).
type MovieTitleLocalizer interface {
	ListTitlesByTMDBIDsWithFallback(ctx context.Context, tmdbIDs []int, lang string) (map[int]string, error)
}

// MovieLibraryHandler serves GET /api/v1/movies — the global movie library
// list (Ф6-R-6b), the movie analog of the series library list.
type MovieLibraryHandler struct {
	repo      ports.MovieLibraryRepository
	resolver  *media.Resolver     // nil-OK: raw TMDB paths flow through unchanged
	localizer MovieTitleLocalizer // nil-OK: canon titles kept when unwired
	logger    *slog.Logger
}

// NewMovieLibraryHandler constructs the handler. repo must be non-nil in
// production; the route is omitted (nil-OK) when the wirer passes nil. resolver
// rewrites raw canon poster_asset paths to sha256 media hashes (nil-OK → raw
// paths flow through, pre-M-FIX-1 behavior).
func NewMovieLibraryHandler(repo ports.MovieLibraryRepository, resolver *media.Resolver, logger *slog.Logger) *MovieLibraryHandler {
	if logger == nil {
		logger = slog.Default()
	}
	return &MovieLibraryHandler{repo: repo, resolver: resolver, logger: logger}
}

// WithLocalizer wires the ?lang title localizer. nil-OK — unwired keeps canon
// titles. Returns the handler for chaining.
func (h *MovieLibraryHandler) WithLocalizer(l MovieTitleLocalizer) *MovieLibraryHandler {
	h.localizer = l
	return h
}

// List returns the deduplicated movie library page.
//
// @Summary     List movie library
// @Description Global movie library backed by movie_states (radarr membership)
// @Description joined to the movies canon. One item per movie (deduplicated by
// @Description tmdb id; instance memberships aggregated). Filter by state
// @Description (all|downloaded|missing), sort (updated_desc|title_asc|
// @Description release_desc), title search (q), and offset/limit pagination.
// @Tags        movies
// @Produce     json
// @Param       state  query     string false "all|downloaded|missing" Enums(all,downloaded,missing)
// @Param       sort   query     string false "updated_desc|title_asc|release_desc" Enums(updated_desc,title_asc,release_desc)
// @Param       q      query     string false "case-insensitive title substring"
// @Param       limit  query     int    false "page size (1-100, default 24)"
// @Param       cursor query     string false "opaque offset cursor from a prior next_cursor"
// @Param       lang   query     string false "BCP-47 UI language for localized titles (e.g. ru-RU)"
// @Success     200    {object}  dto.MovieLibraryList
// @Failure     400    {object}  dto.ErrorResponse
// @Failure     401    {object}  dto.ErrorResponse
// @Failure     500    {object}  dto.ErrorResponse
// @Security    CookieAuth
// @Security    ApiKeyAuth
// @Router      /movies [get]
func (h *MovieLibraryHandler) List(c *gin.Context) {
	state, err := parseMovieLibraryState(c.Query("state"))
	if err != nil {
		c.JSON(http.StatusBadRequest, dto.ErrorResponse{Error: err.Error()})
		return
	}
	sort, err := parseMovieLibrarySort(c.Query("sort"))
	if err != nil {
		c.JSON(http.StatusBadRequest, dto.ErrorResponse{Error: err.Error()})
		return
	}
	q, err := parseMovieLibrarySearch(c.Query("q"))
	if err != nil {
		c.JSON(http.StatusBadRequest, dto.ErrorResponse{Error: err.Error()})
		return
	}
	limit, err := parseMovieLibraryLimit(c.Query("limit"))
	if err != nil {
		c.JSON(http.StatusBadRequest, dto.ErrorResponse{Error: err.Error()})
		return
	}
	offset, err := parseMovieLibraryCursor(c.Query("cursor"))
	if err != nil {
		c.JSON(http.StatusBadRequest, dto.ErrorResponse{Error: err.Error()})
		return
	}

	ctx := c.Request.Context()
	rows, total, err := h.repo.List(ctx, ports.MovieLibraryFilter{State: state, Search: q}, sort, limit, offset)
	if err != nil {
		h.logger.ErrorContext(ctx, "movie_library_list_failed",
			slog.String("state", string(state)),
			slog.String("sort", string(sort)),
			slog.String("error", err.Error()))
		c.JSON(http.StatusInternalServerError, dto.ErrorResponse{Error: "list failed"})
		return
	}

	items := make([]dto.MovieLibraryItem, 0, len(rows))
	for _, r := range rows {
		insts := r.Instances
		if insts == nil {
			insts = []string{}
		}
		poster := r.PosterAsset
		if h.resolver != nil {
			if hash := h.resolver.Resolve(ctx, r.PosterAsset, "w342", "poster_w342"); hash != nil {
				poster = hash
			}
		}
		items = append(items, dto.MovieLibraryItem{
			TMDBID:      r.TMDBID,
			Title:       r.Title,
			Year:        r.Year,
			Poster:      poster,
			Status:      r.Status,
			ReleaseDate: r.ReleaseDate,
			TMDBRating:  r.TMDBRating,
			IMDBRating:  r.IMDBRating,
			Monitored:   r.Monitored,
			HasFile:     r.HasFile,
			Instances:   insts,
			SizeOnDisk:  r.SizeOnDisk,
			UpdatedAt:   r.UpdatedAt,
		})
	}

	h.localizeMovieTitles(ctx, c.Query("lang"), items)

	hasMore := offset+len(rows) < total
	var nextCursor string
	if hasMore {
		nextCursor = strconv.Itoa(offset + limit)
	}
	c.JSON(http.StatusOK, dto.MovieLibraryList{
		Items:      items,
		Total:      total,
		HasMore:    hasMore,
		NextCursor: nextCursor,
	})
}

// localizeMovieTitles overrides item titles with the caller's requested language
// in a single batch lookup. No-op (zero DB calls) when the localizer is unwired,
// lang is blank, or the page is empty — preserving canon behavior. Items with a
// map miss keep their canon title. Mirrors localizeSeriesCacheTitles.
func (h *MovieLibraryHandler) localizeMovieTitles(ctx context.Context, lang string, items []dto.MovieLibraryItem) {
	if h.localizer == nil || strings.TrimSpace(lang) == "" || len(items) == 0 {
		return
	}
	ids := make([]int, 0, len(items))
	seen := make(map[int]struct{}, len(items))
	for _, it := range items {
		if it.TMDBID == 0 {
			continue
		}
		if _, ok := seen[it.TMDBID]; ok {
			continue
		}
		seen[it.TMDBID] = struct{}{}
		ids = append(ids, it.TMDBID)
	}
	if len(ids) == 0 {
		return
	}
	titles, err := h.localizer.ListTitlesByTMDBIDsWithFallback(ctx, ids, lang)
	if err != nil {
		h.logger.WarnContext(ctx, "movie_library_localize_failed",
			slog.String("lang", lang), slog.String("error", err.Error()))
		return
	}
	for i := range items {
		if t, ok := titles[items[i].TMDBID]; ok {
			items[i].Title = t
		}
	}
}

func parseMovieLibraryState(raw string) (ports.MovieLibraryState, error) {
	raw = strings.ToLower(strings.TrimSpace(raw))
	if raw == "" {
		return ports.MovieLibraryStateAll, nil
	}
	s := ports.MovieLibraryState(raw)
	if !s.IsValid() {
		return "", errMovieState
	}
	return s, nil
}

func parseMovieLibrarySort(raw string) (ports.MovieLibrarySort, error) {
	raw = strings.ToLower(strings.TrimSpace(raw))
	if raw == "" {
		return ports.MovieLibrarySortUpdatedDesc, nil
	}
	s := ports.MovieLibrarySort(raw)
	if !s.IsValid() {
		return "", errMovieSort
	}
	return s, nil
}

func parseMovieLibrarySearch(raw string) (string, error) {
	q := strings.TrimSpace(raw)
	if q == "" {
		return "", nil
	}
	if len(q) > movieLibraryMaxSearchLen {
		return "", errMovieSearchLen
	}
	return q, nil
}

func parseMovieLibraryLimit(raw string) (int, error) {
	if strings.TrimSpace(raw) == "" {
		return movieLibraryDefaultLimit, nil
	}
	n, err := strconv.Atoi(raw)
	if err != nil {
		return 0, errMovieLimit
	}
	if n < 1 || n > movieLibraryMaxLimit {
		return 0, errMovieLimit
	}
	return n, nil
}

func parseMovieLibraryCursor(raw string) (int, error) {
	if strings.TrimSpace(raw) == "" {
		return 0, nil
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 0 {
		return 0, errMovieCursor
	}
	return n, nil
}
