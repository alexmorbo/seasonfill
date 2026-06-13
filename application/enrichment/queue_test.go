package enrichment

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
	got, ok := q.dequeue(ctx)
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
	got, ok := q.dequeue(ctx)
	require.True(t, ok)
	assert.Equal(t, int64(7), got.EntityID)
}

func TestQueue_DequeueRespectsCtxCancel(t *testing.T) {
	t.Parallel()
	q := newPriorityQueue()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, ok := q.dequeue(ctx)
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
	_, ok := q.dequeue(ctx)
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
	_, ok := q.dequeue(ctx)
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
