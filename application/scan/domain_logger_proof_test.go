package scan

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestCascadeSeriesDelete_NilLogger_EmitsDomainScan is the F-4b-1 proof
// (Story 392): when CascadeSeriesDelete is called with deps.Logger=nil,
// the fallback path wraps slog.Default() via ports.DomainLogger(..., "scan")
// so every record emitted by this helper carries domain="scan".
func TestCascadeSeriesDelete_NilLogger_EmitsDomainScan(t *testing.T) {
	var buf bytes.Buffer
	prev := slog.Default()
	t.Cleanup(func() { slog.SetDefault(prev) })
	slog.SetDefault(slog.New(slog.NewJSONHandler(&buf, nil)))

	cache := &cascadeFakeCache{}
	eps := &cascadeFakeEpisodes{rowsToReturn: 3}
	tx := &cascadeFakeTx{}

	cacheDeleted, rows, _, err := CascadeSeriesDelete(context.Background(), CascadeDeleteDeps{
		SeriesCache:   cache,
		EpisodeStates: eps,
		Tx:            tx,
		Logger:        nil,
	}, "alpha", 42)
	require.NoError(t, err)
	assert.True(t, cacheDeleted)
	assert.Equal(t, 3, rows)
	assert.Equal(t, int32(1), atomic.LoadInt32(&cache.softDeleteCalls))

	out := buf.String()
	t.Logf("captured slog output (proof artifact): %s", out)
	assert.True(t, strings.Contains(out, `"domain":"scan"`),
		"expected log record with domain=\"scan\" when deps.Logger=nil; got: %s", out)
}
