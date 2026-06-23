// bg_fetcher.go ships the deferred fetcher behind /discovery/discover
// (story 509 N-2h). When the sync TMDB call times out (5s), the handler
// enqueues the (filter, lang, page) tuple here and returns 202 +
// degraded:["tmdb_throttled"]. The worker processes the queue in
// background, hitting TMDB through the same rate-limited passthrough,
// and writes successful results into the LRU keyed by canonicalHash.
//
// Concurrency invariants:
//   - In-flight dedup: one job per cacheKey at a time. Concurrent
//     EnqueueDedup for the same key drops on the floor (and ticks the
//     cache's dedup-hit counter for observability) until the first job
//     completes.
//   - Queue overflow: capacity 100. If full, the enqueue rolls back the
//     inflight + pending state and logs WARN. The handler never blocks
//     on a full queue — the FE will simply not warm that key on this
//     request and the user retries normally.
//   - Single consumer goroutine: RunWorker is invoked once from
//     cmd/server/server.go via lifecycle.Go. The handler never serialises
//     fetches itself.
//
// The fetcher does NOT publish a separate metric family — the cache's
// pending/dedup counters in cachewatch already surface the activity.
package app

import (
	"context"
	"log/slog"
	"sync"

	disco "github.com/alexmorbo/seasonfill/internal/discovery/domain"
	"github.com/alexmorbo/seasonfill/internal/shared/cachewatch"
	"github.com/alexmorbo/seasonfill/internal/shared/clients/tmdb"
)

// bgQueueCapacity bounds the deferred-fetch queue. Per PRD §5.1.2 — 100
// concurrent ad-hoc requests is well above the FE concurrency budget,
// and overflow degrades to "skip warming" not "drop user request".
const bgQueueCapacity = 100

// bgJob is a queued passthrough fetch the worker drains.
type bgJob struct {
	key    string
	filter tmdb.DiscoverFilter
	lang   string
	page   int
}

// BgFetcher coalesces deferred /discovery/discover fetches behind a
// single goroutine.
type BgFetcher struct {
	inflight sync.Map // cacheKey → struct{}
	queue    chan bgJob
	lru      *cachewatch.Cache[string, []disco.Item]
	pass     TMDBPassthrough
	log      *slog.Logger
}

// NewBgFetcher wires the fetcher against the live LRU + passthrough.
// Construct exactly one at boot from BuildDiscoveryDiscover. RunWorker
// must be launched on lifecycle.Go.
func NewBgFetcher(lru *cachewatch.Cache[string, []disco.Item], pass TMDBPassthrough, log *slog.Logger) *BgFetcher {
	switch {
	case lru == nil:
		panic("discovery bg fetcher: lru required")
	case pass == nil:
		panic("discovery bg fetcher: passthrough required")
	case log == nil:
		panic("discovery bg fetcher: log required")
	}
	return &BgFetcher{
		queue: make(chan bgJob, bgQueueCapacity),
		lru:   lru,
		pass:  pass,
		log:   log,
	}
}

// EnqueueDedup pushes a fetch job onto the queue iff no other job is
// already in-flight for the same cacheKey. Concurrent callers for the
// same key tick the cache's dedup-hit counter and return — the first
// caller wins.
//
// Queue-full rollback: if the queue cannot accept the job, the inflight
// map and pending counter are rolled back and a WARN is logged. The
// caller is the handler; the FE will simply not warm that key on this
// request, which is acceptable (next user retry succeeds).
func (b *BgFetcher) EnqueueDedup(key string, filter tmdb.DiscoverFilter, lang string, page int) {
	if _, loaded := b.inflight.LoadOrStore(key, struct{}{}); loaded {
		b.lru.RecordDedupHit()
		return
	}
	b.lru.IncPending()
	job := bgJob{key: key, filter: filter, lang: lang, page: page}
	select {
	case b.queue <- job:
	default:
		// Queue full — roll back state so a future request can try again.
		b.inflight.Delete(key)
		b.lru.DecPending()
		b.log.Warn("discovery.discover.queue_full",
			slog.String("cache_key", key),
			slog.Int("page", page))
	}
}

// RunWorker drains the queue until ctx cancels. Single consumer — never
// fan out, the rate limiter inside tmdb.Client already serialises the
// upstream calls and a second goroutine here would only shorten the
// effective Retry-After window.
//
// Returns nil on graceful shutdown (ctx cancel). Any Fetch error is
// logged at WARN; the cacheKey is NOT marked failed — the LRU simply
// stays empty and the next request retries. This matches the operator
// expectation that a transient TMDB outage doesn't poison the cache.
func (b *BgFetcher) RunWorker(ctx context.Context) error {
	for {
		select {
		case <-ctx.Done():
			return nil
		case job := <-b.queue:
			b.process(ctx, job)
		}
	}
}

// process handles one queued job. Always releases inflight + pending,
// even on error. Successful fetches populate the LRU.
func (b *BgFetcher) process(ctx context.Context, job bgJob) {
	defer b.inflight.Delete(job.key)
	defer b.lru.DecPending()

	items, err := b.pass.Fetch(ctx, job.filter, job.lang, job.page)
	if err != nil {
		b.log.Warn("discovery.discover.bg_fetch_failed",
			slog.String("cache_key", job.key),
			slog.Int("page", job.page),
			slog.String("error", err.Error()))
		return
	}
	b.lru.Add(job.key, items)
}
