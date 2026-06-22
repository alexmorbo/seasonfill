package loops

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alexmorbo/seasonfill/internal/shared/domain"
	"github.com/alexmorbo/seasonfill/internal/watchdog/domain/cooldown"
)

type stubBlCounter struct {
	calls atomic.Int32
	mu    sync.Mutex
	by    map[domain.InstanceName]int
	err   error
}

func (s *stubBlCounter) CountByInstance(_ context.Context, instance domain.InstanceName) (int, error) {
	s.calls.Add(1)
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.err != nil {
		return 0, s.err
	}
	return s.by[instance], nil
}

type stubCdCounter struct {
	calls atomic.Int32
	mu    sync.Mutex
	by    map[domain.InstanceName]int
	err   error
}

func (s *stubCdCounter) CountActiveByScopeGroupedByInstance(
	_ context.Context, _ cooldown.Scope, _ time.Time,
) (map[domain.InstanceName]int, error) {
	s.calls.Add(1)
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.err != nil {
		return nil, s.err
	}
	return s.by, nil
}

type stubInsts struct{ list []domain.InstanceName }

func (s stubInsts) List() []domain.InstanceName { return s.list }

type stubStateMetrics struct {
	mu        sync.Mutex
	cooldown  map[domain.InstanceName]int
	blacklist map[domain.InstanceName]int
}

func (s *stubStateMetrics) SetCooldownPending(instance domain.InstanceName, count int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cooldown == nil {
		s.cooldown = map[domain.InstanceName]int{}
	}
	s.cooldown[instance] = count
}
func (s *stubStateMetrics) SetBlacklistSize(instance domain.InstanceName, size int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.blacklist == nil {
		s.blacklist = map[domain.InstanceName]int{}
	}
	s.blacklist[instance] = size
}

// runCollector spins the collector under a properly-armed bgWG so
// the embedded defer bgWG.Done doesn't underflow. Returns the wait
// group + a cancel function the test calls after assertions.
func runCollector(t *testing.T, c *WatchdogStateCollector, after time.Duration) {
	t.Helper()
	if c.bgWG != nil {
		c.bgWG.Add(1)
	}
	ctx, cancel := context.WithTimeout(context.Background(), after)
	defer cancel()
	c.Run(ctx)
}

func TestWatchdogStateCollector_TicksAndPublishes(t *testing.T) {
	t.Parallel()
	bl := &stubBlCounter{by: map[domain.InstanceName]int{"alpha": 4, "beta": 7}}
	cd := &stubCdCounter{by: map[domain.InstanceName]int{"alpha": 11, "beta": 0}}
	mtr := &stubStateMetrics{}
	c := NewWatchdogStateCollector(bl, cd,
		stubInsts{list: []domain.InstanceName{"alpha", "beta"}},
		mtr, 50*time.Millisecond, &sync.WaitGroup{}, nil)
	runCollector(t, c, 250*time.Millisecond)

	if bl.calls.Load() < 2 || cd.calls.Load() < 2 {
		t.Errorf("expected ≥2 ticks, got bl=%d cd=%d", bl.calls.Load(), cd.calls.Load())
	}
	mtr.mu.Lock()
	defer mtr.mu.Unlock()
	if mtr.cooldown["alpha"] != 11 {
		t.Errorf("alpha cooldown = %d, want 11", mtr.cooldown["alpha"])
	}
	if mtr.blacklist["beta"] != 7 {
		t.Errorf("beta blacklist = %d, want 7", mtr.blacklist["beta"])
	}
}

func TestWatchdogStateCollector_BlacklistErrorDoesNotStall(t *testing.T) {
	t.Parallel()
	bl := &stubBlCounter{err: errors.New("boom")}
	cd := &stubCdCounter{by: map[domain.InstanceName]int{"alpha": 1}}
	mtr := &stubStateMetrics{}
	c := NewWatchdogStateCollector(bl, cd,
		stubInsts{list: []domain.InstanceName{"alpha"}},
		mtr, 50*time.Millisecond, &sync.WaitGroup{}, nil)
	runCollector(t, c, 150*time.Millisecond)
	if cd.calls.Load() < 2 {
		t.Errorf("cooldown counter should keep ticking on blacklist errors, got %d", cd.calls.Load())
	}
	mtr.mu.Lock()
	defer mtr.mu.Unlock()
	if mtr.cooldown["alpha"] != 1 {
		t.Errorf("cooldown gauge must still publish on bl-error tick, got %d", mtr.cooldown["alpha"])
	}
}

func TestWatchdogStateCollector_CooldownErrorPreservesBlacklistPublish(t *testing.T) {
	t.Parallel()
	bl := &stubBlCounter{by: map[domain.InstanceName]int{"alpha": 6}}
	cd := &stubCdCounter{err: errors.New("cooldown down")}
	mtr := &stubStateMetrics{}
	c := NewWatchdogStateCollector(bl, cd,
		stubInsts{list: []domain.InstanceName{"alpha"}},
		mtr, 50*time.Millisecond, &sync.WaitGroup{}, nil)
	runCollector(t, c, 150*time.Millisecond)
	mtr.mu.Lock()
	defer mtr.mu.Unlock()
	if mtr.blacklist["alpha"] != 6 {
		t.Errorf("blacklist gauge must publish despite cooldown error, got %d", mtr.blacklist["alpha"])
	}
}

func TestWatchdogStateCollector_DrainsOnContextCancel(t *testing.T) {
	t.Parallel()
	bl := &stubBlCounter{by: map[domain.InstanceName]int{"alpha": 0}}
	cd := &stubCdCounter{by: map[domain.InstanceName]int{"alpha": 0}}
	mtr := &stubStateMetrics{}
	bgWG := &sync.WaitGroup{}
	c := NewWatchdogStateCollector(bl, cd,
		stubInsts{list: []domain.InstanceName{"alpha"}},
		mtr, time.Second, bgWG, nil)
	ctx, cancel := context.WithCancel(context.Background())
	bgWG.Add(1)
	go c.Run(ctx)
	time.Sleep(20 * time.Millisecond)
	cancel()
	bgWG.Wait()
}

func TestWatchdogStateCollector_DefaultInterval(t *testing.T) {
	t.Parallel()
	c := NewWatchdogStateCollector(nil, nil, nil, nil, 0, nil, nil)
	if got := time.Duration(c.intervalNS.Load()); got != DefaultWatchdogStateInterval {
		t.Errorf("default interval = %v, want %v", got, DefaultWatchdogStateInterval)
	}
}
