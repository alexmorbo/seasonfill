package observability

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestIncGridPosterSentinel_RegistersPerReason(t *testing.T) {
	IncGridPosterSentinel(GridPosterSentinelNoRow)
	IncGridPosterSentinel(GridPosterSentinelNoRow)
	IncGridPosterSentinel(GridPosterSentinelNoRow)
	IncGridPosterSentinel(GridPosterSentinelEmptyPosterRow)
	IncGridPosterSentinel(GridPosterSentinelResolverMiss)

	body := writeAndRead(t)
	assert.Contains(t, body, `seasonfill_grid_poster_sentinel_total{reason="no_row"} 3`)
	assert.Contains(t, body, `seasonfill_grid_poster_sentinel_total{reason="empty_poster_row"} 1`)
	assert.Contains(t, body, `seasonfill_grid_poster_sentinel_total{reason="resolver_miss"} 1`)
}

func TestMetricGridPosterSentinel_ConstShape(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "seasonfill_grid_poster_sentinel_total", MetricGridPosterSentinel)
	assert.Equal(t, "no_row", GridPosterSentinelNoRow)
	assert.Equal(t, "empty_poster_row", GridPosterSentinelEmptyPosterRow)
	assert.Equal(t, "resolver_miss", GridPosterSentinelResolverMiss)
}
