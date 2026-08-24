package rest

import (
	"context"
	"log/slog"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	searchapp "github.com/alexmorbo/seasonfill/internal/search/app"
	searchdomain "github.com/alexmorbo/seasonfill/internal/search/domain"
	shareddomain "github.com/alexmorbo/seasonfill/internal/shared/domain"
	"github.com/alexmorbo/seasonfill/internal/shared/http/dto"
)

// defaultLang is the BCP-47 tag used when the client omits ?lang=. Mirrors
// internal/discovery/rest/handlers.go defaultLang + the worker's en-US default.
const defaultLang = "en-US"

// maxQueryLen / maxLimit bound the request. maxQueryLen mirrors the discovery
// guarded search (1..100 after trim). maxLimit caps limitPerGroup so a caller
// cannot demand an unbounded scan; 0 (unset) falls through to the use case's
// own 20/group default.
const (
	maxQueryLen = 100
	maxLimit    = 50
)

// scope enum (D10).
const (
	scopeLibrary = "library"
	scopeCatalog = "catalog"
	scopeAll     = "all"
)

// type-filter tokens (D10 `types=` CSV).
const (
	typeSeries     = "series"
	typeMovie      = "movie"
	typeCollection = "collection"
	typePerson     = "person"
)

// bcp47Re mirrors internal/discovery/rest/handlers.go bcp47Re (which in turn
// mirrors internal/shared/ports/validator.go:25) — a 2-3 letter language plus
// an optional 2-4 letter region/script subtag.
var bcp47Re = regexp.MustCompile(`^[a-zA-Z]{2,3}(-[a-zA-Z]{2,4})?$`)

// LibrarySearcher is the narrow port the handler reads. Satisfied by the
// concrete *app.UnifiedSearchUseCase. Declared here (not in app) so the
// handler stays unit-testable with a hand-rolled fake — the use case is a
// concrete type, not an interface, so the seam lives at the rest boundary
// (clean-arch: the interface belongs to the consumer).
type LibrarySearcher interface {
	Search(ctx context.Context, q, language string, limitPerGroup int, scope searchapp.Scope, types searchapp.TypeFilter) (searchdomain.LibrarySearchResult, error)
}

// SearchHandler serves GET /api/v1/search. Construct via NewSearchHandler
// (called from wiring/search.go).
type SearchHandler struct {
	search LibrarySearcher
	log    *slog.Logger
}

// NewSearchHandler wires the handler. search + log are required — panics on a
// nil dependency so a wiring bug surfaces at boot rather than at first request
// (mirrors NewDiscoveryHandler / NewUnifiedSearchUseCase).
func NewSearchHandler(search LibrarySearcher, log *slog.Logger) *SearchHandler {
	switch {
	case search == nil:
		panic("search handler: library searcher required")
	case log == nil:
		panic("search handler: log required")
	}
	return &SearchHandler{search: search, log: log}
}

// Search serves GET /api/v1/search.
//
// @Summary     Unified hybrid search
// @Description Grouped hybrid search across series, movies, collections and
// @Description people (ADR-0024). Returns four arrays keyed by entity; empty
// @Description groups serialize as []. scope selects library|catalog|all:
// @Description library returns local hits only, catalog returns TMDB fan-out
// @Description hits, and all merges the two (library first, deduped by tmdb_id).
// @Description types is an optional CSV subset of series,movie,collection,person
// @Description that filters which groups are populated. Each hit carries
// @Description source="library" or source="catalog".
// @Tags        search
// @Produce     json
// @Param       q      query     string true  "Search query (1..100 chars after trim)"
// @Param       scope  query     string false "library | catalog | all (default all)"
// @Param       types  query     string false "CSV subset of series,movie,collection,person (default all)"
// @Param       lang   query     string false "BCP-47 language tag (default en-US)"
// @Param       limit  query     int    false "Max hits per group (1..50; default 20)"
// @Success     200    {object}  rest.SearchResponse
// @Failure     400    {object}  dto.ErrorResponse
// @Failure     401    {object}  dto.ErrorResponse
// @Failure     500    {object}  dto.ErrorResponse
// @Security    CookieAuth
// @Security    ApiKeyAuth
// @Router      /search [get]
func (h *SearchHandler) Search(c *gin.Context) {
	q := strings.TrimSpace(c.Query("q"))
	if q == "" || len(q) > maxQueryLen {
		c.JSON(http.StatusBadRequest, dto.ErrorResponse{
			Error: "q must be 1..100 characters after trim", Code: "INVALID_QUERY"})
		return
	}

	scope := c.DefaultQuery("scope", scopeAll)
	switch scope {
	case scopeLibrary, scopeCatalog, scopeAll:
	default:
		c.JSON(http.StatusBadRequest, dto.ErrorResponse{
			Error: "scope must be library, catalog, or all", Code: "INVALID_SCOPE"})
		return
	}

	lang := c.DefaultQuery("lang", defaultLang)
	if !validateLang(lang) {
		c.JSON(http.StatusBadRequest, dto.ErrorResponse{
			Error: "lang must be a BCP-47 tag", Code: "INVALID_LANGUAGE"})
		return
	}

	types, ok := parseTypes(c.Query("types"))
	if !ok {
		c.JSON(http.StatusBadRequest, dto.ErrorResponse{
			Error: "types must be a CSV subset of series,movie,collection,person",
			Code:  "INVALID_TYPES"})
		return
	}

	limit, ok := parseLimit(c.Query("limit"))
	if !ok {
		c.JSON(http.StatusBadRequest, dto.ErrorResponse{
			Error: "limit must be a positive integer", Code: "INVALID_LIMIT"})
		return
	}

	res, err := h.search.Search(c.Request.Context(), q, lang, limit, appScope(scope), appTypes(types))
	if err != nil {
		h.log.WarnContext(c.Request.Context(), "search.failed",
			slog.String("query", q),
			slog.String("scope", scope),
			slog.String("language", lang),
			slog.String("error", err.Error()))
		c.JSON(http.StatusInternalServerError, dto.ErrorResponse{
			Error: "search failed", Code: "SEARCH_READ_FAILED"})
		return
	}

	c.JSON(http.StatusOK, buildResponse(q, scope, types, res))
}

// validateLang gates the BCP-47 subset (mirrors discovery). Empty is rejected;
// callers default to en-US BEFORE calling.
func validateLang(s string) bool {
	if s == "" {
		return false
	}
	return bcp47Re.MatchString(s)
}

// typeSet records which entity groups the caller wants populated.
type typeSet struct {
	series     bool
	movie      bool
	collection bool
	person     bool
}

// list returns the enabled tokens in a stable order, for echoing in the
// response envelope.
func (t typeSet) list() []string {
	out := make([]string, 0, 4)
	if t.series {
		out = append(out, typeSeries)
	}
	if t.movie {
		out = append(out, typeMovie)
	}
	if t.collection {
		out = append(out, typeCollection)
	}
	if t.person {
		out = append(out, typePerson)
	}
	sort.Strings(out)
	return out
}

// parseTypes parses the optional CSV types filter. Empty input selects all
// four groups. An unknown token is rejected (ok=false → 400) — mirrors the
// strict enum validation in movie_discover_handler.parse (sort_by → 400).
func parseTypes(raw string) (typeSet, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return typeSet{series: true, movie: true, collection: true, person: true}, true
	}
	var ts typeSet
	for tok := range strings.SplitSeq(raw, ",") {
		switch strings.TrimSpace(tok) {
		case "":
			continue
		case typeSeries:
			ts.series = true
		case typeMovie:
			ts.movie = true
		case typeCollection:
			ts.collection = true
		case typePerson:
			ts.person = true
		default:
			return typeSet{}, false
		}
	}
	// A CSV of only empty tokens (e.g. ",,") selects nothing valid — treat as
	// bad input rather than an all-empty response.
	if !ts.series && !ts.movie && !ts.collection && !ts.person {
		return typeSet{}, false
	}
	return ts, true
}

// appScope maps the validated scope string → app.Scope.
func appScope(scope string) searchapp.Scope {
	switch scope {
	case scopeCatalog:
		return searchapp.ScopeCatalog
	case scopeAll:
		return searchapp.ScopeAll
	default:
		return searchapp.ScopeLibrary
	}
}

// appTypes maps the parsed typeSet → app.TypeFilter (no rest types leak into app).
func appTypes(ts typeSet) searchapp.TypeFilter {
	return searchapp.TypeFilter{
		Series: ts.series, Movie: ts.movie, Collection: ts.collection, Person: ts.person,
	}
}

// parseLimit parses the optional per-group cap. Empty → 0 (the use case
// applies its own 20/group default). A value above maxLimit is clamped; a
// non-integer or <1 value is rejected (ok=false → 400).
func parseLimit(raw string) (int, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0, true
	}
	v, err := strconv.Atoi(raw)
	if err != nil || v < 1 {
		return 0, false
	}
	if v > maxLimit {
		v = maxLimit
	}
	return v, true
}

// buildResponse maps the grouped domain result into the wire envelope. Every
// group is initialized to a non-nil empty slice so an empty or type-excluded
// group serializes as [] (never null) — the FE consumes arrays.
func buildResponse(q, scope string, ts typeSet, res searchdomain.LibrarySearchResult) SearchResponse {
	resp := SearchResponse{
		Query:       q,
		Scope:       scope,
		Types:       ts.list(),
		Series:      []SearchSeriesItem{},
		Movies:      []SearchMovieItem{},
		Collections: []SearchCollectionItem{},
		People:      []SearchPersonItem{},
	}
	if ts.series {
		for _, hit := range res.Series {
			resp.Series = append(resp.Series, seriesItem(hit))
		}
	}
	if ts.movie {
		for _, hit := range res.Movies {
			resp.Movies = append(resp.Movies, movieItem(hit))
		}
	}
	if ts.collection {
		for _, hit := range res.Collections {
			resp.Collections = append(resp.Collections, collectionItem(hit))
		}
	}
	if ts.person {
		for _, hit := range res.People {
			resp.People = append(resp.People, personItem(hit))
		}
	}
	return resp
}

func seriesItem(h searchdomain.SeriesHit) SearchSeriesItem {
	return SearchSeriesItem{
		ID:           int64(h.SeriesID),
		TMDBID:       tmdbPtr(h.TMDBID),
		Title:        h.Title,
		Year:         intPtr(h.Year),
		PosterPath:   strPtr(h.PosterPath),
		BackdropPath: strPtr(h.BackdropPath),
		Source:       wireSource(h.Source),
	}
}

func movieItem(h searchdomain.MovieHit) SearchMovieItem {
	return SearchMovieItem{
		ID:           int64(h.MovieID),
		TMDBID:       tmdbPtr(h.TMDBID),
		Title:        h.Title,
		Year:         intPtr(h.Year),
		PosterPath:   strPtr(h.PosterPath),
		BackdropPath: strPtr(h.BackdropPath),
		Source:       wireSource(h.Source),
	}
}

func collectionItem(h searchdomain.CollectionHit) SearchCollectionItem {
	return SearchCollectionItem{
		ID:           int64(h.CollectionID),
		TMDBID:       tmdbPtr(h.TMDBID),
		Name:         h.Name,
		PosterPath:   strPtr(h.PosterPath),
		BackdropPath: strPtr(h.BackdropPath),
		Source:       wireSource(h.Source),
	}
}

func personItem(h searchdomain.PersonHit) SearchPersonItem {
	return SearchPersonItem{
		ID:          int64(h.PersonID),
		TMDBID:      tmdbPtr(h.TMDBID),
		Name:        h.Name,
		ProfilePath: strPtr(h.ProfilePath),
		KnownFor:    strPtr(h.KnownFor),
		Source:      wireSource(h.Source),
	}
}

// tmdbPtr converts the typed *shareddomain.TMDBID to a *int64 wire pointer,
// copying the value so the wire struct never aliases the domain hit.
func tmdbPtr(t *shareddomain.TMDBID) *int64 {
	if t == nil {
		return nil
	}
	v := int64(*t)
	return &v
}

// intPtr / strPtr copy the pointee into a fresh pointer (never alias the
// domain hit's storage).
func intPtr(p *int) *int {
	if p == nil {
		return nil
	}
	v := *p
	return &v
}

func strPtr(p *string) *string {
	if p == nil {
		return nil
	}
	v := *p
	return &v
}
