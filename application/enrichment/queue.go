// Package enrichment — Story 211 queue.
//
// priorityQueue is the dispatcher's bounded channel + dedup tracker.
// Capacity 200 (cold + hot share the cap — PRD §5.5 "cap 200"); two
// independent buffered channels, drained hot-first per worker tick.
// Dedup is keyed on (kind, id) — a second Enqueue for an in-flight
// or queued entity is a no-op.
//
// Concurrency model:
//   - enqueue / dequeue safe for concurrent callers (mu protects the
//     dedup map; channel ops are already thread-safe).
//   - dequeue returns (Job, ok). ok=false ⇒ closed; the worker exits.
//   - The dedup entry is released by the WORKER after the job
//     completes (success or terminal failure), NOT by dequeue —
//     keeping the entry pinned across the worker's HTTP call window
//     is what gives us "two simultaneous Enqueue ⇒ one HTTP call"
//     (PRD acceptance criterion).

package enrichment

import (
	"context"
	"sync"

	"github.com/alexmorbo/seasonfill/internal/observability"
)

const queueCapacity = 200

type priorityQueue struct {
	hot  chan Job
	cold chan Job

	mu       sync.Mutex
	inFlight map[string]struct{} // key = kind:id
	// Story 306 — per-kind depth counter. Includes pending + in-flight
	// (the inFlight map already covers both). Mutated under mu; the
	// gauge tick publishes under mu so the writer set is single-writer
	// per (kind) label and races impossible by construction.
	depth  map[EntityKind]int
	closed bool
}

func newPriorityQueue() *priorityQueue {
	q := &priorityQueue{
		hot:      make(chan Job, queueCapacity),
		cold:     make(chan Job, queueCapacity),
		inFlight: make(map[string]struct{}),
		depth:    map[EntityKind]int{},
	}
	// Pre-publish zero for the three known kinds so the gauge appears
	// in /metrics at boot (Grafana panel doesn't render an empty
	// "no series" until the first enqueue).
	for _, k := range []EntityKind{EntitySeries, EntityPerson, EntityOMDb} {
		observability.SetEnrichmentQueueDepth(string(k), 0)
	}
	return q
}

// dedupKey is the natural key for the in-flight set.
func dedupKey(kind EntityKind, id int64) string {
	// Hand-rolled — avoids fmt.Sprintf overhead on a hot path.
	return string(kind) + ":" + itoa(id)
}

func itoa(n int64) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

// enqueue adds a job iff (kind, id) is not already pending or
// in-flight. Returns true on accept, false on dedup-skip / closed
// queue / full channel. Channel-full ALSO returns false — the
// dispatcher logs and the caller's enqueue is dropped silently
// (nightly sweep will re-enqueue). The drop is logged at WARN.
func (q *priorityQueue) enqueue(j Job) bool {
	q.mu.Lock()
	if q.closed {
		q.mu.Unlock()
		return false
	}
	key := dedupKey(j.Kind, j.EntityID)
	if _, ok := q.inFlight[key]; ok {
		q.mu.Unlock()
		return false
	}
	q.inFlight[key] = struct{}{}
	q.depth[j.Kind]++
	depthAfter := q.depth[j.Kind]
	q.mu.Unlock()

	// Publish the per-kind depth outside the mutex (still single-writer
	// because the previous block was holding the lock when it computed
	// depthAfter). Story 306.
	observability.SetEnrichmentQueueDepth(string(j.Kind), depthAfter)

	ch := q.cold
	if j.Priority == PriorityHot {
		ch = q.hot
	}
	select {
	case ch <- j:
		return true
	default:
		// Channel full — release the dedup slot and the depth, then
		// report the drop. The gauge re-publish guarantees the depth
		// reflects the rollback.
		q.mu.Lock()
		delete(q.inFlight, key)
		q.depth[j.Kind]--
		depthAfter = q.depth[j.Kind]
		q.mu.Unlock()
		observability.SetEnrichmentQueueDepth(string(j.Kind), depthAfter)
		return false
	}
}

// dequeue blocks until a job is available or ctx cancels. Hot beats
// cold: every wake-up tries hot first via a non-blocking receive,
// then falls back to a select over both. Returns (zero, false) on
// ctx cancel or close.
func (q *priorityQueue) dequeue(ctx context.Context) (Job, bool) {
	// Hot fast path — try hot first without selecting on cold.
	select {
	case j, ok := <-q.hot:
		if !ok {
			return Job{}, false
		}
		return j, true
	default:
	}
	select {
	case <-ctx.Done():
		return Job{}, false
	case j, ok := <-q.hot:
		if !ok {
			return Job{}, false
		}
		return j, true
	case j, ok := <-q.cold:
		if !ok {
			return Job{}, false
		}
		return j, true
	}
}

// release drops the dedup entry for (kind, id) — called by the
// worker after the job completes (success or terminal). Safe to
// call for a key not present (defensive).
func (q *priorityQueue) release(kind EntityKind, id int64) {
	q.mu.Lock()
	key := dedupKey(kind, id)
	if _, ok := q.inFlight[key]; !ok {
		// Defensive: release for a key not present is a no-op AND
		// must NOT decrement the depth (avoids a negative gauge after
		// double-release).
		q.mu.Unlock()
		return
	}
	delete(q.inFlight, key)
	q.depth[kind]--
	depthAfter := q.depth[kind]
	q.mu.Unlock()
	observability.SetEnrichmentQueueDepth(string(kind), depthAfter)
}

// close shuts the channels; outstanding dequeue calls observe
// (zero, false). Idempotent.
func (q *priorityQueue) close() {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.closed {
		return
	}
	q.closed = true
	close(q.hot)
	close(q.cold)
}
