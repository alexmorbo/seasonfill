// canonical_hash.go ships the deterministic cache-key builder for the
// /discovery/discover LRU (story 509 N-2h). The builder folds an 18-field
// DiscoverFilter + lang + page tuple into a single sha256-hex digest, with
// two strict invariants:
//
//  1. KEY-ORDER INDEPENDENCE: callers cannot influence the key by re-
//     ordering fields in the Go struct. The serialiser sorts alphabetically
//     by URL parameter name before hashing.
//
//  2. SLICE-ORDER INDEPENDENCE: multi-value filters (with_genres,
//     with_networks, with_keywords, …) are sorted ascending before
//     joining. `with_genres=18,35` and `with_genres=35,18` produce the
//     SAME hash — TMDB's set-semantics make the orderings equivalent at
//     the API level, and we don't want two ?with_genres= permutations
//     to cache-miss separately.
//
// The hash includes lang + page because the same filter at page 1 vs page
// 2 yields different result sets — a single key cannot collapse them.
//
// Output: lowercase hex (sha256 → 64 chars). The cache stores values
// keyed by this string; no further transformation needed.
package rest

import (
	"crypto/sha256"
	"encoding/hex"
	"net/url"
	"sort"
	"strconv"
	"strings"

	"github.com/alexmorbo/seasonfill/internal/shared/clients/tmdb"
)

// canonicalHash returns the sha256-hex digest of a canonical URL-encoded
// query string built from (filter, lang, page). The output is stable
// across runs, Go versions, and field declaration order.
func canonicalHash(filter tmdb.DiscoverFilter, lang string, page int) string {
	params := buildCanonicalParams(filter, lang, page)

	// Sort by key alphabetically for stable serialisation.
	keys := make([]string, 0, len(params))
	for k := range params {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	// Re-encode via url.Values so values are escaped identically to the
	// outbound TMDB URL — keeps the canonical-key semantics aligned with
	// what would actually hit upstream if we bypassed the cache.
	out := url.Values{}
	for _, k := range keys {
		out.Set(k, params[k])
	}
	sum := sha256.Sum256([]byte(out.Encode()))
	return hex.EncodeToString(sum[:])
}

// buildCanonicalParams flattens DiscoverFilter into a string map, omitting
// empty fields. Multi-value slices are sorted ascending before joining to
// guarantee that `[18,35]` and `[35,18]` hash identically. The join
// separator mirrors buildDiscoverQuery's choice (comma for AND, pipe for
// OR on with_status/with_type) so the canonical key stays semantically
// aligned with the wire request.
func buildCanonicalParams(filter tmdb.DiscoverFilter, lang string, page int) map[string]string {
	m := make(map[string]string, 22)
	m["lang"] = lang
	m["page"] = strconv.Itoa(page)

	if len(filter.WithGenres) > 0 {
		m["with_genres"] = joinSorted(filter.WithGenres, ",")
	}
	if len(filter.WithoutGenres) > 0 {
		m["without_genres"] = joinSorted(filter.WithoutGenres, ",")
	}
	if filter.FirstAirDateGte != nil {
		m["first_air_date.gte"] = *filter.FirstAirDateGte
	}
	if filter.FirstAirDateLte != nil {
		m["first_air_date.lte"] = *filter.FirstAirDateLte
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
	if len(filter.WithNetworks) > 0 {
		m["with_networks"] = joinSorted(filter.WithNetworks, ",")
	}
	if filter.WithOriginCountry != nil && *filter.WithOriginCountry != "" {
		m["with_origin_country"] = *filter.WithOriginCountry
	}
	if len(filter.WithKeywords) > 0 {
		m["with_keywords"] = joinSorted(filter.WithKeywords, ",")
	}
	if len(filter.WithWatchProviders) > 0 {
		m["with_watch_providers"] = joinSorted(filter.WithWatchProviders, ",")
	}
	if filter.WatchRegion != nil && *filter.WatchRegion != "" {
		m["watch_region"] = *filter.WatchRegion
	}
	if len(filter.WithStatus) > 0 {
		sep := ","
		if filter.WithStatusOp == "" || filter.WithStatusOp == "or" {
			sep = "|"
		}
		m["with_status"] = joinSorted(filter.WithStatus, sep)
		m["with_status_op"] = normaliseOp(filter.WithStatusOp)
	}
	if len(filter.WithType) > 0 {
		sep := ","
		if filter.WithTypeOp == "" || filter.WithTypeOp == "or" {
			sep = "|"
		}
		m["with_type"] = joinSorted(filter.WithType, sep)
		m["with_type_op"] = normaliseOp(filter.WithTypeOp)
	}
	if filter.SortBy != "" {
		m["sort_by"] = filter.SortBy
	}
	return m
}

// joinSorted renders a sorted-ascending int slice as sep-joined digits.
// Mutates a copy — caller's slice is untouched.
func joinSorted(xs []int, sep string) string {
	cp := append([]int(nil), xs...)
	sort.Ints(cp)
	var b strings.Builder
	for i, x := range cp {
		if i > 0 {
			b.WriteString(sep)
		}
		b.WriteString(strconv.Itoa(x))
	}
	return b.String()
}

// normaliseOp coerces the WithStatusOp/WithTypeOp field to the closed set
// {"and","or"}. Empty / unknown → "or" (TMDB default).
func normaliseOp(op string) string {
	if op == "and" {
		return "and"
	}
	return "or"
}
