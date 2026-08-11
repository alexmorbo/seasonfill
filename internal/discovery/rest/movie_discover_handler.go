// movie_discover_handler.go ships the movie discovery HTTP surface (Ф6-R-4a):
//
//	GET /api/v1/discovery/movie/discover
//	GET /api/v1/discovery/movie/trending?scope=day|week
//	GET /api/v1/discovery/movie/popular
//	GET /api/v1/discovery/movie/search?q=&lang=&page=
//
// /discover follows the TV Pattern-B flow (LRU hit → 5s sync → 202 warming →
// 502) minus the keyword blocklist (no movie blocklist in R-4a). trending /
// popular / search are straight passthrough-sync (no LRU, no prewarm worker —
// deferred per story §2.9); every returned row is still stub-upserted by the
// passthrough.
//
// Outcome metric: movie_discover_handler_outcome_total{outcome=…} — a metric
// distinct from the TV discover counter (never repurposed).
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

// Movie discovery Pattern-B constants (mirror the TV discover handler).
const (
	movieDiscoverSyncTimeout       = 5 * time.Second
	movieDiscoverWarmingRetryAfter = 3
	movieDiscoverPerPage           = 20
	movieDiscoverMaxPage           = 500
	movieDiscoverThrottleThreshold = 1 * time.Second
)

// movieReleaseTypeMin / Max bound the with_release_type enum (1..6).
const (
	movieReleaseTypeMin = 1
	movieReleaseTypeMax = 6
)

// MovieDiscoverHandler serves the four movie discovery endpoints.
type MovieDiscoverHandler struct {
	lru      *cachewatch.Cache[string, []disco.MovieItem]
	pass     app.MovieTMDBPassthrough
	resolver *media.Resolver // nil-OK: raw TMDB paths flow through unchanged
	log      *slog.Logger
}

// NewMovieDiscoverHandler wires the handler. lru/pass/log are required;
// resolver is nil-OK (legacy raw-path behavior). Panics on missing required
// ports so a wiring bug surfaces at boot.
func NewMovieDiscoverHandler(
	lru *cachewatch.Cache[string, []disco.MovieItem],
	pass app.MovieTMDBPassthrough,
	resolver *media.Resolver,
	log *slog.Logger,
) *MovieDiscoverHandler {
	switch {
	case lru == nil:
		panic("movie discover handler: lru required")
	case pass == nil:
		panic("movie discover handler: passthrough required")
	case log == nil:
		panic("movie discover handler: log required")
	}
	return &MovieDiscoverHandler{lru: lru, pass: pass, resolver: resolver, log: log}
}

// Discover implements Pattern B for /discovery/movie/discover.
func (h *MovieDiscoverHandler) Discover(c *gin.Context) {
	filter, lang, page, ok := h.parse(c)
	if !ok {
		return
	}
	cacheKey := movieCanonicalHash(filter, lang, page)
	ctx := c.Request.Context()

	// 1. LRU hit.
	if items, found := h.lru.Get(cacheKey); found {
		observability.IncMovieDiscoverHandlerOutcome(OutcomeHit)
		c.JSON(http.StatusOK, h.envelope(ctx, items, page, "hit", 0))
		return
	}

	// 2. Sync attempt with 5s timeout.
	syncCtx, cancel := context.WithTimeout(ctx, movieDiscoverSyncTimeout)
	defer cancel()
	items, err := h.pass.FetchDiscover(syncCtx, filter, lang, page)
	switch {
	case err == nil:
		h.lru.Add(cacheKey, items)
		observability.IncMovieDiscoverHandlerOutcome(OutcomeMissSync)
		c.JSON(http.StatusOK, h.envelope(ctx, items, page, "miss", 0))
		return
	case errors.Is(err, context.DeadlineExceeded) || errors.Is(syncCtx.Err(), context.DeadlineExceeded):
		// 3. Sync timed out → 202 warming. R-4a has no movie bg fetcher, so
		// the LRU is not warmed asynchronously; the next request retries the
		// sync path (documented deviation — the TV handler enqueues a bg fetch).
		observability.IncMovieDiscoverHandlerOutcome(OutcomeMissWarming)
		resp := h.envelope(ctx, nil, page, "warming", movieDiscoverWarmingRetryAfter)
		resp.Degraded = appendDegraded(resp.Degraded, "tmdb_throttled")
		c.JSON(http.StatusAccepted, resp)
		return
	default:
		// 4. Hard failure (TMDB 5xx, network, decode).
		h.log.WarnContext(ctx, "discovery.movie.discover.handler_error",
			slog.String("cache_key", cacheKey),
			slog.Int("page", page),
			slog.String("error", err.Error()))
		observability.IncMovieDiscoverHandlerOutcome(OutcomeError)
		respondError(c, http.StatusBadGateway, "tmdb_unavailable", "upstream movie discover fetch failed")
		return
	}
}

// Trending serves /discovery/movie/trending?scope=day|week.
func (h *MovieDiscoverHandler) Trending(c *gin.Context) {
	scope := c.DefaultQuery("scope", "day")
	var ts tmdb.TrendingScope
	switch scope {
	case "day":
		ts = tmdb.TrendingDay
	case "week":
		ts = tmdb.TrendingWeek
	default:
		respondError(c, http.StatusBadRequest, "invalid_scope", "scope must be 'day' or 'week'")
		return
	}
	lang, page, ok := h.parsePaging(c)
	if !ok {
		return
	}
	h.serveList(c, "trending", func(ctx context.Context) ([]disco.MovieItem, error) {
		return h.pass.FetchTrending(ctx, ts, lang, page)
	}, page)
}

// Popular serves /discovery/movie/popular.
func (h *MovieDiscoverHandler) Popular(c *gin.Context) {
	lang, page, ok := h.parsePaging(c)
	if !ok {
		return
	}
	h.serveList(c, "popular", func(ctx context.Context) ([]disco.MovieItem, error) {
		return h.pass.FetchPopular(ctx, lang, page)
	}, page)
}

// Search serves /discovery/movie/search?q=…&lang=…&page=…
func (h *MovieDiscoverHandler) Search(c *gin.Context) {
	q := strings.TrimSpace(c.Query("q"))
	if q == "" || len(q) > 100 {
		respondError(c, http.StatusBadRequest, "invalid_query", "q must be 1..100 characters after trim")
		return
	}
	lang, page, ok := h.parsePaging(c)
	if !ok {
		return
	}
	h.serveList(c, "search", func(ctx context.Context) ([]disco.MovieItem, error) {
		return h.pass.FetchSearch(ctx, q, lang, page)
	}, page)
}

// serveList runs the passthrough-sync flow for trending / popular / search:
// one bounded TMDB fetch → 200 on success, 502 on transport failure. No LRU;
// the passthrough still stub-upserts every returned row.
func (h *MovieDiscoverHandler) serveList(
	c *gin.Context,
	endpoint string,
	fetch func(context.Context) ([]disco.MovieItem, error),
	page int,
) {
	ctx := c.Request.Context()
	syncCtx, cancel := context.WithTimeout(ctx, movieDiscoverSyncTimeout)
	defer cancel()
	items, err := fetch(syncCtx)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(syncCtx.Err(), context.DeadlineExceeded) {
			observability.IncMovieDiscoverHandlerOutcome(OutcomeMissWarming)
			resp := h.envelope(ctx, nil, page, "warming", movieDiscoverWarmingRetryAfter)
			resp.Degraded = appendDegraded(resp.Degraded, "tmdb_throttled")
			c.JSON(http.StatusAccepted, resp)
			return
		}
		h.log.WarnContext(ctx, "discovery.movie."+endpoint+".handler_error",
			slog.Int("page", page),
			slog.String("error", err.Error()))
		observability.IncMovieDiscoverHandlerOutcome(OutcomeError)
		respondError(c, http.StatusBadGateway, "tmdb_unavailable", "upstream movie "+endpoint+" fetch failed")
		return
	}
	observability.IncMovieDiscoverHandlerOutcome(OutcomeMissSync)
	c.JSON(http.StatusOK, h.envelope(ctx, items, page, "miss", 0))
}

// envelope projects the movie items + folds the degraded signals.
func (h *MovieDiscoverHandler) envelope(ctx context.Context, items []disco.MovieItem, page int, status string, retryAfter int) MovieDiscoverResponse {
	resp := MovieDiscoverResponse{
		Items:             projectMovieItems(ctx, items, h.resolver),
		Page:              page,
		PerPage:           movieDiscoverPerPage,
		CacheStatus:       status,
		RetryAfterSeconds: retryAfter,
	}
	if h.pass.LastWaitSeconds() > movieDiscoverThrottleThreshold.Seconds() {
		resp.Degraded = appendDegraded(resp.Degraded, "tmdb_throttled")
	}
	return resp
}

// projectMovieItems maps domain MovieItems → wire MovieDiscoverItem,
// rewriting raw TMDB poster/backdrop paths into sha256 wire hashes via the
// shared MediaResolver (nil-OK → raw paths flow through). Same w342/w780
// sizes the series discovery projection uses, so movie tiles share the
// mediaproxy cache slot with the eventual /movie/{id} click-through.
func projectMovieItems(ctx context.Context, items []disco.MovieItem, resolver *media.Resolver) []MovieDiscoverItem {
	out := make([]MovieDiscoverItem, 0, len(items))
	for _, it := range items {
		poster := it.PosterPath
		backdrop := it.BackdropPath
		if resolver != nil {
			if hash := resolver.Resolve(ctx, it.PosterPath, "w342", "poster_w342"); hash != nil {
				poster = hash
			}
			if hash := resolver.Resolve(ctx, it.BackdropPath, "w780", "backdrop_w780"); hash != nil {
				backdrop = hash
			}
		}
		row := MovieDiscoverItem{
			MovieID:          int64(it.MovieID),
			Title:            it.Title,
			Year:             it.Year,
			PosterHash:       poster,
			BackdropHash:     backdrop,
			OriginalLanguage: it.OriginalLanguage,
			TMDBRating:       it.TMDBRating,
		}
		if it.TMDBID != nil {
			v := int(*it.TMDBID)
			row.TMDBID = &v
		}
		out = append(out, row)
	}
	return out
}

// parsePaging extracts (lang, page) for trending / popular / search, applies
// defaults + clamps + BCP-47 validation, and writes a 400 envelope on error.
func (h *MovieDiscoverHandler) parsePaging(c *gin.Context) (lang string, page int, ok bool) {
	lang = c.DefaultQuery("lang", defaultLang)
	if !validateLang(lang) {
		respondError(c, http.StatusBadRequest, "invalid_language", "lang must be a BCP-47 tag")
		return "", 0, false
	}
	page = 1
	if raw := c.Query("page"); raw != "" {
		v, err := strconv.Atoi(raw)
		if err != nil || v < 1 || v > movieDiscoverMaxPage {
			respondError(c, http.StatusBadRequest, "invalid_page", "page must be in [1,500]")
			return "", 0, false
		}
		page = v
	}
	return lang, page, true
}

// parse binds /discovery/movie/discover query params into a
// MovieDiscoverFilter and validates page + lang. On error, writes the F-2c
// envelope and returns ok=false.
func (h *MovieDiscoverHandler) parse(c *gin.Context) (tmdb.MovieDiscoverFilter, string, int, bool) {
	lang := c.DefaultQuery("lang", defaultLang)
	if !validateLang(lang) {
		respondError(c, http.StatusBadRequest, "invalid_filter", "lang must be BCP-47")
		return tmdb.MovieDiscoverFilter{}, "", 0, false
	}
	page := 1
	if raw := c.Query("page"); raw != "" {
		v, err := strconv.Atoi(raw)
		if err != nil || v < 1 || v > movieDiscoverMaxPage {
			respondError(c, http.StatusBadRequest, "invalid_filter", "page must be in [1,500]")
			return tmdb.MovieDiscoverFilter{}, "", 0, false
		}
		page = v
	}

	filter := tmdb.MovieDiscoverFilter{}
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
	parseIntList("with_keywords", &filter.WithKeywords, 1, 1_000_000)
	parseIntList("without_keywords", &filter.WithoutKeywords, 1, 1_000_000)
	parseIntList("with_watch_providers", &filter.WithWatchProviders, 1, 1_000_000)
	parseIntList("with_release_type", &filter.WithReleaseType, movieReleaseTypeMin, movieReleaseTypeMax)

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
	parseStringPtr("primary_release_date.gte", &filter.PrimaryReleaseDateGte)
	parseStringPtr("primary_release_date.lte", &filter.PrimaryReleaseDateLte)
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
	parseIntPtr("primary_release_year", &filter.PrimaryReleaseYear, 1800, 9999)

	if raw := strings.TrimSpace(c.Query("sort_by")); raw != "" {
		switch raw {
		case "popularity.desc", "vote_average.desc",
			"primary_release_date.desc", "primary_release_date.asc", "revenue.desc":
			filter.SortBy = raw
		default:
			bindErr = "sort_by"
		}
	}

	if bindErr != "" {
		respondError(c, http.StatusBadRequest, "invalid_filter", bindErr+" failed validation")
		return tmdb.MovieDiscoverFilter{}, "", 0, false
	}
	return filter, lang, page, true
}
