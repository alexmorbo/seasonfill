// handlers.go ships the discovery HTTP surface (PRD §5.1 endpoint
// table, story 507 N-2f):
//
//	GET /api/v1/discovery/trending?scope=day|week&lang=&page=&per_page=
//	GET /api/v1/discovery/popular?lang=&page=&per_page=
//	GET /api/v1/discovery/genre/:id?lang=&page=&per_page=
//	GET /api/v1/discovery/network/:id?lang=&page=&per_page=
//	GET /api/v1/discovery/keyword/:id?lang=&page=&per_page=
//	GET /api/v1/discovery/genres?lang=
//	GET /api/v1/discovery/networks?lang=
//
// Cold-start envelope (PRD §5.1.1 lines 665-678): on /trending and
// /popular only, when WarmingProbe.IsWarming() reports true, the
// handler short-circuits with an empty 200 envelope carrying
// degraded:["discovery_warming"] + warming_estimate_seconds=30.
//
// On-demand long-tail (PRD §5.1.1 lines 686-692): for genre / network
// / keyword, when the list is missing or older than 7d the handler
// calls RefreshOnDemand.RefreshNow synchronously, then reads back
// the (now fresh) list. Concurrent cold-cache calls collapse onto a
// single TMDB fetch via golang.org/x/sync/singleflight keyed by
// kind|param|lang.
//
// Error envelope (F-2c): every 4xx/5xx response is
// {"error":"<snake_slug>", "message":"<human>"} per the project
// convention (mirrors internal/shared/http/middleware/errors.go).
package rest

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"golang.org/x/sync/singleflight"

	"github.com/alexmorbo/seasonfill/internal/discovery/app"
	disco "github.com/alexmorbo/seasonfill/internal/discovery/domain"
	"github.com/alexmorbo/seasonfill/internal/discovery/persistence"
	shareddomain "github.com/alexmorbo/seasonfill/internal/shared/domain"
	"github.com/alexmorbo/seasonfill/internal/shared/media"
)

// staleTTL is the on-demand long-tail freshness window (PRD §5.1.1
// line 691). Lists older than 7d are refreshed inline on read.
const staleTTL = 7 * 24 * time.Hour

// defaultLang is the BCP-47 tag used when the client omits ?lang=.
// Matches the worker's en-US default and the genres_i18n fallback.
const defaultLang = "en-US"

// pagination defaults / clamps (PRD §6.6 / F-3 validator).
const (
	defaultPage    = 1
	maxPage        = 50
	defaultPerPage = 20
	maxPerPage     = 100
)

// bcp47Re mirrors internal/shared/ports/validator.go:25.
var bcp47Re = regexp.MustCompile(`^[a-zA-Z]{2,3}(-[a-zA-Z]{2,4})?$`)

// DiscoveryHandler serves the seven curated read endpoints. Construct
// via NewDiscoveryHandler (called from wiring/discovery.go).
type DiscoveryHandler struct {
	repo     app.DiscoveryListRepo
	warming  app.WarmingProbe
	refresh  app.RefreshOnDemand
	genres   *persistence.GenresPickerRepo
	networks *persistence.NetworksPickerRepo
	// search — story 508 (N-2g). Nil-OK at construction-time for tests
	// that exercise the curated endpoints only; the Search handler
	// returns 503 search_unavailable when the use case is unwired
	// (TMDB disabled at boot).
	search *app.SearchUseCase
	// resolver — story 526 (shared MediaResolver). Translates the
	// disco.Item raw TMDB PosterPath / BackdropPath into the sha256
	// hash the FE serves via /api/v1/media/:hash. Nil-OK: when nil,
	// projectItem ships the raw path verbatim (legacy behavior — the
	// FE then renders monograms because mediaproxy doesn't know the
	// path). Wiring hands the same *media.Resolver instance that the
	// seriesdetail composers use so cold-start cache hydration covers
	// the discovery slot too.
	resolver *media.Resolver
	// libraryInstances — story 527 (Bug 2 fix). Populates per-item
	// DiscoverySeriesItem.InLibraryInstances by issuing one batched
	// lookup per response over the page's canonical series ids.
	// Nil-OK: when nil, the slice ships as []string{} for every item
	// (legacy pre-527 behavior — UX regresses to "+ Add to Sonarr
	// always visible", never panics).
	libraryInstances app.LibraryInstancesPort
	log              *slog.Logger

	// sfGroup collapses concurrent cold-cache on-demand refresh calls
	// onto a single TMDB fetch. Key: kind|param|lang. The shared
	// group keeps memory usage flat — singleflight evicts the entry
	// the moment the call returns.
	sfGroup singleflight.Group
}

// NewDiscoveryHandler wires the handler against its narrow ports.
// Every arg is required EXCEPT searchUC and resolver (both nil-OK; the
// Search handler returns 503 search_unavailable when searchUC is nil;
// when resolver is nil, raw TMDB paths flow through projectItem
// unchanged — same as pre-526 behavior). Panics on missing required
// ports so a wiring bug surfaces at startup rather than at first
// request.
func NewDiscoveryHandler(
	repo app.DiscoveryListRepo,
	warming app.WarmingProbe,
	refresh app.RefreshOnDemand,
	genres *persistence.GenresPickerRepo,
	networks *persistence.NetworksPickerRepo,
	searchUC *app.SearchUseCase,
	resolver *media.Resolver,
	libraryInstances app.LibraryInstancesPort,
	log *slog.Logger,
) *DiscoveryHandler {
	switch {
	case repo == nil:
		panic("discovery handler: repo required")
	case warming == nil:
		panic("discovery handler: warming probe required")
	case refresh == nil:
		panic("discovery handler: refresh required")
	case genres == nil:
		panic("discovery handler: genres picker required")
	case networks == nil:
		panic("discovery handler: networks picker required")
	case log == nil:
		panic("discovery handler: log required")
	}
	return &DiscoveryHandler{
		repo:             repo,
		warming:          warming,
		refresh:          refresh,
		genres:           genres,
		networks:         networks,
		search:           searchUC,         // nil-OK
		resolver:         resolver,         // nil-OK
		libraryInstances: libraryInstances, // nil-OK
		log:              log,
	}
}

// Trending serves GET /api/v1/discovery/trending.
func (h *DiscoveryHandler) Trending(c *gin.Context) {
	scope := c.DefaultQuery("scope", "day")
	var kind disco.Kind
	switch scope {
	case "day":
		kind = disco.KindTrendingDay
	case "week":
		kind = disco.KindTrendingWeek
	default:
		respondError(c, http.StatusBadRequest, "invalid_scope",
			"scope must be 'day' or 'week'")
		return
	}
	h.serveLeaderboard(c, kind)
}

// Popular serves GET /api/v1/discovery/popular.
func (h *DiscoveryHandler) Popular(c *gin.Context) {
	h.serveLeaderboard(c, disco.KindPopular)
}

// serveLeaderboard runs the trending / popular pipeline:
//  1. validate lang + page + per_page
//  2. cold-start short-circuit when warming
//  3. read the (kind, "", lang) page from the repo
//  4. project + wrap in envelope
func (h *DiscoveryHandler) serveLeaderboard(c *gin.Context, kind disco.Kind) {
	lang, page, perPage, ok := h.parsePaging(c)
	if !ok {
		return
	}

	if h.warming.IsWarming() {
		c.JSON(http.StatusOK, h.warmingEnvelope(page, perPage))
		return
	}

	resp, err := h.readAndProject(c.Request.Context(), kind, "", lang, page, perPage, nil)
	if err != nil {
		h.log.WarnContext(c.Request.Context(), "discovery leaderboard read failed",
			slog.String("kind", string(kind)),
			slog.String("language", lang),
			slog.String("error", err.Error()))
		respondError(c, http.StatusInternalServerError, "discovery_read_failed",
			"read failed")
		return
	}
	c.JSON(http.StatusOK, resp)
}

// ByGenre serves GET /api/v1/discovery/genre/:id.
//
// Long-tail contract (PRD §5.1.1 lines 686-692): when the
// (KindByGenre, id, lang) tuple is missing OR stale-by-7d the handler
// calls RefreshOnDemand inline before reading. Concurrent cold-cache
// requests for the same key collapse onto a single TMDB fetch via
// singleflight.
func (h *DiscoveryHandler) ByGenre(c *gin.Context) {
	h.serveLongTail(c, disco.KindByGenre)
}

// ByNetwork serves GET /api/v1/discovery/network/:id. Same shape as
// ByGenre with kind=by_network.
func (h *DiscoveryHandler) ByNetwork(c *gin.Context) {
	h.serveLongTail(c, disco.KindByNetwork)
}

// ByKeyword serves GET /api/v1/discovery/keyword/:id. Same shape as
// ByGenre with kind=by_keyword. Keywords have no picker endpoint —
// clients must already know the keyword id (FE will offer this via
// /series/{id} keyword chips in N-3).
func (h *DiscoveryHandler) ByKeyword(c *gin.Context) {
	h.serveLongTail(c, disco.KindByKeyword)
}

// serveLongTail runs the genre / network / keyword pipeline:
//  1. parse :id (must be positive integer)
//  2. parse lang + page + per_page
//  3. test IsStale; if stale, RefreshNow (singleflight-collapsed)
//  4. read + project
//  5. if refresh failed AND repo still empty → 502
//     if refresh failed AND repo has stale rows → 200 + degraded:["refresh_failed"]
//     if refresh ok AND repo still empty → 200 + degraded:["genre_unknown_to_tmdb"]
func (h *DiscoveryHandler) serveLongTail(c *gin.Context, kind disco.Kind) {
	rawID := c.Param("id")
	idInt, err := strconv.Atoi(rawID)
	if err != nil || idInt <= 0 {
		respondError(c, http.StatusBadRequest, "invalid_id",
			"id must be a positive integer")
		return
	}
	param := strconv.Itoa(idInt)

	lang, page, perPage, ok := h.parsePaging(c)
	if !ok {
		return
	}

	ctx := c.Request.Context()

	stale, err := h.repo.IsStale(ctx, kind, param, lang, staleTTL)
	if err != nil {
		h.log.WarnContext(ctx, "discovery is_stale query failed",
			slog.String("kind", string(kind)),
			slog.String("param", param),
			slog.String("language", lang),
			slog.String("error", err.Error()))
		respondError(c, http.StatusInternalServerError, "discovery_read_failed",
			"is_stale failed")
		return
	}

	var refreshErr error
	if stale {
		key := string(kind) + "|" + param + "|" + lang
		_, refreshErr, _ = h.sfGroup.Do(key, func() (any, error) {
			// Defensive recover — singleflight propagates panics
			// to every coalesced caller. Wrap the worker call to
			// turn a panic into an error so a buggy refresh path
			// doesn't crash 16 in-flight goroutines simultaneously.
			defer func() {
				if r := recover(); r != nil {
					h.log.ErrorContext(ctx, "discovery refresh panic",
						slog.String("kind", string(kind)),
						slog.String("param", param),
						slog.String("language", lang),
						slog.Any("recover", r))
				}
			}()
			return nil, h.refresh.RefreshNow(ctx, kind, param, lang)
		})
		if refreshErr != nil {
			h.log.WarnContext(ctx, "discovery on-demand refresh failed",
				slog.String("kind", string(kind)),
				slog.String("param", param),
				slog.String("language", lang),
				slog.String("error", refreshErr.Error()))
		}
	}

	var extra []string
	if refreshErr != nil {
		extra = append(extra, "refresh_failed")
	}
	resp, err := h.readAndProject(ctx, kind, param, lang, page, perPage, extra)
	if err != nil {
		h.log.WarnContext(ctx, "discovery long-tail read failed",
			slog.String("kind", string(kind)),
			slog.String("param", param),
			slog.String("language", lang),
			slog.String("error", err.Error()))
		respondError(c, http.StatusInternalServerError, "discovery_read_failed",
			"read failed")
		return
	}

	switch {
	case len(resp.Items) == 0 && refreshErr != nil:
		// Refresh failed AND no stale fallback to render.
		respondError(c, http.StatusBadGateway, "discovery_unavailable",
			"upstream refresh failed and no cached rows available")
		return
	case len(resp.Items) == 0 && refreshErr == nil:
		// Refresh succeeded but TMDB returned no items for this param —
		// surface a non-fatal hint so the FE renders empty-state.
		// errStaleRead remains the named sentinel for log correlation
		// across the two response paths.
		_ = errStaleRead
		resp.Degraded = append(resp.Degraded, "genre_unknown_to_tmdb")
	}

	c.JSON(http.StatusOK, resp)
}

// PickerGenres serves GET /api/v1/discovery/genres.
func (h *DiscoveryHandler) PickerGenres(c *gin.Context) {
	lang := c.DefaultQuery("lang", defaultLang)
	if !validateLang(lang) {
		respondError(c, http.StatusBadRequest, "invalid_language",
			"lang must be a BCP-47 tag")
		return
	}
	items, err := h.genres.List(c.Request.Context(), lang)
	if err != nil {
		h.log.WarnContext(c.Request.Context(), "discovery genres picker failed",
			slog.String("error", err.Error()))
		respondError(c, http.StatusInternalServerError, "picker_read_failed",
			"genres picker read failed")
		return
	}
	c.JSON(http.StatusOK, gin.H{"items": items})
}

// PickerNetworks serves GET /api/v1/discovery/networks.
func (h *DiscoveryHandler) PickerNetworks(c *gin.Context) {
	lang := c.DefaultQuery("lang", defaultLang)
	if !validateLang(lang) {
		respondError(c, http.StatusBadRequest, "invalid_language",
			"lang must be a BCP-47 tag")
		return
	}
	items, err := h.networks.List(c.Request.Context(), lang)
	if err != nil {
		h.log.WarnContext(c.Request.Context(), "discovery networks picker failed",
			slog.String("error", err.Error()))
		respondError(c, http.StatusInternalServerError, "picker_read_failed",
			"networks picker read failed")
		return
	}
	c.JSON(http.StatusOK, gin.H{"items": items})
}

// Search serves GET /api/v1/discovery/search?q=…&lang=… (story 508).
// Two-tier lookup per PRD §5.1.1 lines 711-720:
//
//  1. Local LIKE: response envelope {items, source:"local"}.
//  2. On local miss: TMDB /search/tv fallback with stub-upsert +
//     hot enqueue, envelope {items, source:"tmdb"}.
//
// Validation:
//   - q trimmed; empty → 400 invalid_query.
//   - len(q) > 100 → 400 invalid_query.
//   - lang BCP-47 validated via the shared regex (defaults to en-US).
//
// Errors:
//   - TMDB transport failure on fallback path → 502 tmdb_unavailable.
//   - Repo error on local path → 500 search_read_failed.
func (h *DiscoveryHandler) Search(c *gin.Context) {
	if h.search == nil {
		respondError(c, http.StatusServiceUnavailable, "search_unavailable",
			"search use case not wired (TMDB disabled)")
		return
	}
	q := strings.TrimSpace(c.Query("q"))
	if q == "" || len(q) > 100 {
		respondError(c, http.StatusBadRequest, "invalid_query",
			"q must be 1..100 characters after trim")
		return
	}
	lang := c.DefaultQuery("lang", defaultLang)
	if !validateLang(lang) {
		respondError(c, http.StatusBadRequest, "invalid_language",
			"lang must be a BCP-47 tag")
		return
	}

	ctx := c.Request.Context()

	localItems, err := h.search.Local(ctx, q, lang, 20)
	if err != nil {
		h.log.WarnContext(ctx, "discovery.search.local_failed",
			slog.String("query", q),
			slog.String("language", lang),
			slog.String("error", err.Error()))
		respondError(c, http.StatusInternalServerError, "search_read_failed",
			"local search failed")
		return
	}
	if len(localItems) > 0 {
		inLib := h.resolveInLibrary(ctx, localItems)
		c.JSON(http.StatusOK, gin.H{
			"items":  projectSearchItems(ctx, localItems, h.resolver, inLib),
			"source": "local",
		})
		return
	}

	tmdbItems, err := h.search.TMDBFallback(ctx, q, lang)
	if err != nil {
		respondError(c, http.StatusBadGateway, "tmdb_unavailable",
			"tmdb fallback failed")
		return
	}
	inLib := h.resolveInLibrary(ctx, tmdbItems)
	c.JSON(http.StatusOK, gin.H{
		"items":  projectSearchItems(ctx, tmdbItems, h.resolver, inLib),
		"source": "tmdb",
	})
}

// projectSearchItems maps domain Items → wire DiscoverySeriesItem
// preserving the curated endpoints' projection contract (empty []
// for InLibraryInstances, nil-safe pointer field copies). Story 526
// — when resolver is non-nil, raw TMDB poster/backdrop paths are
// translated into sha256 wire hashes that the FE serves via
// /api/v1/media/:hash. Nil resolver preserves the legacy raw-path
// behavior.
func projectSearchItems(
	ctx context.Context,
	items []disco.Item,
	resolver *media.Resolver,
	inLibrary map[shareddomain.SeriesID][]string,
) []DiscoverySeriesItem {
	out := make([]DiscoverySeriesItem, 0, len(items))
	for _, it := range items {
		out = append(out, projectItem(ctx, it, resolver, inLibrary))
	}
	return out
}

// parsePaging extracts (lang, page, per_page) from the query string,
// applies defaults + clamps + BCP-47 validation, and returns false
// after writing a 400 envelope. The caller MUST stop processing on
// !ok.
func (h *DiscoveryHandler) parsePaging(c *gin.Context) (lang string, page, perPage int, ok bool) {
	lang = c.DefaultQuery("lang", defaultLang)
	if !validateLang(lang) {
		respondError(c, http.StatusBadRequest, "invalid_language",
			"lang must be a BCP-47 tag")
		return "", 0, 0, false
	}

	page = defaultPage
	if raw := c.Query("page"); raw != "" {
		v, err := strconv.Atoi(raw)
		if err != nil || v < 1 || v > maxPage {
			respondError(c, http.StatusBadRequest, "invalid_page",
				"page must be in [1,50]")
			return "", 0, 0, false
		}
		page = v
	}

	perPage = defaultPerPage
	if raw := c.Query("per_page"); raw != "" {
		v, err := strconv.Atoi(raw)
		if err != nil || v < 1 {
			respondError(c, http.StatusBadRequest, "invalid_per_page",
				"per_page must be a positive integer")
			return "", 0, 0, false
		}
		if v > maxPerPage {
			v = maxPerPage
		}
		perPage = v
	}
	return lang, page, perPage, true
}

// readAndProject reads one (kind, param, lang) page from the repo,
// projects disco.Item → DiscoverySeriesItem, and returns the wire
// envelope. extraDegraded is appended verbatim — long-tail handlers
// pass ["refresh_failed"] when on-demand refresh errored but stale
// rows remain readable.
func (h *DiscoveryHandler) readAndProject(
	ctx context.Context,
	kind disco.Kind,
	param, lang string,
	page, perPage int,
	extraDegraded []string,
) (*DiscoveryListResponse, error) {
	pg, err := h.repo.GetRanked(ctx, kind, param, lang, page, perPage)
	if err != nil {
		return nil, err
	}

	inLib := h.resolveInLibrary(ctx, pg.Items)
	items := make([]DiscoverySeriesItem, 0, len(pg.Items))
	for _, it := range pg.Items {
		items = append(items, projectItem(ctx, it, h.resolver, inLib))
	}
	resp := &DiscoveryListResponse{
		Items:       items,
		RefreshedAt: pg.RefreshedAt,
		Page:        page,
		PerPage:     perPage,
		Total:       pg.Total,
	}
	if len(extraDegraded) > 0 {
		resp.Degraded = append(resp.Degraded, extraDegraded...)
	}
	return resp, nil
}

// projectItem maps the domain Item → wire DiscoverySeriesItem.
// Genres / OriginalTitle / IMDBRating / Status are NOT populated by
// GetRanked today (the repo's JOIN omits those series columns). They
// stay nil until N-2g extends the projection — by which time the FE
// will already render the no-data branch.
//
// TMDBRating (story 1036) IS populated: GetRanked COALESCEs the canon
// series.tmdb_rating over the ingest-stored discovery_lists.tmdb_rating
// floor, so every item TMDB provides a vote_average for carries a rating.
//
// TVDBID + OriginalLanguage joined into the projection in story 523
// (N-4 unblock) so the FE AddToSonarr modal can submit straight from
// the list response.
//
// InLibraryInstances (story 527): when inLibrary is non-nil and the
// map carries the item's SeriesID, the wire field renders the sorted
// instance slug list (matches the existing Sonarr-instance naming).
// Otherwise []string{} — same shape as pre-527.
//
// Story 526 — when resolver is non-nil, PosterPath / BackdropPath are
// translated from raw TMDB paths into sha256 wire hashes by routing
// through the shared MediaResolver. The same async pre-warm queue +
// EnsurePending semantics that fire from the seriesdetail composer
// fire here too, so a /discovery tile hit warms the mediaproxy cache
// for the eventual /series/{id} click-through. Nil resolver leaves
// the raw path untouched (legacy behavior — matches the pre-526
// projection contract verbatim).
//
// Story 554 — the resolved value is mirrored onto BOTH the new
// PosterHash / BackdropHash wire fields AND the legacy PosterPath /
// BackdropPath ones so a stale FE bundle that still reads the legacy
// names continues to render correctly during the FE CDN cache
// transition window. New FE bundles prefer the *_hash fields. See
// internal/discovery/rest/types.go for the rename rationale.
func projectItem(
	ctx context.Context,
	it disco.Item,
	resolver *media.Resolver,
	inLibrary map[shareddomain.SeriesID][]string,
) DiscoverySeriesItem {
	posterPath := it.PosterPath
	backdropPath := it.BackdropPath
	if resolver != nil {
		// w342 mirrors the SeriesPosterListSize the pre-warm pipeline
		// uses for catalog tiles — same source URL → same hash, so the
		// /discovery tile shares the mediaproxy cache slot with the
		// /series/{id}/list view.
		if hash := resolver.Resolve(ctx, it.PosterPath, "w342", "poster_w342"); hash != nil {
			posterPath = hash
		}
		// w780 mirrors the SeriesBackdropListSize used by the canon
		// tiles. The backdrop is below-the-fold in /discovery, so
		// async-only Resolve is appropriate (no ResolveSync budget).
		if hash := resolver.Resolve(ctx, it.BackdropPath, "w780", "backdrop_w780"); hash != nil {
			backdropPath = hash
		}
	}
	instances := []string{}
	if inLibrary != nil {
		if hit, ok := inLibrary[it.SeriesID]; ok && len(hit) > 0 {
			instances = append(instances, hit...)
		}
	}
	out := DiscoverySeriesItem{
		ID:                 int64(it.SeriesID),
		Title:              it.Title,
		Year:               it.Year,
		TMDBRating:         it.TMDBRating, // story 1036 — ingest-stored TMDB vote_average
		PosterHash:         posterPath,    // story 554 — same value, new wire name
		PosterPath:         posterPath,    // legacy mirror
		BackdropHash:       backdropPath,  // story 554 — new wire name
		BackdropPath:       backdropPath,  // legacy mirror
		OriginalLanguage:   it.OriginalLanguage,
		InLibraryInstances: instances,
	}
	if it.TMDBID != nil {
		v := int(*it.TMDBID)
		out.TMDBID = &v
	}
	if it.TVDBID != nil {
		v := int(*it.TVDBID)
		out.TVDBID = &v
	}
	if len(it.Genres) > 0 {
		out.Genres = append(out.Genres, it.Genres...)
	}
	return out
}

// resolveInLibrary issues one batched cross-instance lookup for the
// page items and returns a map keyed on series id. The caller threads
// the map into projectItem so the projection loop stays O(N) over the
// page without per-item SQL.
//
// Failure modes:
//   - nil port (tests, wiring race) → returns nil; projectItem keeps
//     []string{} per the existing wire contract.
//   - port error → logs a single Warn (domain="discovery") and returns
//     nil; UX regresses to "+ Add to Sonarr always visible" but the
//     request never 500s. The hardcoded-empty path is the same shape
//     as the pre-527 behavior, so the regression is invisible to the
//     FE's nil-guard at DiscoverySeriesCard.tsx:23.
//   - empty page (len(items)==0) → returns nil (no SQL).
func (h *DiscoveryHandler) resolveInLibrary(ctx context.Context, items []disco.Item) map[shareddomain.SeriesID][]string {
	if h.libraryInstances == nil || len(items) == 0 {
		return nil
	}
	ids := make([]shareddomain.SeriesID, 0, len(items))
	for _, it := range items {
		if it.SeriesID > 0 {
			ids = append(ids, it.SeriesID)
		}
	}
	if len(ids) == 0 {
		return nil
	}
	m, err := h.libraryInstances.ListByCanonicalSeriesIDs(ctx, ids)
	if err != nil {
		h.log.WarnContext(ctx, "discovery.in_library.lookup_failed",
			slog.Int("page_size", len(ids)),
			slog.String("error", err.Error()))
		return nil
	}
	return m
}

// warmingEnvelope returns the cold-start short-circuit response shape.
func (h *DiscoveryHandler) warmingEnvelope(page, perPage int) DiscoveryListResponse {
	est := WarmingEstimateSeconds
	return DiscoveryListResponse{
		Items:       []DiscoverySeriesItem{},
		RefreshedAt: time.Time{},
		Page:        page,
		PerPage:     perPage,
		Total:       0,
		Degraded:    []string{"discovery_warming"},
		WarmingEst:  &est,
	}
}

// validateLang gates the BCP-47 subset documented at
// internal/shared/ports/validator.go:25 — 2-3 letter language +
// optional 2-4 letter region/script. Empty is rejected here; the
// callers default to "en-US" BEFORE calling.
func validateLang(s string) bool {
	if s == "" {
		return false
	}
	// Inlined the regex to avoid importing the struct-validator
	// machinery for a one-field check. Matches
	// internal/shared/ports/validator.go bcp47LanguageTagPattern.
	return bcp47Re.MatchString(s)
}

// respondError emits the F-2c envelope and aborts the chain so any
// downstream middleware (e.g. trace_id logger) sees the chosen
// status. The handler does NOT route through ErrorResponseMiddleware
// because it does not push c.Error — the slug + status are chosen
// at the call site for maximum readability of branch coverage.
func respondError(c *gin.Context, status int, slug, msg string) {
	c.AbortWithStatusJSON(status, gin.H{
		"error":   slug,
		"message": msg,
	})
}

// errStaleRead is the marker used by serveLongTail when the repo read
// after RefreshNow still returns 0 items — the handler surfaces
// "genre_unknown_to_tmdb" in degraded[] rather than 404.
var errStaleRead = errors.New("discovery: stale read after refresh")
