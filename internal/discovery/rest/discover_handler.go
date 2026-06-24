// discover_handler.go ships GET /api/v1/discovery/discover (story 509
// N-2h). Pattern B handler flow per PRD §5.1.2 line 815:
//
//	FE /discover?... → handler:
//	  1. F-3 validate filter + lang + page
//	  2. cacheKey = canonicalHash(filter, lang, page)
//	  3. LRU.Get(cacheKey)
//	       ├── HIT  → 200 {items, cache_status: "hit"}
//	       └── MISS
//	           4. Sync attempt with ctx.WithTimeout(5s)
//	              ├── err == nil → LRU.Add, 200 {items, cache_status: "miss"}
//	              ├── err == DeadlineExceeded
//	              │     → bg.EnqueueDedup → 202 {items:[], cache_status:"warming",
//	              │                              degraded:["tmdb_throttled"],
//	              │                              retry_after_seconds:3}
//	              └── other err → 502 {error: "tmdb_unavailable"}
//
// degraded envelope folds multiple signals — `discovery_warming` from the
// worker probe + `tmdb_throttled` when LastWaitSeconds > 1s can both be
// present.
//
// Outcome metric: discover_handler_outcome_total{outcome="hit|miss_sync|
// miss_warming|error"} is incremented per branch. All 4 outcomes are
// exercised by unit tests.
package rest

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/alexmorbo/seasonfill/internal/discovery/app"
	disco "github.com/alexmorbo/seasonfill/internal/discovery/domain"
	"github.com/alexmorbo/seasonfill/internal/observability"
	"github.com/alexmorbo/seasonfill/internal/shared/cachewatch"
	"github.com/alexmorbo/seasonfill/internal/shared/clients/tmdb"
	"github.com/alexmorbo/seasonfill/internal/shared/media"
)

// Pattern B constants (PRD §5.1.2).
const (
	discoverSyncTimeout       = 5 * time.Second
	discoverWarmingRetryAfter = 3
	discoverPerPage           = 20
	discoverMaxPage           = 500
	discoverThrottleThreshold = 1 * time.Second
)

// Outcome label values exposed via discover_handler_outcome_total{outcome=…}.
const (
	OutcomeHit         = "hit"
	OutcomeMissSync    = "miss_sync"
	OutcomeMissWarming = "miss_warming"
	OutcomeError       = "error"
)

// DiscoverResponse is the wire envelope for /discovery/discover.
type DiscoverResponse struct {
	Items             []DiscoverySeriesItem `json:"items"`
	Page              int                   `json:"page"`
	PerPage           int                   `json:"per_page"`
	CacheStatus       string                `json:"cache_status"`
	Degraded          []string              `json:"degraded,omitempty"`
	RetryAfterSeconds int                   `json:"retry_after_seconds,omitempty"`
}

// DiscoverHandler serves GET /api/v1/discovery/discover.
type DiscoverHandler struct {
	lru     *cachewatch.Cache[string, []disco.Item]
	pass    app.TMDBPassthrough
	bg      *app.BgFetcher
	warming app.WarmingProbe
	// resolver — story 526 (shared MediaResolver). Same role as the
	// curated DiscoveryHandler counterpart: rewrites raw TMDB image
	// paths into sha256 wire hashes so the FE renders posters through
	// /api/v1/media/:hash. Nil-OK preserves legacy raw-path behavior.
	resolver *media.Resolver
	log      *slog.Logger
}

// NewDiscoverHandler wires the handler. lru/pass/bg/warming/log are
// required; resolver is nil-OK (legacy behavior — raw TMDB paths
// flow through projectItem unchanged).
func NewDiscoverHandler(
	lru *cachewatch.Cache[string, []disco.Item],
	pass app.TMDBPassthrough,
	bg *app.BgFetcher,
	warming app.WarmingProbe,
	resolver *media.Resolver,
	log *slog.Logger,
) *DiscoverHandler {
	switch {
	case lru == nil:
		panic("discover handler: lru required")
	case pass == nil:
		panic("discover handler: passthrough required")
	case bg == nil:
		panic("discover handler: bg fetcher required")
	case warming == nil:
		panic("discover handler: warming probe required")
	case log == nil:
		panic("discover handler: log required")
	}
	return &DiscoverHandler{lru: lru, pass: pass, bg: bg, warming: warming, resolver: resolver, log: log}
}

// Handle implements Pattern B.
func (h *DiscoverHandler) Handle(c *gin.Context) {
	filter, lang, page, ok := h.parse(c)
	if !ok {
		return
	}
	cacheKey := canonicalHash(filter, lang, page)

	ctx := c.Request.Context()

	// 1. LRU hit.
	if items, found := h.lru.Get(cacheKey); found {
		observability.IncDiscoverHandlerOutcome(OutcomeHit)
		c.JSON(http.StatusOK, h.envelope(ctx, items, page, "hit", 0))
		return
	}

	// 2. Sync attempt with 5s timeout.
	syncCtx, cancel := context.WithTimeout(ctx, discoverSyncTimeout)
	defer cancel()
	items, err := h.pass.Fetch(syncCtx, filter, lang, page)
	switch {
	case err == nil:
		h.lru.Add(cacheKey, items)
		observability.IncDiscoverHandlerOutcome(OutcomeMissSync)
		c.JSON(http.StatusOK, h.envelope(ctx, items, page, "miss", 0))
		return
	case errors.Is(err, context.DeadlineExceeded) || errors.Is(syncCtx.Err(), context.DeadlineExceeded):
		// 3. Sync timed out — kick the background fetcher + return 202.
		h.bg.EnqueueDedup(cacheKey, filter, lang, page)
		observability.IncDiscoverHandlerOutcome(OutcomeMissWarming)
		resp := h.envelope(ctx, nil, page, "warming", discoverWarmingRetryAfter)
		resp.Degraded = appendDegraded(resp.Degraded, "tmdb_throttled")
		c.JSON(http.StatusAccepted, resp)
		return
	default:
		// 4. Hard failure (TMDB 5xx, network, decode).
		h.log.WarnContext(c.Request.Context(), "discovery.discover.handler_error",
			slog.String("cache_key", cacheKey),
			slog.Int("page", page),
			slog.String("error", err.Error()))
		observability.IncDiscoverHandlerOutcome(OutcomeError)
		respondError(c, http.StatusBadGateway, "tmdb_unavailable",
			"upstream discover fetch failed")
		return
	}
}

// envelope folds the response shape including the (possibly empty)
// degraded list signals.
func (h *DiscoverHandler) envelope(ctx context.Context, items []disco.Item, page int, status string, retryAfter int) DiscoverResponse {
	resp := DiscoverResponse{
		Items:             projectSearchItems(ctx, items, h.resolver),
		Page:              page,
		PerPage:           discoverPerPage,
		CacheStatus:       status,
		RetryAfterSeconds: retryAfter,
	}
	if h.warming.IsWarming() {
		resp.Degraded = appendDegraded(resp.Degraded, "discovery_warming")
	}
	if h.pass.LastWaitSeconds() > discoverThrottleThreshold.Seconds() {
		resp.Degraded = appendDegraded(resp.Degraded, "tmdb_throttled")
	}
	return resp
}

// parse binds query parameters into a DiscoverFilter and validates page +
// lang. On error, writes the F-2c envelope and returns ok=false.
//
// Validation rules (PRD §5.1.2):
//   - lang: BCP-47 (defaults to en-US).
//   - page: 1..500 (TMDB cap).
//   - sort_by: closed set { popularity.desc | vote_average.desc |
//     first_air_date.desc } when present; empty allowed.
//   - WithStatus/WithType ints clamped to documented enums (0..5 / 0..6).
//   - WithStatusOp / WithTypeOp ∈ { "", "and", "or" }.
func (h *DiscoverHandler) parse(c *gin.Context) (tmdb.DiscoverFilter, string, int, bool) {
	lang := c.DefaultQuery("lang", defaultLang)
	if !validateLang(lang) {
		respondError(c, http.StatusBadRequest, "invalid_filter", "lang must be BCP-47")
		return tmdb.DiscoverFilter{}, "", 0, false
	}
	page := 1
	if raw := c.Query("page"); raw != "" {
		v, err := strconv.Atoi(raw)
		if err != nil || v < 1 || v > discoverMaxPage {
			respondError(c, http.StatusBadRequest, "invalid_filter", "page must be in [1,500]")
			return tmdb.DiscoverFilter{}, "", 0, false
		}
		page = v
	}

	filter := tmdb.DiscoverFilter{}
	var bindErr string

	parseIntList := func(qkey string, dst *[]int, lo, hi int) {
		if bindErr != "" {
			return
		}
		raw := c.Query(qkey)
		if raw == "" {
			return
		}
		for s := range strings.SplitSeq(raw, ",") {
			s = strings.TrimSpace(s)
			if s == "" {
				continue
			}
			n, err := strconv.Atoi(s)
			if err != nil || n < lo || n > hi {
				bindErr = qkey
				return
			}
			*dst = append(*dst, n)
		}
	}
	parseIntList("with_genres", &filter.WithGenres, 1, 100000)
	parseIntList("without_genres", &filter.WithoutGenres, 1, 100000)
	parseIntList("with_networks", &filter.WithNetworks, 1, 1_000_000)
	parseIntList("with_keywords", &filter.WithKeywords, 1, 1_000_000)
	parseIntList("with_watch_providers", &filter.WithWatchProviders, 1, 1_000_000)
	parseIntList("with_status", &filter.WithStatus, 0, 5)
	parseIntList("with_type", &filter.WithType, 0, 6)

	parseStringPtr := func(qkey string, dst **string) {
		if bindErr != "" {
			return
		}
		v := strings.TrimSpace(c.Query(qkey))
		if v == "" {
			return
		}
		*dst = &v
	}
	parseStringPtr("first_air_date.gte", &filter.FirstAirDateGte)
	parseStringPtr("first_air_date.lte", &filter.FirstAirDateLte)
	parseStringPtr("with_original_language", &filter.WithOriginalLang)
	parseStringPtr("with_origin_country", &filter.WithOriginCountry)
	parseStringPtr("watch_region", &filter.WatchRegion)

	parseFloatPtr := func(qkey string, dst **float64) {
		if bindErr != "" {
			return
		}
		raw := c.Query(qkey)
		if raw == "" {
			return
		}
		v, err := strconv.ParseFloat(raw, 64)
		if err != nil || v < 0 || v > 10 {
			bindErr = qkey
			return
		}
		*dst = &v
	}
	parseFloatPtr("vote_average.gte", &filter.VoteAverageGte)
	parseFloatPtr("vote_average.lte", &filter.VoteAverageLte)

	parseIntPtr := func(qkey string, dst **int, lo, hi int) {
		if bindErr != "" {
			return
		}
		raw := c.Query(qkey)
		if raw == "" {
			return
		}
		v, err := strconv.Atoi(raw)
		if err != nil || v < lo || v > hi {
			bindErr = qkey
			return
		}
		*dst = &v
	}
	parseIntPtr("vote_count.gte", &filter.VoteCountGte, 0, 1_000_000)
	parseIntPtr("with_runtime.gte", &filter.WithRuntimeGte, 0, 1000)
	parseIntPtr("with_runtime.lte", &filter.WithRuntimeLte, 0, 1000)

	// Closed-set enums.
	if raw := strings.TrimSpace(c.Query("with_status_op")); raw != "" {
		if raw != "and" && raw != "or" {
			bindErr = "with_status_op"
		}
		filter.WithStatusOp = raw
	}
	if raw := strings.TrimSpace(c.Query("with_type_op")); raw != "" {
		if raw != "and" && raw != "or" {
			bindErr = "with_type_op"
		}
		filter.WithTypeOp = raw
	}
	if raw := strings.TrimSpace(c.Query("sort_by")); raw != "" {
		switch raw {
		case "popularity.desc", "vote_average.desc", "first_air_date.desc":
			filter.SortBy = raw
		default:
			bindErr = "sort_by"
		}
	}

	if bindErr != "" {
		respondError(c, http.StatusBadRequest, "invalid_filter", bindErr+" failed validation")
		return tmdb.DiscoverFilter{}, "", 0, false
	}
	return filter, lang, page, true
}

// appendDegraded inserts s into the slice iff not already present.
func appendDegraded(in []string, s string) []string {
	for _, v := range in {
		if v == s {
			return in
		}
	}
	return append(in, s)
}
