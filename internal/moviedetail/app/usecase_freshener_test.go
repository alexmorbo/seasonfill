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

// sequencingCanon returns a different canon per GetByTMDBID call, so a test can
// model "stale on first read, fresh after HandleForced + re-read".
type sequencingCanon struct {
	rows  []movie.Canon
	calls int
}

func (s *sequencingCanon) GetByTMDBID(_ context.Context, _ domain.TMDBID) (movie.Canon, error) {
	i := s.calls
	if i >= len(s.rows) {
		i = len(s.rows) - 1
	}
	s.calls++
	return s.rows[i], nil
}

// stubFreshener returns a canned FreshenResult and records the call.
type stubFreshener struct {
	result FreshenResult
	calls  int
}

func (f *stubFreshener) EnsureFresh(_ context.Context, _ movie.Canon, _ string) FreshenResult {
	f.calls++
	return f.result
}

func TestUseCase_Get_FreshenerRefreshed_RereadsFreshCanon(t *testing.T) {
	t.Parallel()
	tid := domain.TMDBID(693134)
	stale := movie.Canon{ID: domain.MovieID(42), TMDBID: &tid, Title: "Stub Title", Hydration: movie.HydrationStub}
	fresh := movie.Canon{ID: domain.MovieID(42), TMDBID: &tid, Title: "Real Title", Hydration: movie.HydrationFull}
	canonReader := &sequencingCanon{rows: []movie.Canon{stale, fresh}}
	fr := &stubFreshener{result: FreshenResult{Refreshed: true}}
	stub := &fakeStale{}

	uc := New(
		canonReader,
		fakeI18n{err: ports.ErrNotFound}, // no localized row → hero falls back to canon.Title
		fakeCollection{},
		fakeMembership{},
	).WithHydrationTrigger(stub, fixedClock(time.Now()), discardLog()).
		WithFreshener(fr)

	d, err := uc.Get(context.Background(), tid, "ru-RU")
	require.NoError(t, err)
	assert.Equal(t, 1, fr.calls, "freshener consulted once before assembly")
	assert.Equal(t, 2, canonReader.calls, "canon re-read after a successful refresh")
	assert.Equal(t, "Real Title", d.Title, "assembled hero reflects the re-read fresh canon")
	assert.Empty(t, stub.calls, "no async fallback when the sync freshener refreshed")
	assert.NotContains(t, d.Degraded, "enrichment")
}

func TestUseCase_Get_FreshenerDegraded_FallsBackAndMarksDegraded(t *testing.T) {
	t.Parallel()
	tid := domain.TMDBID(693134)
	// Stub + tmdb_id → probe reports stale, so the async fallback (mark-stale) fires.
	canon := movie.Canon{ID: domain.MovieID(42), TMDBID: &tid, Title: "Stub", Hydration: movie.HydrationStub}
	canonReader := &sequencingCanon{rows: []movie.Canon{canon}}
	fr := &stubFreshener{result: FreshenResult{Degraded: true}}
	stub := &fakeStale{}

	uc := New(
		canonReader,
		fakeI18n{err: ports.ErrNotFound},
		fakeCollection{},
		fakeMembership{},
	).WithHydrationTrigger(stub, fixedClock(time.Now()), discardLog()).
		WithFreshener(fr)

	d, err := uc.Get(context.Background(), tid, "ru-RU")
	require.NoError(t, err)
	assert.Equal(t, 1, fr.calls)
	assert.Equal(t, 1, canonReader.calls, "no re-read on degrade (nothing was written)")
	require.Len(t, stub.calls, 1, "degrade → async mark-stale fallback fires")
	assert.Equal(t, domain.MovieID(42), stub.calls[0])
	assert.Contains(t, d.Degraded, "enrichment", "degraded[] surfaces the enrichment shortfall")
}

func TestUseCase_Get_FreshenerFresh_NoFallbackNoReread(t *testing.T) {
	t.Parallel()
	tid := domain.TMDBID(693134)
	canon := movie.Canon{ID: domain.MovieID(42), TMDBID: &tid, Title: "Fresh", Hydration: movie.HydrationFull}
	canonReader := &sequencingCanon{rows: []movie.Canon{canon}}
	fr := &stubFreshener{result: FreshenResult{Fresh: true}}
	stub := &fakeStale{}

	uc := New(
		canonReader,
		fakeI18n{err: ports.ErrNotFound},
		fakeCollection{},
		fakeMembership{},
	).WithHydrationTrigger(stub, fixedClock(time.Now()), discardLog()).
		WithFreshener(fr)

	d, err := uc.Get(context.Background(), tid, "ru-RU")
	require.NoError(t, err)
	assert.Equal(t, 1, canonReader.calls, "fresh → no re-read")
	assert.Empty(t, stub.calls, "fresh → no async fallback")
	assert.NotContains(t, d.Degraded, "enrichment")
}
