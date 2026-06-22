package wiring

import (
	"bytes"
	"context"
	"log/slog"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/cmd/server/adapters"
	"github.com/alexmorbo/seasonfill/internal/enrichment/domain/enrichment"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
)

// countingSeriesStaleScanner records how many times ListStaleForTMDB
// is called. Zero is the success signal for the empty-holder gate test
// (the gate must fire BEFORE this scanner runs).
type countingSeriesStaleScanner struct{ calls atomic.Int32 }

func (c *countingSeriesStaleScanner) ListStaleForTMDB(_ context.Context, _ time.Duration, _ int) ([]domain.SeriesID, error) {
	c.calls.Add(1)
	return nil, nil
}

type countingPeopleStaleScanner struct{ calls atomic.Int32 }

func (c *countingPeopleStaleScanner) ListStaleForTMDB(_ context.Context, _ time.Duration, _ int) ([]int64, error) {
	c.calls.Add(1)
	return nil, nil
}

// countingErrorRepo tracks ListDueForRetry calls. The other
// EnrichmentErrorRepo methods panic — the nightly tick must never reach
// them on the gated path, and a panic surfaces drift immediately rather
// than papering over a regression.
type countingErrorRepo struct {
	listCalls atomic.Int32
}

func (c *countingErrorRepo) ListDueForRetry(_ context.Context, _ enrichment.Source, _ time.Time, _ int) ([]enrichment.EnrichmentError, error) {
	c.listCalls.Add(1)
	return nil, nil
}

func (c *countingErrorRepo) RecordFailure(context.Context, enrichment.EnrichmentError) error {
	panic("RecordFailure must not be called from runNightlyTick gate path")
}

func (c *countingErrorRepo) ClearOnSuccess(context.Context, enrichment.EntityType, int64, enrichment.Source) error {
	panic("ClearOnSuccess must not be called from runNightlyTick gate path")
}

func (c *countingErrorRepo) GetForEntity(context.Context, enrichment.EntityType, int64) ([]enrichment.EnrichmentError, error) {
	panic("GetForEntity must not be called from runNightlyTick gate path")
}

func (c *countingErrorRepo) GetByEntitySource(context.Context, enrichment.EntityType, int64, enrichment.Source) (enrichment.EnrichmentError, error) {
	panic("GetByEntitySource must not be called from runNightlyTick gate path")
}

// TestRunNightlyTick_SkippedWhenHolderEmpty verifies the B-23 gate:
// when tmdbHolder.Load() returns nil (unconfigured install) the nightly
// stale rescan MUST short-circuit BEFORE touching any DB scanner or
// the enrichment_errors retry-list. The skip log must carry
// reason="tmdb_holder_empty" so operators on DEBUG can verify the gate
// fired.
func TestRunNightlyTick_SkippedWhenHolderEmpty(t *testing.T) {
	t.Parallel()

	holder := adapters.NewTMDBClientHolder() // empty — Load returns nil
	require.Nil(t, holder.Load(), "precondition: holder must be empty")

	series := &countingSeriesStaleScanner{}
	people := &countingPeopleStaleScanner{}
	errs := &countingErrorRepo{}

	var buf bytes.Buffer
	log := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	runNightlyTick(context.Background(), nightlyTickDeps{
		TMDBHolder:       holder,
		SeriesStaleScan:  series,
		PeopleStaleScan:  people,
		EnrichmentErrors: errs,
		Dispatcher:       nil, // gate fires before any Enqueue — nil-safe
		Log:              log,
	})

	assert.Equal(t, int32(0), series.calls.Load(),
		"series stale scanner MUST NOT be called when holder is empty")
	assert.Equal(t, int32(0), people.calls.Load(),
		"people stale scanner MUST NOT be called when holder is empty")
	assert.Equal(t, int32(0), errs.listCalls.Load(),
		"enrichment_errors retry-list MUST NOT be queried when holder is empty")

	out := buf.String()
	assert.Contains(t, out, `"msg":"enrichment.nightly.skipped"`,
		"skip log line must fire so operators can confirm the gate")
	assert.Contains(t, out, `"reason":"tmdb_holder_empty"`,
		"skip log must carry the reason field")
	assert.NotContains(t, out, `"msg":"enrichment.nightly.swept"`,
		"swept summary must NOT fire on the gated path")
}
