package loops

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alexmorbo/seasonfill/internal/shared/domain"
)

type stubCapRepo struct {
	calls  atomic.Int32
	counts map[domain.InstanceName]int
	err    error
}

func (s *stubCapRepo) CountPresentByInstance(_ context.Context, instance domain.InstanceName) (int, error) {
	s.calls.Add(1)
	if s.err != nil {
		return 0, s.err
	}
	return s.counts[instance], nil
}

type stubCapInstances struct{ list []domain.InstanceName }

func (s stubCapInstances) List() []domain.InstanceName { return s.list }

type stubCapMetrics struct {
	mu   sync.Mutex
	seen map[domain.InstanceName]int
}

func (s *stubCapMetrics) SetQbitTorrentsRows(instance domain.InstanceName, count int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.seen == nil {
		s.seen = map[domain.InstanceName]int{}
	}
	s.seen[instance] = count
}

func TestQbitCapacityLoop_TicksAndPublishes(t *testing.T) {
	t.Parallel()
	repo := &stubCapRepo{counts: map[domain.InstanceName]int{"alpha": 5, "beta": 9}}
	mtr := &stubCapMetrics{}
	loop := NewQbitCapacityLoop(repo,
		stubCapInstances{list: []domain.InstanceName{"alpha", "beta"}},
		mtr, 50*time.Millisecond, &sync.WaitGroup{}, nil)

	ctx, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
	defer cancel()
	wg := &sync.WaitGroup{}
	wg.Go(func() {
		// Bump the loop's bgWG mirror so Run's defer Done is balanced.
		loop.bgWG.Add(1)
		loop.Run(ctx)
	})
	wg.Wait()

	if got := repo.calls.Load(); got < 2 {
		t.Errorf("expected >=2 repo calls, got %d", got)
	}
	mtr.mu.Lock()
	defer mtr.mu.Unlock()
	if mtr.seen["alpha"] != 5 || mtr.seen["beta"] != 9 {
		t.Errorf("metrics not updated: %#v", mtr.seen)
	}
}

func TestQbitCapacityLoop_PerInstanceErrorDoesNotStall(t *testing.T) {
	t.Parallel()
	repo := &stubCapRepo{err: errors.New("boom")}
	mtr := &stubCapMetrics{}
	bgWG := &sync.WaitGroup{}
	loop := NewQbitCapacityLoop(repo,
		stubCapInstances{list: []domain.InstanceName{"alpha"}},
		mtr, 50*time.Millisecond, bgWG, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	bgWG.Add(1)
	loop.Run(ctx)
	if repo.calls.Load() < 2 {
		t.Errorf("loop should keep ticking on per-instance errors, got %d calls", repo.calls.Load())
	}
}

func TestQbitCapacityLoop_DrainsOnContextCancel(t *testing.T) {
	t.Parallel()
	repo := &stubCapRepo{counts: map[domain.InstanceName]int{"alpha": 1}}
	mtr := &stubCapMetrics{}
	bgWG := &sync.WaitGroup{}
	loop := NewQbitCapacityLoop(repo,
		stubCapInstances{list: []domain.InstanceName{"alpha"}},
		mtr, time.Second, bgWG, nil)
	ctx, cancel := context.WithCancel(context.Background())
	bgWG.Add(1)
	go loop.Run(ctx)
	time.Sleep(20 * time.Millisecond)
	cancel()
	bgWG.Wait()
}

// TestQbitCapacityInstancesFunc_Adapter sanity-checks the closure
// adapter wiring used by production (closure over holder.Load()).
func TestQbitCapacityInstancesFunc_Adapter(t *testing.T) {
	t.Parallel()
	called := false
	src := QbitCapacityInstancesFunc(func() []domain.InstanceName {
		called = true
		return []domain.InstanceName{"alpha"}
	})
	got := src.List()
	if !called {
		t.Fatal("adapter did not invoke the closure")
	}
	if len(got) != 1 || got[0] != "alpha" {
		t.Errorf("unexpected list: %v", got)
	}
}
