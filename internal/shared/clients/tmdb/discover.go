package tmdb

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"strings"
)

// Trending fetches /trending/tv/{scope} where scope ∈ {day, week}. Page is
// 1-based; caller bounds at 500 per TMDB cap.
func (c *Client) Trending(ctx context.Context, scope TrendingScope, language string, page int) (*TVListResponse, error) {
	if scope != TrendingDay && scope != TrendingWeek {
		return nil, fmt.Errorf("tmdb: invalid trending scope %q (want day|week)", scope)
	}
	q := url.Values{}
	q.Set("language", c.languageFor(language))
	q.Set("page", strconv.Itoa(pageOrOne(page)))
	return c.fetchTVList(ctx, "/trending/tv/"+string(scope), q, "Trending")
}

// Popular fetches /tv/popular.
func (c *Client) Popular(ctx context.Context, language string, page int) (*TVListResponse, error) {
	q := url.Values{}
	q.Set("language", c.languageFor(language))
	q.Set("page", strconv.Itoa(pageOrOne(page)))
	return c.fetchTVList(ctx, "/tv/popular", q, "Popular")
}

// DiscoverTV fetches /discover/tv with the allow-listed filter parameters.
// Language is taken from the client's default; the filter struct does NOT
// carry a language field (matches PRD §5.1.2 — Discover stays on the
// client's default language).
func (c *Client) DiscoverTV(ctx context.Context, filter DiscoverFilter, page int) (*TVListResponse, error) {
	q := buildDiscoverQuery(filter, c.languageFor(""), page)
	return c.fetchTVList(ctx, "/discover/tv", q, "DiscoverTV")
}

// SearchTV fetches /search/tv?query=…
func (c *Client) SearchTV(ctx context.Context, query, language string, page int) (*TVListResponse, error) {
	if strings.TrimSpace(query) == "" {
		return nil, fmt.Errorf("tmdb: SearchTV: empty query")
	}
	q := url.Values{}
	q.Set("query", query)
	q.Set("language", c.languageFor(language))
	q.Set("page", strconv.Itoa(pageOrOne(page)))
	q.Set("include_adult", "false")
	return c.fetchTVList(ctx, "/search/tv", q, "SearchTV")
}

// fetchTVList is the shared parse/error path for the four list endpoints.
// `opName` shows up in the wrapped error so callers can locate the failure
// in stack traces without parsing path strings.
func (c *Client) fetchTVList(ctx context.Context, path string, q url.Values, opName string) (*TVListResponse, error) {
	body, err := c.do(ctx, path, q)
	if err != nil {
		return nil, fmt.Errorf("tmdb: %s: %w", opName, err)
	}
	var out TVListResponse
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("tmdb: decode %s: %w", opName, err)
	}
	return &out, nil
}

// buildDiscoverQuery serialises a DiscoverFilter into the canonical
// /discover/tv query string per PRD §5.1.2. include_adult is hardcoded
// false; page is forced to ≥1. Empty/nil filter fields are omitted from
// the URL — TMDB applies its own defaults when the param is absent.
//
// Multi-value joining: comma (`,`) is AND, pipe (`|`) is OR. Only
// with_status / with_type honour the op override; every other multi-value
// field uses AND (TMDB API treats them as set membership).
func buildDiscoverQuery(filter DiscoverFilter, lang string, page int) url.Values {
	q := url.Values{}
	q.Set("language", lang)
	q.Set("page", strconv.Itoa(pageOrOne(page)))
	q.Set("include_adult", "false")

	if len(filter.WithGenres) > 0 {
		q.Set("with_genres", joinInts(filter.WithGenres, ","))
	}
	if len(filter.WithoutGenres) > 0 {
		q.Set("without_genres", joinInts(filter.WithoutGenres, ","))
	}
	if filter.FirstAirDateGte != nil {
		q.Set("first_air_date.gte", *filter.FirstAirDateGte)
	}
	if filter.FirstAirDateLte != nil {
		q.Set("first_air_date.lte", *filter.FirstAirDateLte)
	}
	if filter.VoteAverageGte != nil {
		q.Set("vote_average.gte", strconv.FormatFloat(*filter.VoteAverageGte, 'f', -1, 64))
	}
	if filter.VoteAverageLte != nil {
		q.Set("vote_average.lte", strconv.FormatFloat(*filter.VoteAverageLte, 'f', -1, 64))
	}
	if filter.VoteCountGte != nil {
		q.Set("vote_count.gte", strconv.Itoa(*filter.VoteCountGte))
	}
	if filter.WithRuntimeGte != nil {
		q.Set("with_runtime.gte", strconv.Itoa(*filter.WithRuntimeGte))
	}
	if filter.WithRuntimeLte != nil {
		q.Set("with_runtime.lte", strconv.Itoa(*filter.WithRuntimeLte))
	}
	if filter.WithOriginalLang != nil && *filter.WithOriginalLang != "" {
		q.Set("with_original_language", *filter.WithOriginalLang)
	}
	if len(filter.WithNetworks) > 0 {
		q.Set("with_networks", joinInts(filter.WithNetworks, ","))
	}
	if filter.WithOriginCountry != nil && *filter.WithOriginCountry != "" {
		q.Set("with_origin_country", *filter.WithOriginCountry)
	}
	if len(filter.WithKeywords) > 0 {
		q.Set("with_keywords", joinInts(filter.WithKeywords, ","))
	}
	if len(filter.WithWatchProviders) > 0 {
		q.Set("with_watch_providers", joinInts(filter.WithWatchProviders, ","))
	}
	if filter.WatchRegion != nil && *filter.WatchRegion != "" {
		q.Set("watch_region", *filter.WatchRegion)
	}
	if len(filter.WithStatus) > 0 {
		q.Set("with_status", joinInts(filter.WithStatus, opSeparator(filter.WithStatusOp)))
	}
	if len(filter.WithType) > 0 {
		q.Set("with_type", joinInts(filter.WithType, opSeparator(filter.WithTypeOp)))
	}
	if filter.SortBy != "" {
		q.Set("sort_by", filter.SortBy)
	}
	return q
}

// opSeparator translates the "and"|"or" op label to TMDB's URL separator:
// comma (`,`) for AND, pipe (`|`) for OR. Empty / unknown → "|" (OR — the
// TMDB API default for with_status/with_type, per docs).
func opSeparator(op string) string {
	if strings.EqualFold(op, "and") {
		return ","
	}
	return "|"
}

// joinInts renders an int slice as `sep`-joined ASCII digits without
// allocating an intermediate []string. Returns "" for an empty slice
// (callers gate on len() > 0 before calling).
func joinInts(xs []int, sep string) string {
	if len(xs) == 0 {
		return ""
	}
	var b strings.Builder
	for i, x := range xs {
		if i > 0 {
			b.WriteString(sep)
		}
		b.WriteString(strconv.Itoa(x))
	}
	return b.String()
}

// pageOrOne clamps a TMDB page argument to ≥1. TMDB returns 422 on
// page < 1; clients should still defend against accidental 0.
func pageOrOne(page int) int {
	if page < 1 {
		return 1
	}
	return page
}
