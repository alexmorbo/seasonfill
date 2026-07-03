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

type stubCountsRepo struct {
	calls  atomic.Int32
	counts catalogpersistence.CatalogCounts
	err    error
}

func (s *stubCountsRepo) Counts(_ context.Context) (catalogpersistence.CatalogCounts, error) {
	s.calls.Add(1)
	if s.err != nil {
		return catalogpersistence.CatalogCounts{}, s.err
	}
	return s.counts, nil
}

type stubCountsMetrics struct {
	mu                        sync.Mutex
	series, seasons, episodes int64
	sets                      int
}

func (s *stubCountsMetrics) SetCatalogCounts(series, seasons, episodes int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.series, s.seasons, s.episodes = series, seasons, episodes
	s.sets++
}

func TestCatalogCountsLoop_TicksAndPublishes(t *testing.T) {
	t.Parallel()
	repo := &stubCountsRepo{counts: catalogpersistence.CatalogCounts{Series: 12, Seasons: 34, Episodes: 560}}
	mtr := &stubCountsMetrics{}
	loop := NewCatalogCountsLoop(repo, mtr, 50*time.Millisecond, &sync.WaitGroup{}, nil)

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
	if mtr.series != 12 || mtr.seasons != 34 || mtr.episodes != 560 {
		t.Errorf("metrics not updated: series=%d seasons=%d episodes=%d",
			mtr.series, mtr.seasons, mtr.episodes)
	}
}

func TestCatalogCountsLoop_QueryErrorSkipsTick(t *testing.T) {
	t.Parallel()
	repo := &stubCountsRepo{err: errors.New("boom")}
	mtr := &stubCountsMetrics{}
	bgWG := &sync.WaitGroup{}
	loop := NewCatalogCountsLoop(repo, mtr, 50*time.Millisecond, bgWG, nil)

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

func TestCatalogCountsLoop_DrainsOnContextCancel(t *testing.T) {
	t.Parallel()
	repo := &stubCountsRepo{counts: catalogpersistence.CatalogCounts{Series: 1}}
	mtr := &stubCountsMetrics{}
	bgWG := &sync.WaitGroup{}
	loop := NewCatalogCountsLoop(repo, mtr, time.Second, bgWG, nil)

	ctx, cancel := context.WithCancel(context.Background())
	bgWG.Add(1)
	go loop.Run(ctx)
	time.Sleep(20 * time.Millisecond)
	cancel()
	bgWG.Wait()
}
