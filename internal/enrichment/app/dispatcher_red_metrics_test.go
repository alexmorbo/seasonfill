package enrichment

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

	"github.com/VictoriaMetrics/metrics"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/internal/observability"
)

func counterVal(name string) uint64 { return metrics.GetOrCreateCounter(name).Get() }
func gaugeVal(name string) float64  { return metrics.GetOrCreateGauge(name, nil).Get() }

// TestDispatcher_REDMetrics_SuccessAndError drives one success job and one
// error job through the real dispatcher pump and asserts the M-2 families move.
func TestDispatcher_REDMetrics_SuccessAndError(t *testing.T) {
	// NOT parallel — see file header: keeps the inflight gauge read clean.
	const (
		okCounter    = `seasonfill_enrichment_job_total{kind="series",result="success"}`
		errCounter   = `seasonfill_enrichment_job_total{kind="series",result="error"}`
		inflightName = `seasonfill_enrichment_job_inflight{kind="series"}`
	)
	okBase := counterVal(okCounter)
	errBase := counterVal(errCounter)
	inflightBase := gaugeVal(inflightName)

	d := NewDispatcher(Workers{
		SeriesHandler: func(_ context.Context, id int64) error {
			if id == 1 {
				return errors.New("boom") // → result="error"
			}
			return nil // → result="success"
		},
	}, quietLogger())
	ctx := t.Context()
	d.Start(ctx)

	// Track completions indirectly via the success/error counters.
	d.Enqueue(EntitySeries, 1, PriorityHot) // error
	d.Enqueue(EntitySeries, 2, PriorityHot) // success
	waitFor(t, 2*time.Second, func() bool {
		return counterVal(okCounter) >= okBase+1 && counterVal(errCounter) >= errBase+1
	})

	// Close waits for the pool to drain — inflight MUST return to baseline.
	d.Close()

	assert.GreaterOrEqual(t, counterVal(okCounter), okBase+1, "success counter must increment")
	assert.GreaterOrEqual(t, counterVal(errCounter), errBase+1, "error counter must increment")
	assert.InDelta(t, inflightBase, gaugeVal(inflightName), 0.0001,
		"inflight gauge must return to baseline after all jobs complete")

	// Exposition-level name presence (mirrors the WritePrometheus assertion
	// pattern used by db_pool_metrics_test.go / quota_metric_test.go).
	buf := &bytes.Buffer{}
	observability.WritePrometheus(buf)
	body := buf.String()
	assert.Contains(t, body, okCounter, "success counter line must be exposed")
	assert.Contains(t, body, errCounter, "error counter line must be exposed")
	assert.Contains(t, body, `seasonfill_enrichment_job_duration_seconds_count{kind="series"}`,
		"duration histogram _count line must be exposed (>=1 observation)")
}

// TestDispatcher_REDMetrics_SkippedForNilHandler proves the nil-handler branch
// records result="skipped" and still balances the inflight gauge.
func TestDispatcher_REDMetrics_SkippedForNilHandler(t *testing.T) {
	const (
		skipCounter  = `seasonfill_enrichment_job_total{kind="person",result="skipped"}`
		inflightName = `seasonfill_enrichment_job_inflight{kind="person"}`
	)
	skipBase := counterVal(skipCounter)
	inflightBase := gaugeVal(inflightName)

	d := NewDispatcher(Workers{
		SeriesHandler: func(context.Context, int64) error { return nil },
		PersonHandler: nil, // → runHandler handler==nil → result="skipped"
	}, quietLogger())
	ctx := t.Context()
	d.Start(ctx)
	d.Enqueue(EntityPerson, 5, PriorityHot)
	waitFor(t, 2*time.Second, func() bool { return counterVal(skipCounter) >= skipBase+1 })
	d.Close()

	assert.GreaterOrEqual(t, counterVal(skipCounter), skipBase+1, "skipped counter must increment")
	assert.InDelta(t, inflightBase, gaugeVal(inflightName), 0.0001,
		"inflight gauge must return to baseline after a skipped job")
}

// TestDispatcher_REDMetrics_PanicBalancesInflight proves the single defer
// Decrements inflight even when the handler panics (result defaults to "error").
func TestDispatcher_REDMetrics_PanicBalancesInflight(t *testing.T) {
	const inflightName = `seasonfill_enrichment_job_inflight{kind="series"}`
	inflightBase := gaugeVal(inflightName)

	d := NewDispatcher(Workers{}, quietLogger())
	// Pin the dedup slot as a normal enqueue would, then invoke runHandler
	// directly with a panicking handler inside a recover guard.
	require.True(t, d.queue.enqueue(Job{Kind: EntitySeries, EntityID: 555, Priority: PriorityHot}))
	rctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_, ok := d.queue.dequeue(rctx, EntitySeries)
	require.True(t, ok)

	func() {
		defer func() { _ = recover() }()
		d.runHandler(context.Background(), quietLogger(),
			Job{Kind: EntitySeries, EntityID: 555, Priority: PriorityHot},
			func(context.Context, int64, Priority) error {
				panic("intentional panic — inflight MUST still decrement")
			})
	}()

	assert.InDelta(t, inflightBase, gaugeVal(inflightName), 0.0001,
		"inflight gauge must return to baseline after a handler panic")
}
