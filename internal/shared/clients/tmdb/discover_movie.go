package tmdb

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"strings"
)

// TrendingMovie fetches /trending/movie/{window}, window ∈ {day, week}.
// Honors per-call language via c.languageFor (mirror of Trending; #1184).
func (c *Client) TrendingMovie(ctx context.Context, scope TrendingScope, language string, page int) (*MovieListResponse, error) {
	if scope != TrendingDay && scope != TrendingWeek {
		return nil, fmt.Errorf("tmdb: invalid trending scope %q (want day|week)", scope)
	}
	q := url.Values{}
	q.Set("language", c.languageFor(language))
	q.Set("page", strconv.Itoa(pageOrOne(page)))
	return c.fetchMovieList(ctx, "/trending/movie/"+string(scope), q, "TrendingMovie")
}

// MoviePopular fetches /movie/popular. Honors per-call language via
// c.languageFor (mirror of Popular; #1184).
func (c *Client) MoviePopular(ctx context.Context, language string, page int) (*MovieListResponse, error) {
	q := url.Values{}
	q.Set("language", c.languageFor(language))
	q.Set("page", strconv.Itoa(pageOrOne(page)))
	return c.fetchMovieList(ctx, "/movie/popular", q, "MoviePopular")
}

// DiscoverMovie fetches /discover/movie. Honors per-call language via
// c.languageFor(lang) exactly like DiscoverTV (issue #1184) so discover
// rows localize titles + posters to the requesting user.
func (c *Client) DiscoverMovie(ctx context.Context, filter MovieDiscoverFilter, lang string, page int) (*MovieListResponse, error) {
	q := buildMovieDiscoverQuery(filter, c.languageFor(lang), page)
	return c.fetchMovieList(ctx, "/discover/movie", q, "DiscoverMovie")
}

// SearchMovie fetches /search/movie?query=… Honors per-call language via
// c.languageFor (mirror of SearchTV; #1184). Empty query → error.
func (c *Client) SearchMovie(ctx context.Context, query, language string, page int) (*MovieListResponse, error) {
	if strings.TrimSpace(query) == "" {
		return nil, fmt.Errorf("tmdb: SearchMovie: empty query")
	}
	q := url.Values{}
	q.Set("query", query)
	q.Set("language", c.languageFor(language))
	q.Set("page", strconv.Itoa(pageOrOne(page)))
	q.Set("include_adult", "false")
	return c.fetchMovieList(ctx, "/search/movie", q, "SearchMovie")
}

// fetchMovieList is the shared parse/error path for the four movie list
// endpoints. opName shows up in the wrapped error so callers locate the
// failure without parsing path strings. Mirror of fetchTVList.
func (c *Client) fetchMovieList(ctx context.Context, path string, q url.Values, opName string) (*MovieListResponse, error) {
	body, err := c.do(ctx, path, q)
	if err != nil {
		return nil, fmt.Errorf("tmdb: %s: %w", opName, err)
	}
	var out MovieListResponse
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("tmdb: decode %s: %w", opName, err)
	}
	return &out, nil
}

// buildMovieDiscoverQuery serialises a MovieDiscoverFilter into the canonical
// /discover/movie query string. Mirror of buildDiscoverQuery: reuses
// joinInts (same package); include_adult hardcoded false; page forced ≥1;
// empty/nil filter fields omitted. with_release_type OR-joins with pipe.
func buildMovieDiscoverQuery(filter MovieDiscoverFilter, lang string, page int) url.Values {
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
	if filter.PrimaryReleaseDateGte != nil {
		q.Set("primary_release_date.gte", *filter.PrimaryReleaseDateGte)
	}
	if filter.PrimaryReleaseDateLte != nil {
		q.Set("primary_release_date.lte", *filter.PrimaryReleaseDateLte)
	}
	if filter.PrimaryReleaseYear != nil {
		q.Set("primary_release_year", strconv.Itoa(*filter.PrimaryReleaseYear))
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
	if filter.WithOriginCountry != nil && *filter.WithOriginCountry != "" {
		q.Set("with_origin_country", *filter.WithOriginCountry)
	}
	if len(filter.WithKeywords) > 0 {
		q.Set("with_keywords", joinInts(filter.WithKeywords, ","))
	}
	if len(filter.WithoutKeywords) > 0 {
		q.Set("without_keywords", joinInts(filter.WithoutKeywords, ","))
	}
	if len(filter.WithWatchProviders) > 0 {
		q.Set("with_watch_providers", joinInts(filter.WithWatchProviders, ","))
	}
	if filter.WatchRegion != nil && *filter.WatchRegion != "" {
		q.Set("watch_region", *filter.WatchRegion)
	}
	if len(filter.WithReleaseType) > 0 {
		q.Set("with_release_type", joinInts(filter.WithReleaseType, "|"))
	}
	if filter.SortBy != "" {
		q.Set("sort_by", filter.SortBy)
	}
	return q
}
