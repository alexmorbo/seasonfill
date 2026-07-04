package loops

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	catalogpersistence "github.com/alexmorbo/seasonfill/internal/catalog/persistence"
)

type stubPosterCoverageRepo struct {
	calls    atomic.Int32
	coverage catalogpersistence.LibraryPosterCoverage
	err      error
}

func (s *stubPosterCoverageRepo) LibraryPosterCoverage(_ context.Context) (catalogpersistence.LibraryPosterCoverage, error) {
	s.calls.Add(1)
	if s.err != nil {
		return catalogpersistence.LibraryPosterCoverage{}, s.err
	}
	return s.coverage, nil
}

type stubPosterCoverageMetrics struct {
	mu             sync.Mutex
	covered, total int64
	sets           int
}

func (s *stubPosterCoverageMetrics) SetLibraryPosterCoverage(covered, total int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.covered, s.total = covered, total
	s.sets++
}

func TestLibraryPosterCoverageLoop_TicksAndPublishes(t *testing.T) {
	t.Parallel()
	repo := &stubPosterCoverageRepo{coverage: catalogpersistence.LibraryPosterCoverage{Covered: 69, Total: 120}}
	mtr := &stubPosterCoverageMetrics{}
	loop := NewLibraryPosterCoverageLoop(repo, mtr, 50*time.Millisecond, &sync.WaitGroup{}, nil)

	ctx, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
	defer cancel()
	wg := &sync.WaitGroup{}
	wg.Go(func() {
		loop.bgWG.Add(1)
		loop.Run(ctx)
	})
	wg.Wait()

	if got := repo.calls.Load(); got < 2 {
		t.Errorf("expected >=2 repo calls, got %d", got)
	}
	mtr.mu.Lock()
	defer mtr.mu.Unlock()
	if mtr.covered != 69 || mtr.total != 120 {
		t.Errorf("metrics not updated: covered=%d total=%d", mtr.covered, mtr.total)
	}
}

func TestLibraryPosterCoverageLoop_QueryErrorSkipsTick(t *testing.T) {
	t.Parallel()
	repo := &stubPosterCoverageRepo{err: errors.New("boom")}
	mtr := &stubPosterCoverageMetrics{}
	bgWG := &sync.WaitGroup{}
	loop := NewLibraryPosterCoverageLoop(repo, mtr, 50*time.Millisecond, bgWG, nil)

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	bgWG.Add(1)
	loop.Run(ctx)

	if repo.calls.Load() < 2 {
		t.Errorf("loop should keep ticking on query errors, got %d calls", repo.calls.Load())
	}
	mtr.mu.Lock()
	defer mtr.mu.Unlock()
	if mtr.sets != 0 {
		t.Errorf("no gauge should be published on query error, got %d sets", mtr.sets)
	}
}

func TestLibraryPosterCoverageLoop_DrainsOnContextCancel(t *testing.T) {
	t.Parallel()
	repo := &stubPosterCoverageRepo{coverage: catalogpersistence.LibraryPosterCoverage{Covered: 1, Total: 1}}
	mtr := &stubPosterCoverageMetrics{}
	bgWG := &sync.WaitGroup{}
	loop := NewLibraryPosterCoverageLoop(repo, mtr, time.Second, bgWG, nil)

	ctx, cancel := context.WithCancel(context.Background())
	bgWG.Add(1)
	go loop.Run(ctx)
	time.Sleep(20 * time.Millisecond)
	cancel()
	bgWG.Wait()
}
