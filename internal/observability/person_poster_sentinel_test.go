package observability

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestIncPersonPosterSentinel_RegistersPerReason(t *testing.T) {
	IncPersonPosterSentinel(PersonPosterSentinelNoRow)
	IncPersonPosterSentinel(PersonPosterSentinelEmptyPosterRow)
	IncPersonPosterSentinel(PersonPosterSentinelEmptyPosterRow)
	IncPersonPosterSentinel(PersonPosterSentinelResolverMiss)

	body := writeAndRead(t)
	assert.Contains(t, body, `seasonfill_person_poster_sentinel_total{reason="no_row"} 1`)
	assert.Contains(t, body, `seasonfill_person_poster_sentinel_total{reason="empty_poster_row"} 2`)
	assert.Contains(t, body, `seasonfill_person_poster_sentinel_total{reason="resolver_miss"} 1`)
}

func TestMetricPersonPosterSentinel_ConstShape(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "seasonfill_person_poster_sentinel_total", MetricPersonPosterSentinel)
	assert.Equal(t, "no_row", PersonPosterSentinelNoRow)
	assert.Equal(t, "empty_poster_row", PersonPosterSentinelEmptyPosterRow)
	assert.Equal(t, "resolver_miss", PersonPosterSentinelResolverMiss)
}
