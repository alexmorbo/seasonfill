package loops

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	enrichpersistence "github.com/alexmorbo/seasonfill/internal/enrichment/persistence"
)

type stubEnrichCoverageRepo struct {
	calls atomic.Int32
	cov   enrichpersistence.EnrichmentCoverage
	err   error
}

func (s *stubEnrichCoverageRepo) EnrichmentCoverage(_ context.Context) (enrichpersistence.EnrichmentCoverage, error) {
	s.calls.Add(1)
	if s.err != nil {
		return enrichpersistence.EnrichmentCoverage{}, s.err
	}
	return s.cov, nil
}

type stubEnrichCoverageMetrics struct {
	mu         sync.Mutex
	ratios     map[string]float64
	checked    map[string]int64
	unenriched map[string]int64
	sets       int
}

func newStubEnrichCoverageMetrics() *stubEnrichCoverageMetrics {
	return &stubEnrichCoverageMetrics{
		ratios:     map[string]float64{},
		checked:    map[string]int64{},
		unenriched: map[string]int64{},
	}
}

func (s *stubEnrichCoverageMetrics) SetPosterCoverageRatio(lang string, ratio float64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ratios[lang] = ratio
	s.sets++
}

func (s *stubEnrichCoverageMetrics) SetCheckedEmpty(kind string, n int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.checked[kind] = n
	s.sets++
}

func (s *stubEnrichCoverageMetrics) SetUnenrichedSeries(reason string, n int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.unenriched[reason] = n
	s.sets++
}

func TestEnrichmentCoverageLoop_TicksAndPublishes(t *testing.T) {
	t.Parallel()
	repo := &stubEnrichCoverageRepo{cov: enrichpersistence.EnrichmentCoverage{
		LibraryTotal:        4,
		PosterCoveredByLang: map[string]int64{"en-US": 3, "ru-RU": 1},
		CheckedEmpty:        map[string]int64{"poster": 2, "backdrop": 0},
		Unenriched:          map[string]int64{"no_tmdb_id": 1, "never_synced": 5},
	}}
	mtr := newStubEnrichCoverageMetrics()
	loop := NewEnrichmentCoverageLoop(repo, mtr, 50*time.Millisecond, &sync.WaitGroup{}, nil)

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
	if mtr.ratios["en-US"] != 0.75 {
		t.Errorf("en-US ratio = %v, want 0.75", mtr.ratios["en-US"])
	}
	if mtr.ratios["ru-RU"] != 0.25 {
		t.Errorf("ru-RU ratio = %v, want 0.25", mtr.ratios["ru-RU"])
	}
	if mtr.checked["poster"] != 2 || mtr.checked["backdrop"] != 0 {
		t.Errorf("checked = %+v, want poster=2 backdrop=0", mtr.checked)
	}
	if mtr.unenriched["no_tmdb_id"] != 1 || mtr.unenriched["never_synced"] != 5 {
		t.Errorf("unenriched = %+v, want no_tmdb_id=1 never_synced=5", mtr.unenriched)
	}
}

func TestEnrichmentCoverageLoop_EmptyLibraryRatioIsOne(t *testing.T) {
	t.Parallel()
	repo := &stubEnrichCoverageRepo{cov: enrichpersistence.EnrichmentCoverage{
		LibraryTotal:        0,
		PosterCoveredByLang: map[string]int64{},
		CheckedEmpty:        map[string]int64{},
		Unenriched:          map[string]int64{},
	}}
	mtr := newStubEnrichCoverageMetrics()
	loop := NewEnrichmentCoverageLoop(repo, mtr, 50*time.Millisecond, &sync.WaitGroup{}, nil)

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Millisecond)
	defer cancel()
	loop.bgWG.Add(1)
	loop.Run(ctx)

	mtr.mu.Lock()
	defer mtr.mu.Unlock()
	if mtr.ratios["en-US"] != 1.0 || mtr.ratios["ru-RU"] != 1.0 {
		t.Errorf("empty library must read ratio 1.0 per lang, got %+v", mtr.ratios)
	}
}

func TestEnrichmentCoverageLoop_QueryErrorSkipsTick(t *testing.T) {
	t.Parallel()
	repo := &stubEnrichCoverageRepo{err: errors.New("boom")}
	mtr := newStubEnrichCoverageMetrics()
	bgWG := &sync.WaitGroup{}
	loop := NewEnrichmentCoverageLoop(repo, mtr, 50*time.Millisecond, bgWG, nil)

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
