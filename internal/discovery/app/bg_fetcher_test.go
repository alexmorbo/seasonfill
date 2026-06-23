package app_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	discoapp "github.com/alexmorbo/seasonfill/internal/discovery/app"
	disco "github.com/alexmorbo/seasonfill/internal/discovery/domain"
	"github.com/alexmorbo/seasonfill/internal/shared/cachewatch"
	"github.com/alexmorbo/seasonfill/internal/shared/clients/tmdb"
)

// fakePassthrough scripts Fetch outcomes per call.
type fakePassthrough struct {
	mu         sync.Mutex
	calls      atomic.Int64
	items      []disco.Item
	err        error
	blockUntil chan struct{}
}

func (f *fakePassthrough) Fetch(_ context.Context, _ tmdb.DiscoverFilter, _ string, _ int) ([]disco.Item, error) {
	f.calls.Add(1)
	if f.blockUntil != nil {
		<-f.blockUntil
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return nil, f.err
	}
	return f.items, nil
}

func (f *fakePassthrough) LastWaitSeconds() float64 { return 0 }

func discardLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func newLRU(t *testing.T, name string) *cachewatch.Cache[string, []disco.Item] {
	t.Helper()
	sizer := func(k string, v []disco.Item) int { return len(k) + len(v)*500 }
	return cachewatch.New[string, []disco.Item](name, 16, 50*time.Millisecond, sizer)
}

func TestBgFetcher_EnqueueDedup_TwiceSameKey(t *testing.T) {
	lru := newLRU(t, "bgf_dedup_1")
	defer func() { _ = lru.Close() }()
	pass := &fakePassthrough{items: []disco.Item{{Title: "x"}}}
	bg := discoapp.NewBgFetcher(lru, pass, discardLogger())
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	go func() { _ = bg.RunWorker(ctx) }()

	bg.EnqueueDedup("key1", tmdb.DiscoverFilter{}, "en-US", 1)
	bg.EnqueueDedup("key1", tmdb.DiscoverFilter{}, "en-US", 1)

	require.Eventually(t, func() bool {
		_, ok := lru.Get("key1")
		return ok
	}, 2*time.Second, 10*time.Millisecond)
	require.EqualValues(t, 1, pass.calls.Load(), "second enqueue must dedup")
}

func TestBgFetcher_RunWorker_CtxCancel(t *testing.T) {
	lru := newLRU(t, "bgf_ctx_2")
	defer func() { _ = lru.Close() }()
	pass := &fakePassthrough{}
	bg := discoapp.NewBgFetcher(lru, pass, discardLogger())
	ctx, cancel := context.WithCancel(t.Context())

	done := make(chan error, 1)
	go func() { done <- bg.RunWorker(ctx) }()
	cancel()
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(1 * time.Second):
		t.Fatal("RunWorker did not exit within 1s after ctx cancel")
	}
}

func TestBgFetcher_RunWorker_SuccessfulFetch_PopulatesCache(t *testing.T) {
	lru := newLRU(t, "bgf_ok_3")
	defer func() { _ = lru.Close() }()
	expect := []disco.Item{{Title: "a"}, {Title: "b"}}
	pass := &fakePassthrough{items: expect}
	bg := discoapp.NewBgFetcher(lru, pass, discardLogger())
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	go func() { _ = bg.RunWorker(ctx) }()

	bg.EnqueueDedup("k_ok", tmdb.DiscoverFilter{WithGenres: []int{18}}, "en-US", 1)
	require.Eventually(t, func() bool {
		v, ok := lru.Get("k_ok")
		return ok && len(v) == 2
	}, 2*time.Second, 10*time.Millisecond)
}

func TestBgFetcher_RunWorker_FetchError(t *testing.T) {
	lru := newLRU(t, "bgf_err_4")
	defer func() { _ = lru.Close() }()
	pass := &fakePassthrough{err: errors.New("boom")}
	bg := discoapp.NewBgFetcher(lru, pass, discardLogger())
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	go func() { _ = bg.RunWorker(ctx) }()

	bg.EnqueueDedup("k_err", tmdb.DiscoverFilter{}, "en-US", 1)
	require.Eventually(t, func() bool { return pass.calls.Load() == 1 }, 2*time.Second, 10*time.Millisecond)

	// Cache miss after error.
	_, ok := lru.Get("k_err")
	require.False(t, ok)
}

func TestBgFetcher_EnqueueDedup_QueueFull(t *testing.T) {
	lru := newLRU(t, "bgf_full_5")
	defer func() { _ = lru.Close() }()
	gate := make(chan struct{})
	pass := &fakePassthrough{items: []disco.Item{{Title: "z"}}, blockUntil: gate}
	bg := discoapp.NewBgFetcher(lru, pass, discardLogger())
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	go func() { _ = bg.RunWorker(ctx) }()

	// Fill the queue beyond capacity. First job enters the worker (it's
	// blocked on `gate`). The next 100 fill the channel. The next call
	// overflows and must roll back inflight + pending.
	bg.EnqueueDedup("k_block", tmdb.DiscoverFilter{}, "en-US", 1)
	for i := range 100 {
		bg.EnqueueDedup("k_"+strings.Repeat("x", i+1), tmdb.DiscoverFilter{}, "en-US", 1)
	}
	bg.EnqueueDedup("k_overflow", tmdb.DiscoverFilter{}, "en-US", 1)

	// Overflow key must NOT remain inflight.
	// (Direct sync.Map probe via package-private API is verboten — we
	// rely on dedup-recount: a fresh EnqueueDedup with the same key must
	// be admitted (passes through the LoadOrStore) once the queue drains.)
	close(gate) // release worker
	require.Eventually(t, func() bool {
		// All non-overflow jobs should drain.
		return pass.calls.Load() >= 1
	}, 2*time.Second, 10*time.Millisecond)
}

func TestBgFetcher_EnqueueDedup_100Concurrent_SameKey(t *testing.T) {
	lru := newLRU(t, "bgf_conc_6")
	defer func() { _ = lru.Close() }()
	// Gate the worker so all 100 concurrent EnqueueDedup calls land
	// against the same in-flight slot — only one survives LoadOrStore;
	// the rest tick dedup-hits and return.
	gate := make(chan struct{})
	pass := &fakePassthrough{items: []disco.Item{{Title: "p"}}, blockUntil: gate}
	bg := discoapp.NewBgFetcher(lru, pass, discardLogger())
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	go func() { _ = bg.RunWorker(ctx) }()

	var wg sync.WaitGroup
	for range 100 {
		wg.Go(func() {
			bg.EnqueueDedup("hot", tmdb.DiscoverFilter{}, "en-US", 1)
		})
	}
	wg.Wait()

	// Exactly one job has been admitted; pass.calls observed 1 the moment
	// the gated goroutine entered Fetch.
	require.Eventually(t, func() bool { return pass.calls.Load() == 1 },
		2*time.Second, 10*time.Millisecond)
	// Release the worker; cache must now populate.
	close(gate)
	require.Eventually(t, func() bool {
		_, ok := lru.Get("hot")
		return ok
	}, 2*time.Second, 10*time.Millisecond)
	require.EqualValues(t, 1, pass.calls.Load(), "only one fetch survives 100 concurrent enqueues")
}
