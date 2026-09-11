package wiring

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/cmd/server/adapters"
	appenrich "github.com/alexmorbo/seasonfill/internal/enrichment/app"
	"github.com/alexmorbo/seasonfill/internal/enrichment/domain/enrichment"
	"github.com/alexmorbo/seasonfill/internal/shared/clients/tmdb"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
)

// countingSeriesStaleScanner records how many times ListStaleForTMDB
// is called. Zero is the success signal for the empty-holder gate test
// (the gate must fire BEFORE this scanner runs).
type countingSeriesStaleScanner struct{ calls atomic.Int32 }

func (c *countingSeriesStaleScanner) ListStaleForTMDB(_ context.Context, _ time.Duration, _ int) ([]domain.SeriesID, error) {
	c.calls.Add(1)
	return nil, nil
}

type countingPeopleStaleScanner struct{ calls atomic.Int32 }

func (c *countingPeopleStaleScanner) ListStaleForTMDB(_ context.Context, _ time.Duration, _ int) ([]int64, error) {
	c.calls.Add(1)
	return nil, nil
}

// countingErrorRepo tracks ListDueForRetry calls. The other
// EnrichmentErrorRepo methods panic — the nightly tick must never reach
// them on the gated path, and a panic surfaces drift immediately rather
// than papering over a regression.
type countingErrorRepo struct {
	listCalls atomic.Int32
}

func (c *countingErrorRepo) ListDueForRetry(_ context.Context, _ enrichment.Source, _ time.Time, _ int) ([]enrichment.EnrichmentError, error) {
	c.listCalls.Add(1)
	return nil, nil
}

func (c *countingErrorRepo) RecordFailure(context.Context, enrichment.EnrichmentError) error {
	panic("RecordFailure must not be called from runNightlyTick gate path")
}

func (c *countingErrorRepo) ClearOnSuccess(context.Context, enrichment.EntityType, int64, enrichment.Source) error {
	panic("ClearOnSuccess must not be called from runNightlyTick gate path")
}

func (c *countingErrorRepo) GetForEntity(context.Context, enrichment.EntityType, int64) ([]enrichment.EnrichmentError, error) {
	panic("GetForEntity must not be called from runNightlyTick gate path")
}

func (c *countingErrorRepo) GetByEntitySource(context.Context, enrichment.EntityType, int64, enrichment.Source) (enrichment.EnrichmentError, error) {
	panic("GetByEntitySource must not be called from runNightlyTick gate path")
}

// TestRunNightlyTick_SkippedWhenHolderEmpty verifies the B-23 gate:
// when tmdbHolder.Load() returns nil (unconfigured install) the nightly
// stale rescan MUST short-circuit BEFORE touching any DB scanner or
// the enrichment_errors retry-list. The skip log must carry
// reason="tmdb_holder_empty" so operators on DEBUG can verify the gate
// fired.
func TestRunNightlyTick_SkippedWhenHolderEmpty(t *testing.T) {
	t.Parallel()

	holder := adapters.NewTMDBClientHolder() // empty — Load returns nil
	require.Nil(t, holder.Load(), "precondition: holder must be empty")

	series := &countingSeriesStaleScanner{}
	people := &countingPeopleStaleScanner{}
	errs := &countingErrorRepo{}

	var buf bytes.Buffer
	log := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	runNightlyTick(context.Background(), nightlyTickDeps{
		TMDBHolder:       holder,
		SeriesStaleScan:  series,
		PeopleStaleScan:  people,
		EnrichmentErrors: errs,
		Dispatcher:       nil, // gate fires before any Enqueue — nil-safe
		Log:              log,
	})

	assert.Equal(t, int32(0), series.calls.Load(),
		"series stale scanner MUST NOT be called when holder is empty")
	assert.Equal(t, int32(0), people.calls.Load(),
		"people stale scanner MUST NOT be called when holder is empty")
	assert.Equal(t, int32(0), errs.listCalls.Load(),
		"enrichment_errors retry-list MUST NOT be queried when holder is empty")

	out := buf.String()
	assert.Contains(t, out, `"msg":"enrichment.nightly.skipped"`,
		"skip log line must fire so operators can confirm the gate")
	assert.Contains(t, out, `"reason":"tmdb_holder_empty"`,
		"skip log must carry the reason field")
	assert.NotContains(t, out, `"msg":"enrichment.nightly.swept"`,
		"swept summary must NOT fire on the gated path")
}

// stubRetryErrorRepo hands out canned ListDueForRetry rows per source and records
// which sources the tick asked for. Unlike countingErrorRepo above it does NOT
// panic on the other methods: this test drives the FULL tick rather than the
// gated path, and a panic would hide which arm actually ran.
type stubRetryErrorRepo struct {
	mu       sync.Mutex
	bySource map[enrichment.Source][]enrichment.EnrichmentError
	asked    []enrichment.Source
}

func (s *stubRetryErrorRepo) ListDueForRetry(_ context.Context, src enrichment.Source, _ time.Time, _ int) ([]enrichment.EnrichmentError, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.asked = append(s.asked, src)
	return s.bySource[src], nil
}

func (s *stubRetryErrorRepo) RecordFailure(context.Context, enrichment.EnrichmentError) error {
	return nil
}

func (s *stubRetryErrorRepo) ClearOnSuccess(context.Context, enrichment.EntityType, int64, enrichment.Source) error {
	return nil
}

func (s *stubRetryErrorRepo) GetForEntity(context.Context, enrichment.EntityType, int64) ([]enrichment.EnrichmentError, error) {
	return nil, nil
}

func (s *stubRetryErrorRepo) GetByEntitySource(context.Context, enrichment.EntityType, int64, enrichment.Source) (enrichment.EnrichmentError, error) {
	return enrichment.EnrichmentError{}, nil
}

func (s *stubRetryErrorRepo) askedFor(src enrichment.Source) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, got := range s.asked {
		if got == src {
			return true
		}
	}
	return false
}

// TestRunNightlyTick_SweepsMovieRetriesIntoTheMovieLane is the behavioural half
// of ADR-0025's retry_sweep/movie invariant. It drives the REAL dispatcher — not
// a recording fake — because the thing that can silently break is the ENTITY
// KIND: enqueuing movie ids under EntitySeries would compile, would tick the
// same counters, and would hand movie ids to the series worker. Only a real
// drain through DispatcherImpl.movieLoop proves the ids landed in the movie lane.
func TestRunNightlyTick_SweepsMovieRetriesIntoTheMovieLane(t *testing.T) {
	t.Parallel()

	holder := adapters.NewTMDBClientHolder()
	holder.Set(&tmdb.Client{}) // non-nil → the B-23 gate lets the tick run
	require.NotNil(t, holder.Load(), "precondition: the tick must not short-circuit")

	errs := &stubRetryErrorRepo{bySource: map[enrichment.Source][]enrichment.EnrichmentError{
		enrichment.SourceTMDBMovie: {
			{
				EntityType: enrichment.EntityTypeMovie,
				EntityID:   4242,
				Source:     enrichment.SourceTMDBMovie,
				Attempts:   2,
			},
			{
				EntityType: enrichment.EntityTypeMovie,
				EntityID:   777,
				Source:     enrichment.SourceTMDBMovie,
				Attempts:   1,
			},
		},
	}}

	var (
		mu      sync.Mutex
		drained []int64
	)
	quiet := slog.New(slog.NewJSONHandler(io.Discard, nil))
	dispatcher := appenrich.NewDispatcher(appenrich.Workers{
		SeriesHandler: func(context.Context, int64) error { return nil },
		PersonHandler: func(context.Context, int64) error { return nil },
		MovieHandler: func(_ context.Context, id int64) error {
			mu.Lock()
			drained = append(drained, id)
			mu.Unlock()
			return nil
		},
	}, quiet)

	// t.Context() is cancelled just BEFORE the t.Cleanup stack runs, so the
	// dispatcher's loops unwind before Close waits on them.
	ctx := t.Context()
	dispatcher.Start(ctx)
	t.Cleanup(dispatcher.Close)

	var buf bytes.Buffer
	log := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	runNightlyTick(ctx, nightlyTickDeps{
		TMDBHolder:       holder,
		SeriesStaleScan:  &countingSeriesStaleScanner{},
		PeopleStaleScan:  &countingPeopleStaleScanner{},
		EnrichmentErrors: errs,
		Dispatcher:       dispatcher,
		Log:              log,
	})

	assert.True(t, errs.askedFor(enrichment.SourceTMDBSeries), "series arm must still run")
	assert.True(t, errs.askedFor(enrichment.SourceTMDBPerson), "person arm must still run")
	assert.True(t, errs.askedFor(enrichment.SourceTMDBMovie),
		"the nightly tick must ask enrichment_errors for due MOVIE retries — without "+
			"this arm the ADR-0025 F1 journal is write-only")

	require.Eventually(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(drained) == 2
	}, 5*time.Second, 10*time.Millisecond,
		"both journalled movies must reach the dispatcher's EntityMovie lane")

	mu.Lock()
	got := append([]int64(nil), drained...)
	mu.Unlock()
	assert.ElementsMatch(t, []int64{4242, 777}, got)

	assert.Contains(t, buf.String(), `"movie_retries":2`,
		"the swept summary must report the movie arm so an operator can see it ran")
}
