// Package enrichment — Story 211 queue, Story 1104 per-kind split.
//
// priorityQueue is the dispatcher's bounded channels + dedup tracker.
// Story 1104: each EntityKind owns its OWN hot/cold channel pair, so a
// worker pool drains only its own kind — no cross-kind hot-spin, no
// drop-on-full-after-release. Priority selects hot-vs-cold WITHIN a
// kind; it never selects the kind.
//
// Capacity 200 per (kind × priority) channel (PRD §5.5 "cap 200").
// Dedup is keyed on (kind, id) — orthogonal to the channels — so a
// second Enqueue for an in-flight or queued entity is a no-op.
//
// Concurrency model:
//   - enqueue / dequeue safe for concurrent callers (mu protects the
//     dedup map + depth counter; channel ops are already thread-safe;
//     the chans map is written once in newPriorityQueue and never
//     mutated after, so lock-free reads of it are race-free).
//   - dequeue(ctx, kind) returns (Job, ok). ok=false ⇒ closed / ctx
//     cancel; the worker exits.
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

// chanPair is one kind's hot + cold buffered channels. Story 1104.
type chanPair struct {
	hot  chan Job
	cold chan Job
}

type priorityQueue struct {
	// chans is keyed by EntityKind. Written ONCE in newPriorityQueue
	// (before the queue is shared with any goroutine) and never mutated
	// after — safe to read without mu. close() closes the channels but
	// does not touch the map.
	chans map[EntityKind]*chanPair

	mu       sync.Mutex
	inFlight map[string]struct{} // key = kind:id
	// Story 306 — per-kind depth counter. Includes pending + in-flight
	// (the inFlight map already covers both). Mutated under mu; the
	// gauge tick publishes under mu so the writer set is single-writer
	// per (kind) label and races impossible by construction. Story 1104
	// does not change this — depth is per-kind regardless of channels.
	depth  map[EntityKind]int
	closed bool
}

// knownKinds is the fixed enum the queue allocates channels for.
var knownKinds = []EntityKind{EntitySeries, EntityPerson, EntityOMDb}

func newPriorityQueue() *priorityQueue {
	q := &priorityQueue{
		chans:    make(map[EntityKind]*chanPair, len(knownKinds)),
		inFlight: make(map[string]struct{}),
		depth:    map[EntityKind]int{},
	}
	// One channel pair per kind. Pre-publish zero depth so the gauge
	// appears in /metrics at boot (Grafana panel doesn't render an empty
	// "no series" until the first enqueue).
	for _, k := range knownKinds {
		q.chans[k] = &chanPair{
			hot:  make(chan Job, queueCapacity),
			cold: make(chan Job, queueCapacity),
		}
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
// queue / full channel / unknown kind. Channel-full ALSO returns
// false — the dispatcher logs and the caller's enqueue is dropped
// silently (nightly sweep will re-enqueue). The drop is counted +
// logged at WARN. Story 1104: the target channel is now selected by
// (kind, priority).
func (q *priorityQueue) enqueue(j Job) bool {
	q.mu.Lock()
	if q.closed {
		q.mu.Unlock()
		return false
	}
	pair := q.chans[j.Kind]
	if pair == nil {
		// Unknown kind — no channels allocated. Defensive: Enqueue
		// validates via IsValid before calling, so this is unreachable
		// in production, but a direct queue caller must not panic.
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

	// The non-blocking channel send is performed while still holding mu.
	// This closes the race window between the closed-check above and the
	// actual ch<-j: since close() also holds mu when it closes the
	// channels, the two operations are mutually exclusive — we will
	// either see closed=true and bail, or we will complete the send
	// before close() gets the lock. Holding mu across a non-blocking
	// select is safe because the select completes instantly (buffered
	// channel, capacity 200).
	ch := pair.cold
	if j.Priority == PriorityHot {
		ch = pair.hot
	}
	var sent bool
	select {
	case ch <- j:
		sent = true
	default:
	}
	if !sent {
		// Channel full — roll back the dedup slot and depth, then
		// report the drop. Story 318: tick the drops counter. Rolling
		// back the dedup slot is what lets a later enqueue succeed once
		// the channel drains (the job is NOT lost forever).
		delete(q.inFlight, key)
		q.depth[j.Kind]--
		depthAfter = q.depth[j.Kind]
		q.mu.Unlock()
		observability.SetEnrichmentQueueDepth(string(j.Kind), depthAfter)
		observability.IncEnrichmentQueueDrop(string(j.Kind))
		return false
	}
	q.mu.Unlock()

	// Publish the per-kind depth outside the mutex (still single-writer
	// because the previous block computed depthAfter while holding the
	// lock). Story 306.
	observability.SetEnrichmentQueueDepth(string(j.Kind), depthAfter)
	return true
}

// dequeue blocks until a job of the given kind is available or ctx
// cancels. Hot beats cold: every wake-up tries the kind's hot channel
// first via a non-blocking receive, then falls back to a select over
// both of that kind's channels. Returns (zero, false) on ctx cancel,
// close, or unknown kind. Story 1104: a worker only ever sees its own
// kind's jobs — the cross-kind drain branch in the dispatcher is gone.
func (q *priorityQueue) dequeue(ctx context.Context, kind EntityKind) (Job, bool) {
	pair := q.chans[kind]
	if pair == nil {
		// Unknown kind has no channels; behave like a closed queue so a
		// mis-wired worker exits instead of spinning.
		return Job{}, false
	}
	// Hot fast path — try hot first without selecting on cold.
	select {
	case j, ok := <-pair.hot:
		if !ok {
			return Job{}, false
		}
		return j, true
	default:
	}
	select {
	case <-ctx.Done():
		return Job{}, false
	case j, ok := <-pair.hot:
		if !ok {
			return Job{}, false
		}
		return j, true
	case j, ok := <-pair.cold:
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

// close shuts every kind's channels; outstanding dequeue calls observe
// (zero, false). Idempotent.
func (q *priorityQueue) close() {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.closed {
		return
	}
	q.closed = true
	for _, pair := range q.chans {
		close(pair.hot)
		close(pair.cold)
	}
}
