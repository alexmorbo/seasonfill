package observability

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestIncRecPosterSentinel_RegistersPerReason(t *testing.T) {
	IncRecPosterSentinel(RecPosterSentinelNoRow)
	IncRecPosterSentinel(RecPosterSentinelNoRow)
	IncRecPosterSentinel(RecPosterSentinelEmptyPosterRow)
	IncRecPosterSentinel(RecPosterSentinelResolverMiss)

	body := writeAndRead(t)
	assert.Contains(t, body, `seasonfill_rec_poster_sentinel_total{reason="no_row"} 2`)
	assert.Contains(t, body, `seasonfill_rec_poster_sentinel_total{reason="empty_poster_row"} 1`)
	assert.Contains(t, body, `seasonfill_rec_poster_sentinel_total{reason="resolver_miss"} 1`)
}

func TestMetricRecPosterSentinel_ConstShape(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "seasonfill_rec_poster_sentinel_total", MetricRecPosterSentinel)
	assert.Equal(t, "no_row", RecPosterSentinelNoRow)
	assert.Equal(t, "empty_poster_row", RecPosterSentinelEmptyPosterRow)
	assert.Equal(t, "resolver_miss", RecPosterSentinelResolverMiss)
}
