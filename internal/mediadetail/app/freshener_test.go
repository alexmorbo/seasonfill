package app

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alexmorbo/seasonfill/internal/mediadetail/domain"
)

// fakePlugin is a configurable SectionPlugin test double exercising the dual
// Coverage/Staleness no-op contract + refresh coalescing.
type fakePlugin struct {
	section domain.Section

	covCovered int
	covTotal   int // 0 => coverage no-op
	covErr     error

	stale    bool
	staleErr error

	refreshErr   error
	refreshCount atomic.Int32
	refreshBlock chan struct{} // if non-nil, Refresh blocks until closed
}

func (p *fakePlugin) Coverage(context.Context, domain.MediaID, string) (int, int, error) {
	return p.covCovered, p.covTotal, p.covErr
}
func (p *fakePlugin) Staleness(context.Context, domain.MediaID, string, time.Time) (domain.SectionVerdict, error) {
	return domain.SectionVerdict{Section: p.section, Stale: p.stale}, p.staleErr
}
func (p *fakePlugin) Refresh(context.Context, domain.MediaID, string) error {
	p.refreshCount.Add(1)
	if p.refreshBlock != nil {
		<-p.refreshBlock
	}
	return p.refreshErr
}
func (p *fakePlugin) Section() domain.Section { return p.section }

func testID(t *testing.T) domain.MediaID {
	t.Helper()
	id, err := domain.NewMediaID(domain.MediaTypeMovie, 42, 0)
	if err != nil {
		t.Fatalf("NewMediaID: %v", err)
	}
	return id
}

func fixedClock() func() time.Time {
	now := time.Date(2026, 8, 19, 12, 0, 0, 0, time.UTC)
	return func() time.Time { return now }
}

func TestEnsureFreshEmptyRegistryIsFresh(t *testing.T) {
	reg := NewSectionRegistry()
	f := NewFreshener(reg, time.Second, fixedClock(), nil)
	got := f.EnsureFresh(context.Background(), testID(t), "ru-RU")
	if !got.Fresh || got.Refreshed || got.Degraded {
		t.Fatalf("empty registry: want Fresh, got %+v", got)
	}
}

func TestEnsureFreshInvalidIDIsFresh(t *testing.T) {
	f := NewFreshener(NewSectionRegistry(), time.Second, fixedClock(), nil)
	got := f.EnsureFresh(context.Background(), domain.MediaID{}, "ru-RU")
	if !got.Fresh {
		t.Fatalf("invalid id: want Fresh, got %+v", got)
	}
}

func TestEnsureFreshUnboundRegistryDegraded(t *testing.T) {
	f := NewFreshener(nil, time.Second, fixedClock(), nil)
	got := f.EnsureFresh(context.Background(), testID(t), "ru-RU")
	if !got.Degraded {
		t.Fatalf("nil registry: want Degraded, got %+v", got)
	}
}

func TestEnsureFreshClosedIsFresh(t *testing.T) {
	reg := NewSectionRegistry()
	reg.Register(domain.MediaTypeMovie, &fakePlugin{section: domain.SectionText, covTotal: 2, covCovered: 1})
	f := NewFreshener(reg, time.Second, fixedClock(), nil)
	f.Close()
	got := f.EnsureFresh(context.Background(), testID(t), "ru-RU")
	if !got.Fresh {
		t.Fatalf("closed: want Fresh, got %+v", got)
	}
}

func TestEnsureFreshStaleViaCoverageRefreshesOnce(t *testing.T) {
	// Coverage-shape stale (covered<total); Staleness no-ops (Stale:false).
	p := &fakePlugin{section: domain.SectionCast, covTotal: 3, covCovered: 1, stale: false}
	reg := NewSectionRegistry()
	reg.Register(domain.MediaTypeMovie, p)
	f := NewFreshener(reg, time.Second, fixedClock(), nil)
	got := f.EnsureFresh(context.Background(), testID(t), "ru-RU")
	if !got.Refreshed {
		t.Fatalf("coverage-stale: want Refreshed, got %+v", got)
	}
	if n := p.refreshCount.Load(); n != 1 {
		t.Fatalf("refreshCount = %d, want 1", n)
	}
}

func TestEnsureFreshStaleViaStalenessRefreshesOnce(t *testing.T) {
	// Staleness-shape stale; Coverage no-ops (total==0).
	p := &fakePlugin{section: domain.SectionText, covTotal: 0, stale: true}
	reg := NewSectionRegistry()
	reg.Register(domain.MediaTypeMovie, p)
	f := NewFreshener(reg, time.Second, fixedClock(), nil)
	got := f.EnsureFresh(context.Background(), testID(t), "ru-RU")
	if !got.Refreshed {
		t.Fatalf("staleness-stale: want Refreshed, got %+v", got)
	}
	if n := p.refreshCount.Load(); n != 1 {
		t.Fatalf("refreshCount = %d, want 1", n)
	}
}

func TestEnsureFreshNotStaleIsFreshNoRefresh(t *testing.T) {
	// Both no-op contracts: coverage total==0 AND staleness Stale:false.
	p := &fakePlugin{section: domain.SectionText, covTotal: 0, stale: false}
	reg := NewSectionRegistry()
	reg.Register(domain.MediaTypeMovie, p)
	f := NewFreshener(reg, time.Second, fixedClock(), nil)
	got := f.EnsureFresh(context.Background(), testID(t), "ru-RU")
	if !got.Fresh {
		t.Fatalf("not-stale: want Fresh, got %+v", got)
	}
	if n := p.refreshCount.Load(); n != 0 {
		t.Fatalf("refreshCount = %d, want 0", n)
	}
}

func TestEnsureFreshCoverageErrorFailsClosed(t *testing.T) {
	// Coverage errors → fail closed; staleness not stale → overall Fresh.
	p := &fakePlugin{section: domain.SectionText, covErr: errors.New("db down"), covTotal: 5, covCovered: 0, stale: false}
	reg := NewSectionRegistry()
	reg.Register(domain.MediaTypeMovie, p)
	f := NewFreshener(reg, time.Second, fixedClock(), nil)
	got := f.EnsureFresh(context.Background(), testID(t), "ru-RU")
	if !got.Fresh {
		t.Fatalf("coverage error: want Fresh (fail-closed), got %+v", got)
	}
	if n := p.refreshCount.Load(); n != 0 {
		t.Fatalf("refreshCount = %d, want 0", n)
	}
}

func TestEnsureFreshRefreshErrorDegradedAndNudges(t *testing.T) {
	p := &fakePlugin{section: domain.SectionText, stale: true, refreshErr: errors.New("tmdb 500")}
	reg := NewSectionRegistry()
	reg.Register(domain.MediaTypeMovie, p)
	f := NewFreshener(reg, time.Second, fixedClock(), nil)
	var nudged atomic.Int32
	f.SetAsyncFallback(func(domain.MediaID, string) { nudged.Add(1) })
	got := f.EnsureFresh(context.Background(), testID(t), "ru-RU")
	if !got.Degraded {
		t.Fatalf("refresh error: want Degraded, got %+v", got)
	}
	if nudged.Load() != 1 {
		t.Fatal("async fallback must be nudged on refresh error")
	}
}

func TestEnsureFreshTimeoutDegradedAndNudges(t *testing.T) {
	block := make(chan struct{})
	defer close(block)
	p := &fakePlugin{section: domain.SectionText, stale: true, refreshBlock: block}
	reg := NewSectionRegistry()
	reg.Register(domain.MediaTypeMovie, p)
	// Tiny sync budget so the blocked refresh trips the timeout branch.
	f := NewFreshener(reg, 30*time.Millisecond, fixedClock(), nil)
	var nudged atomic.Int32
	f.SetAsyncFallback(func(domain.MediaID, string) { nudged.Add(1) })
	got := f.EnsureFresh(context.Background(), testID(t), "ru-RU")
	if !got.Degraded {
		t.Fatalf("timeout: want Degraded, got %+v", got)
	}
	if nudged.Load() != 1 {
		t.Fatal("async fallback must be nudged on timeout")
	}
}

func TestEnsureFreshSingleflightCoalesces(t *testing.T) {
	block := make(chan struct{})
	p := &fakePlugin{section: domain.SectionText, stale: true, refreshBlock: block}
	reg := NewSectionRegistry()
	reg.Register(domain.MediaTypeMovie, p)
	// Large budget so no timeout; the coalescing is what we assert.
	f := NewFreshener(reg, 5*time.Second, fixedClock(), nil)
	id := testID(t)

	const n = 8
	var wg sync.WaitGroup
	results := make([]domain.FreshenResult, n)
	wg.Add(n)
	for i := range n {
		go func(i int) {
			defer wg.Done()
			results[i] = f.EnsureFresh(context.Background(), id, "ru-RU")
		}(i)
	}

	// Wait until at least one refresh has entered (coalesced leader), then
	// release. Poll the atomic counter instead of sleeping on a race.
	deadline := time.After(2 * time.Second)
	for p.refreshCount.Load() == 0 {
		select {
		case <-deadline:
			close(block)
			t.Fatal("refresh never started")
		default:
		}
	}
	// The leader is parked in Refresh on the block channel and cannot proceed
	// until we close it. Give the follower goroutines time to park inside
	// singleflight.Do behind that blocked leader so they coalesce onto it
	// instead of racing in after the leader forgets the key.
	time.Sleep(100 * time.Millisecond)
	close(block)
	wg.Wait()

	if n := p.refreshCount.Load(); n != 1 {
		t.Fatalf("singleflight: refreshCount = %d, want 1 (coalesced)", n)
	}
	for i, r := range results {
		if !r.Refreshed {
			t.Errorf("caller %d: want Refreshed, got %+v", i, r)
		}
	}
}
