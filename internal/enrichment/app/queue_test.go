package enrichment

import (
	"bytes"
	"context"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/internal/observability"
)

func TestQueue_Dedup_TwoEnqueuesOneSlot(t *testing.T) {
	t.Parallel()
	q := newPriorityQueue()
	j := Job{Kind: EntitySeries, EntityID: 1, Priority: PriorityHot}
	assert.True(t, q.enqueue(j), "first enqueue accepted")
	assert.False(t, q.enqueue(j), "duplicate enqueue must be skipped")
}

func TestQueue_HotBeatsCold(t *testing.T) {
	t.Parallel()
	q := newPriorityQueue()
	require.True(t, q.enqueue(Job{Kind: EntitySeries, EntityID: 1, Priority: PriorityCold}))
	require.True(t, q.enqueue(Job{Kind: EntitySeries, EntityID: 2, Priority: PriorityHot}))
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	got, ok := q.dequeue(ctx, EntitySeries)
	require.True(t, ok)
	assert.Equal(t, int64(2), got.EntityID, "hot job must drain first")
}

func TestQueue_DequeueBlocksUntilEnqueue(t *testing.T) {
	t.Parallel()
	q := newPriorityQueue()
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	go func() {
		time.Sleep(20 * time.Millisecond)
		q.enqueue(Job{Kind: EntitySeries, EntityID: 7, Priority: PriorityCold})
	}()
	got, ok := q.dequeue(ctx, EntitySeries)
	require.True(t, ok)
	assert.Equal(t, int64(7), got.EntityID)
}

func TestQueue_DequeueRespectsCtxCancel(t *testing.T) {
	t.Parallel()
	q := newPriorityQueue()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, ok := q.dequeue(ctx, EntitySeries)
	assert.False(t, ok)
}

func TestQueue_Release_RestoresDedupSlot(t *testing.T) {
	t.Parallel()
	q := newPriorityQueue()
	j := Job{Kind: EntitySeries, EntityID: 99, Priority: PriorityHot}
	require.True(t, q.enqueue(j))
	require.False(t, q.enqueue(j))
	q.release(j.Kind, j.EntityID)
	// Drain the existing entry first to keep the channel uncluttered.
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_, ok := q.dequeue(ctx, EntitySeries)
	require.True(t, ok)
	// Now the dedup slot is empty AND the channel is empty — re-enqueue must succeed.
	assert.True(t, q.enqueue(j))
}

func TestQueue_CloseDrainsDequeue(t *testing.T) {
	t.Parallel()
	q := newPriorityQueue()
	q.close()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_, ok := q.dequeue(ctx, EntitySeries)
	assert.False(t, ok)
}

func TestQueue_CapacityOverflow(t *testing.T) {
	t.Parallel()
	q := newPriorityQueue()
	accepted := 0
	for i := 1; i <= queueCapacity+5; i++ {
		if q.enqueue(Job{Kind: EntitySeries, EntityID: int64(i), Priority: PriorityHot}) {
			accepted++
		}
	}
	assert.Equal(t, queueCapacity, accepted, "must accept exactly queueCapacity hot jobs")
}

func TestQueue_CloseIsIdempotent(t *testing.T) {
	t.Parallel()
	q := newPriorityQueue()
	q.close()
	q.close()
}

// 306 — verify per-kind depth gauge ticks on enqueue/release.
// Reads VictoriaMetrics via observability.WritePrometheus rather than
// inspecting the queue's internal counter so the test mirrors what
// the operator's Grafana panel will read.
func TestQueue_DepthGauge_TicksOnEnqueueAndRelease(t *testing.T) {
	q := newPriorityQueue()
	// Initial state — gauge is published as zero from newPriorityQueue,
	// so /metrics already has the entry.
	body := readMetrics(t)
	require.Contains(t, body, `enrichment_queue_depth{worker="series"}`)

	require.True(t, q.enqueue(Job{Kind: EntitySeries, EntityID: 1, Priority: PriorityHot}))
	require.True(t, q.enqueue(Job{Kind: EntitySeries, EntityID: 2, Priority: PriorityCold}))

	// After 2 enqueues the series gauge is 2.
	body = readMetrics(t)
	require.Contains(t, body, `enrichment_queue_depth{worker="series"} 2`)

	q.release(EntitySeries, 1)
	body = readMetrics(t)
	require.Contains(t, body, `enrichment_queue_depth{worker="series"} 1`)

	q.release(EntitySeries, 2)
	body = readMetrics(t)
	require.Contains(t, body, `enrichment_queue_depth{worker="series"} 0`)
}

// Defensive: a stray release for a key not in inFlight must not push
// the gauge negative.
func TestQueue_Release_UnknownKey_DoesNotGoNegative(t *testing.T) {
	q := newPriorityQueue()
	q.release(EntitySeries, 999)
	body := readMetrics(t)
	require.Contains(t, body, `enrichment_queue_depth{worker="series"} 0`)
}

func readMetrics(t *testing.T) string {
	t.Helper()
	buf := &bytes.Buffer{}
	observability.WritePrometheus(buf)
	return buf.String()
}

// Story 1104: a series dequeue must NEVER drain a person job. The job
// stays available for a person dequeue. Proves per-kind channel
// isolation at the queue level (the dispatcher-level counterpart is
// TestDispatcher_SeriesWorkerNeverRunsPersonJob).
func TestQueue_DequeueIsPerKind(t *testing.T) {
	t.Parallel()
	q := newPriorityQueue()
	require.True(t, q.enqueue(Job{Kind: EntityPerson, EntityID: 5, Priority: PriorityHot}))

	// A series dequeue must time out — it must NOT steal the person job.
	ctxS, cancelS := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancelS()
	_, ok := q.dequeue(ctxS, EntitySeries)
	assert.False(t, ok, "series dequeue must not drain a person job")

	// The person job is still there for a person dequeue.
	ctxP, cancelP := context.WithTimeout(context.Background(), time.Second)
	defer cancelP()
	got, ok := q.dequeue(ctxP, EntityPerson)
	require.True(t, ok, "person dequeue must still find the person job")
	assert.Equal(t, int64(5), got.EntityID)
}

// Story 1104: filling a kind's hot channel to cap must drop the next
// enqueue WITHOUT leaking the dedup slot or the depth counter — the
// dropped job must be re-enqueueable once a slot frees, and the drop
// counter must tick. NOT parallel: reads the process-global drop
// counter via a before/after delta.
func TestQueue_FullChannel_RollsBackDedupAndDepth(t *testing.T) {
	q := newPriorityQueue()

	before := metricValue(t, readMetrics(t), `enrichment_queue_drops_total{worker="series"}`)

	// Fill the series HOT channel exactly to cap.
	for i := 1; i <= queueCapacity; i++ {
		require.True(t, q.enqueue(Job{Kind: EntitySeries, EntityID: int64(i), Priority: PriorityHot}))
	}

	// The next hot series enqueue is dropped (channel full).
	dropID := int64(queueCapacity + 1)
	assert.False(t, q.enqueue(Job{Kind: EntitySeries, EntityID: dropID, Priority: PriorityHot}),
		"enqueue into a full channel must return false")

	// Dedup slot + depth must be rolled back — the job is NOT pinned.
	q.mu.Lock()
	_, pinned := q.inFlight[dedupKey(EntitySeries, dropID)]
	depth := q.depth[EntitySeries]
	q.mu.Unlock()
	assert.False(t, pinned, "dropped job's dedup slot must be rolled back")
	assert.Equal(t, queueCapacity, depth, "depth counts only the accepted jobs")

	// The drop counter ticked by exactly one.
	after := metricValue(t, readMetrics(t), `enrichment_queue_drops_total{worker="series"}`)
	assert.Equal(t, before+1, after, "channel-full must tick the drop counter once")

	// Drain one job to free a slot; the previously-dropped id now
	// enqueues fine — proving the dedup slot was correctly rolled back
	// (NOT the old release-then-lose bug).
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_, ok := q.dequeue(ctx, EntitySeries)
	require.True(t, ok)
	assert.True(t, q.enqueue(Job{Kind: EntitySeries, EntityID: dropID, Priority: PriorityHot}),
		"once a slot frees the previously-dropped job must enqueue")
}

// metricValue extracts the numeric value of the metric line whose
// prefix is name+" " from a Prometheus text body. Returns 0 when the
// metric is absent (a never-incremented counter is not yet published).
func metricValue(t *testing.T, body, name string) float64 {
	t.Helper()
	for line := range strings.SplitSeq(body, "\n") {
		if strings.HasPrefix(line, name+" ") {
			fields := strings.Fields(line)
			v, err := strconv.ParseFloat(fields[len(fields)-1], 64)
			require.NoError(t, err)
			return v
		}
	}
	return 0
}
