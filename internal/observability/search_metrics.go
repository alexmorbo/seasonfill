package observability

import (
	"time"

	"github.com/VictoriaMetrics/metrics"
)

// ADR-0025 F3 — observability for the universal-search bounded context
// (ADR-0024). The bc shipped 2026-08-27 with ZERO metric families while
// prod exported 165 other seasonfill_* families; the one perf incident it
// had (ADR-0024 BUG-2: people search at 12s, OR-EXISTS killing the index)
// was found by a human, not by a series.
//
// That incident dictates the shape of this file: latency is recorded
// PER ENTITY GROUP, not only per request. A 12-second people query inside
// a request whose other three groups answer in ~100ms moves the
// request-level mean by a factor the eye reads as noise; the group-level
// series shows it as one line 100x above the other three.
//
// Every label value is a compile-time literal from the call site and is
// funnelled through a sanitizer that maps anything unknown onto the
// "unknown" member of the same closed enum. NEVER a label: the query q,
// the BCP-47 lang (an open set), limit, a user id, an entity id, or an
// error string.

// Metric names (frozen).
const (
	MetricSearchRequestsTotal          = "seasonfill_search_requests_total"
	MetricSearchRequestDurationSeconds = "seasonfill_search_request_duration_seconds"
	MetricSearchGroupQueriesTotal      = "seasonfill_search_group_queries_total"
	MetricSearchGroupDurationSeconds   = "seasonfill_search_group_duration_seconds"
)

// scope label values (closed). Mirror the rest-layer scope enum.
const (
	SearchScopeLibrary = "library"
	SearchScopeCatalog = "catalog"
	SearchScopeAll     = "all"
	SearchScopeUnknown = "unknown"
)

// entity label values (closed). Mirror the four groups of
// searchdomain.LibrarySearchResult.
const (
	SearchEntitySeries      = "series"
	SearchEntityMovies      = "movies"
	SearchEntityCollections = "collections"
	SearchEntityPeople      = "people"
	SearchEntityUnknown     = "unknown"
)

// source label values (closed). Which layer answered the group.
const (
	SearchSourceLibrary = "library"
	SearchSourceCatalog = "catalog"
	SearchSourceUnknown = "unknown"
)

// result label values (closed). "empty" is a first-class outcome, not an
// error: the empty-hit RATIO is a PromQL expression over this label
// (empty / (hits+empty)) rather than a second metric family.
const (
	SearchResultHits    = "hits"
	SearchResultEmpty   = "empty"
	SearchResultError   = "error"
	SearchResultUnknown = "unknown"
)

// SearchResultOf maps an (any hits?, error?) pair onto the closed result
// enum. An error wins over emptiness — a failed query produced no hits for
// a reason that is not "nothing matched".
func SearchResultOf(hits bool, err error) string {
	switch {
	case err != nil:
		return SearchResultError
	case hits:
		return SearchResultHits
	default:
		return SearchResultEmpty
	}
}

func searchScopeLabel(v string) string {
	switch v {
	case SearchScopeLibrary, SearchScopeCatalog, SearchScopeAll:
		return v
	default:
		return SearchScopeUnknown
	}
}

func searchEntityLabel(v string) string {
	switch v {
	case SearchEntitySeries, SearchEntityMovies, SearchEntityCollections, SearchEntityPeople:
		return v
	default:
		return SearchEntityUnknown
	}
}

func searchSourceLabel(v string) string {
	switch v {
	case SearchSourceLibrary, SearchSourceCatalog:
		return v
	default:
		return SearchSourceUnknown
	}
}

func searchResultLabel(v string) string {
	switch v {
	case SearchResultHits, SearchResultEmpty, SearchResultError:
		return v
	default:
		return SearchResultUnknown
	}
}

// ObserveSearchRequest records ONE end-to-end GET /api/v1/search execution:
// the per-(scope,result) counter and the per-scope wall-clock histogram.
// Called from the rest handler around the use-case call, so it covers the
// whole dispatch (library + catalog + merge). Rejected requests (400) are
// deliberately NOT counted here — this family measures searches that ran.
func ObserveSearchRequest(scope, result string, d time.Duration) {
	s := searchScopeLabel(scope)
	metrics.GetOrCreateCounter(
		MetricSearchRequestsTotal + `{scope="` + s +
			`",result="` + searchResultLabel(result) + `"}`).Inc()
	metrics.GetOrCreateHistogram(
		MetricSearchRequestDurationSeconds + `{scope="` + s + `"}`).Update(d.Seconds())
}

// ObserveSearchGroup records ONE per-entity-group query: the
// per-(entity,source,result) counter and the per-(entity,source) latency
// histogram. This is the BUG-2 catcher — it is the only family in which a
// slow people query is visible as a people query.
//
// Call it around the single backing call for one group: the four
// uc.repo.Search* calls in UnifiedSearchUseCase.searchLibrary
// (source=library) and the four TMDB fan-out branches in
// catalog.Adapter.SearchCatalog (source=catalog).
func ObserveSearchGroup(entity, source, result string, d time.Duration) {
	e := searchEntityLabel(entity)
	src := searchSourceLabel(source)
	metrics.GetOrCreateCounter(
		MetricSearchGroupQueriesTotal + `{entity="` + e + `",source="` + src +
			`",result="` + searchResultLabel(result) + `"}`).Inc()
	metrics.GetOrCreateHistogram(
		MetricSearchGroupDurationSeconds + `{entity="` + e +
			`",source="` + src + `"}`).Update(d.Seconds())
}
