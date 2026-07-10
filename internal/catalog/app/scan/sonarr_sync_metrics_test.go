package scan

import (
	"bytes"
	"context"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/internal/observability"
	"github.com/alexmorbo/seasonfill/internal/shared/clients/sonarr"
)

// counterValue extracts the integer value of an exact
// `name{labels}` counter line from a Prometheus text dump, or 0 if the
// metric has not been registered yet.
func counterValue(t *testing.T, body, line string) int {
	t.Helper()
	for l := range strings.SplitSeq(body, "\n") {
		if strings.HasPrefix(l, line+" ") {
			fields := strings.Fields(l)
			v, err := strconv.Atoi(fields[len(fields)-1])
			require.NoError(t, err)
			return v
		}
	}
	return 0
}

func dumpMetrics(t *testing.T) string {
	t.Helper()
	buf := &bytes.Buffer{}
	observability.WritePrometheus(buf)
	return buf.String()
}

// TestSyncSeriesFromSonarr_RowsWrittenCounter drives two full per-series cache
// writes and asserts seasonfill_sonarr_sync_rows_written_total{table="series_cache"}
// advanced by exactly 2 (one row per successful SyncSeriesFromSonarr call).
func TestSyncSeriesFromSonarr_RowsWrittenCounter(t *testing.T) {
	ctx := context.Background()
	const line = `seasonfill_sonarr_sync_rows_written_total{table="series_cache"}`

	deps, _ := newDerivationDeps(t)

	before := counterValue(t, dumpMetrics(t), line)

	// Two DISTINCT series (distinct TVDBID → distinct canon rows → two
	// series_cache upserts). No episodes/PostSync — deterministic cache-write
	// path only.
	_, err := SyncSeriesFromSonarr(ctx, deps, "sonarr-main", SonarrPayloadBundle{
		Series: sonarr.SeriesPayload{ID: 701, TVDBID: 700701, Title: "Series One", Monitored: true},
	})
	require.NoError(t, err)
	_, err = SyncSeriesFromSonarr(ctx, deps, "sonarr-main", SonarrPayloadBundle{
		Series: sonarr.SeriesPayload{ID: 702, TVDBID: 700702, Title: "Series Two", Monitored: true},
	})
	require.NoError(t, err)

	after := counterValue(t, dumpMetrics(t), line)
	assert.Equal(t, before+2, after, "each successful sonarr sync bumps the series_cache rows-written counter by 1")
}
