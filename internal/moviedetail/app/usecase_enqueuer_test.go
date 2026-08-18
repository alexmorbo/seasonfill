package app

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/internal/catalog/domain/movie"
	ports "github.com/alexmorbo/seasonfill/internal/shared/dataports"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
)

// spyEnqueuer records EnqueueMovieHot calls (S1b Hot-lane fallback assertion).
type spyEnqueuer struct {
	calls []domain.MovieID
}

func (s *spyEnqueuer) EnqueueMovieHot(movieID domain.MovieID) {
	s.calls = append(s.calls, movieID)
}

// Degraded freshener → the async fallback fires BOTH the mark-stale nudge AND
// the S1b Hot-lane enqueue for the same movie id.
func TestUseCase_Get_FreshenerDegraded_EnqueuesHotAndMarksStale(t *testing.T) {
	t.Parallel()
	tid := domain.TMDBID(693134)
	// Stub + tmdb_id → MovieProbe reports stale → fallback path runs.
	canon := movie.Canon{ID: domain.MovieID(42), TMDBID: &tid, Title: "Stub", Hydration: movie.HydrationStub}
	canonReader := &sequencingCanon{rows: []movie.Canon{canon}}
	fr := &stubFreshener{result: FreshenResult{Degraded: true}}
	stale := &fakeStale{}
	spy := &spyEnqueuer{}

	uc := New(
		canonReader,
		fakeI18n{err: ports.ErrNotFound},
		fakeCollection{},
		fakeMembership{},
	).WithHydrationTrigger(stale, fixedClock(time.Now()), discardLog()).
		WithFreshener(fr).
		WithEnrichmentEnqueuer(spy)

	d, err := uc.Get(context.Background(), tid, "ru-RU")
	require.NoError(t, err)
	require.Len(t, stale.calls, 1, "degrade → mark-stale background nudge still fires")
	assert.Equal(t, domain.MovieID(42), stale.calls[0])
	require.Len(t, spy.calls, 1, "degrade → S1b Hot-lane enqueue fires ALONGSIDE mark-stale")
	assert.Equal(t, domain.MovieID(42), spy.calls[0])
	assert.Contains(t, d.Degraded, "enrichment")
}

// Successful sync refresh → NEITHER fallback fires (no double-enqueue, no
// redundant mark-stale). The negative case.
func TestUseCase_Get_FreshenerRefreshed_NoHotEnqueueNoMarkStale(t *testing.T) {
	t.Parallel()
	tid := domain.TMDBID(693134)
	staleRow := movie.Canon{ID: domain.MovieID(42), TMDBID: &tid, Title: "Stub", Hydration: movie.HydrationStub}
	freshRow := movie.Canon{ID: domain.MovieID(42), TMDBID: &tid, Title: "Real", Hydration: movie.HydrationFull}
	canonReader := &sequencingCanon{rows: []movie.Canon{staleRow, freshRow}}
	fr := &stubFreshener{result: FreshenResult{Refreshed: true}}
	staleMarker := &fakeStale{}
	spy := &spyEnqueuer{}

	uc := New(
		canonReader,
		fakeI18n{err: ports.ErrNotFound},
		fakeCollection{},
		fakeMembership{},
	).WithHydrationTrigger(staleMarker, fixedClock(time.Now()), discardLog()).
		WithFreshener(fr).
		WithEnrichmentEnqueuer(spy)

	_, err := uc.Get(context.Background(), tid, "ru-RU")
	require.NoError(t, err)
	assert.Empty(t, spy.calls, "sync refresh succeeded → no Hot-lane fallback")
	assert.Empty(t, staleMarker.calls, "sync refresh succeeded → no mark-stale fallback")
}
