// movie_canonical_hash.go ships the deterministic cache-key builder for the
// /discovery/movie/discover LRU (Ф6-R-4a). Movie analog of canonical_hash.go:
// folds a MovieDiscoverFilter + lang + page tuple into one sha256-hex digest
// with the same two invariants — key-order independence (alphabetical param
// sort) and slice-order independence (multi-value filters sorted ascending
// before joining). Reuses joinSorted (same rest package) — no duplication.
//
// R-4a has NO movie keyword blocklist, so there is no bl_epoch fold (that is a
// later Ф6 story); the signature drops the blEpoch arg the TV builder carries.
package rest

import (
	"crypto/sha256"
	"encoding/hex"
	"net/url"
	"sort"
	"strconv"

	"github.com/alexmorbo/seasonfill/internal/shared/clients/tmdb"
)

// movieCanonicalHash returns the sha256-hex digest of a canonical URL-encoded
// query string built from (filter, lang, page). Stable across runs, Go
// versions, and field declaration order.
func movieCanonicalHash(filter tmdb.MovieDiscoverFilter, lang string, page int) string {
	params := buildMovieCanonicalParams(filter, lang, page)

	keys := make([]string, 0, len(params))
	for k := range params {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	out := url.Values{}
	for _, k := range keys {
		out.Set(k, params[k])
	}
	sum := sha256.Sum256([]byte(out.Encode()))
	return hex.EncodeToString(sum[:])
}

// buildMovieCanonicalParams flattens MovieDiscoverFilter into a string map,
// omitting empty fields. Multi-value slices are sorted ascending before
// joining. with_release_type OR-joins with pipe (mirror of the wire query).
func buildMovieCanonicalParams(filter tmdb.MovieDiscoverFilter, lang string, page int) map[string]string {
	m := make(map[string]string, 20)
	m["lang"] = lang
	m["page"] = strconv.Itoa(page)

	if len(filter.WithGenres) > 0 {
		m["with_genres"] = joinSorted(filter.WithGenres, ",")
	}
	if len(filter.WithoutGenres) > 0 {
		m["without_genres"] = joinSorted(filter.WithoutGenres, ",")
	}
	if filter.PrimaryReleaseDateGte != nil {
		m["primary_release_date.gte"] = *filter.PrimaryReleaseDateGte
	}
	if filter.PrimaryReleaseDateLte != nil {
		m["primary_release_date.lte"] = *filter.PrimaryReleaseDateLte
	}
	if filter.PrimaryReleaseYear != nil {
		m["primary_release_year"] = strconv.Itoa(*filter.PrimaryReleaseYear)
	}
	if filter.VoteAverageGte != nil {
		m["vote_average.gte"] = strconv.FormatFloat(*filter.VoteAverageGte, 'f', -1, 64)
	}
	if filter.VoteAverageLte != nil {
		m["vote_average.lte"] = strconv.FormatFloat(*filter.VoteAverageLte, 'f', -1, 64)
	}
	if filter.VoteCountGte != nil {
		m["vote_count.gte"] = strconv.Itoa(*filter.VoteCountGte)
	}
	if filter.WithRuntimeGte != nil {
		m["with_runtime.gte"] = strconv.Itoa(*filter.WithRuntimeGte)
	}
	if filter.WithRuntimeLte != nil {
		m["with_runtime.lte"] = strconv.Itoa(*filter.WithRuntimeLte)
	}
	if filter.WithOriginalLang != nil && *filter.WithOriginalLang != "" {
		m["with_original_language"] = *filter.WithOriginalLang
	}
	if filter.WithOriginCountry != nil && *filter.WithOriginCountry != "" {
		m["with_origin_country"] = *filter.WithOriginCountry
	}
	if len(filter.WithKeywords) > 0 {
		m["with_keywords"] = joinSorted(filter.WithKeywords, ",")
	}
	if len(filter.WithoutKeywords) > 0 {
		m["without_keywords"] = joinSorted(filter.WithoutKeywords, ",")
	}
	if len(filter.WithWatchProviders) > 0 {
		m["with_watch_providers"] = joinSorted(filter.WithWatchProviders, ",")
	}
	if filter.WatchRegion != nil && *filter.WatchRegion != "" {
		m["watch_region"] = *filter.WatchRegion
	}
	if len(filter.WithReleaseType) > 0 {
		m["with_release_type"] = joinSorted(filter.WithReleaseType, "|")
	}
	if filter.SortBy != "" {
		m["sort_by"] = filter.SortBy
	}
	return m
}
