package observability

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// ADR-0025 F3. VictoriaMetrics owns a process-global default set, so these
// tests must NOT call t.Parallel() and must assert DELTAS (seriesValue
// before/after), exactly like auth_metrics_test.go — an absolute value would
// depend on whichever sibling test ran first.

func TestSearchMetricNames_Frozen(t *testing.T) {
	assert.Equal(t, "seasonfill_search_requests_total", MetricSearchRequestsTotal)
	assert.Equal(t, "seasonfill_search_request_duration_seconds", MetricSearchRequestDurationSeconds)
	assert.Equal(t, "seasonfill_search_group_queries_total", MetricSearchGroupQueriesTotal)
	assert.Equal(t, "seasonfill_search_group_duration_seconds", MetricSearchGroupDurationSeconds)
}

func TestSearchResultOf(t *testing.T) {
	assert.Equal(t, SearchResultError, SearchResultOf(true, errors.New("boom")),
		"an error wins over hits")
	assert.Equal(t, SearchResultHits, SearchResultOf(true, nil))
	assert.Equal(t, SearchResultEmpty, SearchResultOf(false, nil))
}

func TestObserveSearchRequest_CounterAndHistogram(t *testing.T) {
	for _, scope := range []string{SearchScopeLibrary, SearchScopeCatalog, SearchScopeAll} {
		for _, result := range []string{SearchResultHits, SearchResultEmpty, SearchResultError} {
			counter := MetricSearchRequestsTotal +
				`{scope="` + scope + `",result="` + result + `"}`
			hist := MetricSearchRequestDurationSeconds + `_count{scope="` + scope + `"}`

			beforeCounter := seriesValue(t, writeAndRead(t), counter)
			beforeHist := seriesValue(t, writeAndRead(t), hist)

			ObserveSearchRequest(scope, result, 25*time.Millisecond)

			body := writeAndRead(t)
			assert.Equal(t, beforeCounter+1, seriesValue(t, body, counter),
				"series %s must increment by 1", counter)
			assert.Equal(t, beforeHist+1, seriesValue(t, body, hist),
				"series %s must increment by 1", hist)
		}
	}
}

func TestObserveSearchGroup_AllEntitySourceCombos(t *testing.T) {
	entities := []string{
		SearchEntitySeries, SearchEntityMovies, SearchEntityCollections, SearchEntityPeople,
	}
	for _, entity := range entities {
		for _, source := range []string{SearchSourceLibrary, SearchSourceCatalog} {
			counter := MetricSearchGroupQueriesTotal + `{entity="` + entity +
				`",source="` + source + `",result="` + SearchResultHits + `"}`
			hist := MetricSearchGroupDurationSeconds +
				`_count{entity="` + entity + `",source="` + source + `"}`

			beforeCounter := seriesValue(t, writeAndRead(t), counter)
			beforeHist := seriesValue(t, writeAndRead(t), hist)

			ObserveSearchGroup(entity, source, SearchResultHits, 12*time.Millisecond)

			body := writeAndRead(t)
			assert.Equal(t, beforeCounter+1, seriesValue(t, body, counter),
				"series %s must increment by 1", counter)
			assert.Equal(t, beforeHist+1, seriesValue(t, body, hist),
				"series %s must increment by 1", hist)
		}
	}
}

// TestSearchGroupDuration_RecordsSeconds proves the histogram carries the
// LATENCY, not just a count — the whole point of the family (BUG-2: people at
// 12s). _sum is asserted, because a _count-only check would still pass if the
// duration argument were dropped.
func TestSearchGroupDuration_RecordsSeconds(t *testing.T) {
	sum := MetricSearchGroupDurationSeconds +
		`_sum{entity="` + SearchEntityPeople + `",source="` + SearchSourceLibrary + `"}`
	before := seriesValue(t, writeAndRead(t), sum)
	ObserveSearchGroup(SearchEntityPeople, SearchSourceLibrary, SearchResultHits, 12*time.Second)
	got := seriesValue(t, writeAndRead(t), sum)
	assert.InDelta(t, before+12, got, 0.5, "the 12s observation must land in %s", sum)
}

// TestSearchLabels_UnknownInputFoldsIntoClosedSet is the cardinality guard. An
// out-of-enum value must NOT mint a new series: it folds onto the "unknown"
// member of its own enum. The injection payload doubles as a safety check —
// unsanitized concatenation would either mint an attacker-named series or make
// VictoriaMetrics panic on an invalid metric name.
func TestSearchLabels_UnknownInputFoldsIntoClosedSet(t *testing.T) {
	const payload = `x",leak="pwned`

	unknownReq := MetricSearchRequestsTotal +
		`{scope="` + SearchScopeUnknown + `",result="` + SearchResultUnknown + `"}`
	unknownGroup := MetricSearchGroupQueriesTotal +
		`{entity="` + SearchEntityUnknown + `",source="` + SearchSourceUnknown +
		`",result="` + SearchResultUnknown + `"}`

	beforeReq := seriesValue(t, writeAndRead(t), unknownReq)
	beforeGroup := seriesValue(t, writeAndRead(t), unknownGroup)

	ObserveSearchRequest(payload, payload, time.Millisecond)
	ObserveSearchGroup(payload, payload, payload, time.Millisecond)

	body := writeAndRead(t)
	assert.Equal(t, beforeReq+1, seriesValue(t, body, unknownReq),
		"an unknown scope/result must fold into %s", unknownReq)
	assert.Equal(t, beforeGroup+1, seriesValue(t, body, unknownGroup),
		"an unknown entity/source/result must fold into %s", unknownGroup)
	assert.NotContains(t, body, "pwned",
		"a label value from outside the closed enum reached the exposition — "+
			"the sanitizer is not doing its job and cardinality is unbounded")

	for line := range strings.SplitSeq(body, "\n") {
		if strings.HasPrefix(line, "seasonfill_search_") {
			assert.NotContains(t, line, "leak=",
				"unexpected label key in %s", line)
		}
	}
}
