package app

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/internal/catalog/domain/movie"
	ports "github.com/alexmorbo/seasonfill/internal/shared/dataports"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
)

// stubSeqCanon returns a (canon, err) pair per GetByTMDBID call so a test can
// model "not-found on first read, stub after EnsureStub, fresh after refresh".
// The last entry repeats once exhausted.
type stubSeqCanon struct {
	seq   []stubCanonStep
	calls int
}

type stubCanonStep struct {
	canon movie.Canon
	err   error
}

func (s *stubSeqCanon) GetByTMDBID(_ context.Context, _ domain.TMDBID) (movie.Canon, error) {
	i := s.calls
	if i >= len(s.seq) {
		i = len(s.seq) - 1
	}
	s.calls++
	return s.seq[i].canon, s.seq[i].err
}

// spyStubResolver records EnsureStub calls and returns a canned error.
type spyStubResolver struct {
	err   error
	calls int
}

func (s *spyStubResolver) EnsureStub(_ context.Context, _ domain.TMDBID, _ string) error {
	s.calls++
	return s.err
}

// TestUseCase_Get_UnknownTMDBResolves_StubCreatedThenHydrated proves the S2 path:
// an unknown tmdb id that TMDB resolves is stub-created, re-read, hydrated by the
// sync freshener, and returned 200 (NOT 404).
func TestUseCase_Get_UnknownTMDBResolves_StubCreatedThenHydrated(t *testing.T) {
	t.Parallel()
	tid := domain.TMDBID(1315772)
	stub := movie.Canon{ID: domain.MovieID(77), TMDBID: &tid, Title: "Seed Title", Hydration: movie.HydrationStub}
	fresh := movie.Canon{ID: domain.MovieID(77), TMDBID: &tid, Title: "Real Title", Hydration: movie.HydrationFull}
	canonReader := &stubSeqCanon{seq: []stubCanonStep{
		{err: ports.ErrNotFound}, // initial lookup misses
		{canon: stub},            // re-read after EnsureStub
		{canon: fresh},           // re-read after freshener Refreshed
	}}
	resolver := &spyStubResolver{} // resolves OK (nil err)
	fr := &stubFreshener{result: FreshenResult{Refreshed: true}}

	uc := New(
		canonReader,
		fakeI18n{err: ports.ErrNotFound}, // no localized row → hero uses canon.Title
		fakeCollection{},
		fakeMembership{},
	).WithStubResolver(resolver).WithFreshener(fr)

	d, err := uc.Get(context.Background(), tid, "ru-RU")
	require.NoError(t, err, "resolved tmdb → 200, not 404")
	assert.Equal(t, 1, resolver.calls, "stub resolver consulted exactly once on the miss")
	assert.Equal(t, 3, canonReader.calls, "miss → EnsureStub re-read → freshener re-read")
	assert.Equal(t, 1, fr.calls, "freshener consulted on the fresh stub")
	assert.Equal(t, "Real Title", d.Title, "assembled hero reflects the hydrated canon")
}

// TestUseCase_Get_UnknownTMDBNotFound_Still404_NoStub proves the guard: a bogus
// tmdb id (resolver returns ErrNotFound — TMDB has no such movie) still bubbles
// ErrNotFound (→ 404) and the canon is NOT re-read (no row was written).
func TestUseCase_Get_UnknownTMDBNotFound_Still404_NoStub(t *testing.T) {
	t.Parallel()
	tid := domain.TMDBID(999999999)
	canonReader := &stubSeqCanon{seq: []stubCanonStep{{err: ports.ErrNotFound}}}
	resolver := &spyStubResolver{err: ports.ErrNotFound} // TMDB not-found → no stub
	fr := &stubFreshener{result: FreshenResult{Fresh: true}}

	uc := New(
		canonReader,
		fakeI18n{err: ports.ErrNotFound},
		fakeCollection{},
		fakeMembership{},
	).WithStubResolver(resolver).WithFreshener(fr)

	_, err := uc.Get(context.Background(), tid, "ru-RU")
	require.Error(t, err)
	assert.True(t, errors.Is(err, ports.ErrNotFound), "bogus tmdb still maps to 404")
	assert.Equal(t, 1, resolver.calls, "resolver consulted once")
	assert.Equal(t, 1, canonReader.calls, "no re-read — nothing was written")
	assert.Equal(t, 0, fr.calls, "freshener never runs on a 404")
}

// TestUseCase_Get_UnknownTMDBResolverError_500 proves a non-ErrNotFound resolver
// error (e.g. TMDB 5xx) surfaces as-is so the handler maps it to 500 (NOT 404).
func TestUseCase_Get_UnknownTMDBResolverError_500(t *testing.T) {
	t.Parallel()
	tid := domain.TMDBID(1315772)
	canonReader := &stubSeqCanon{seq: []stubCanonStep{{err: ports.ErrNotFound}}}
	resolver := &spyStubResolver{err: errors.New("tmdb upstream 503")}

	uc := New(
		canonReader,
		fakeI18n{err: ports.ErrNotFound},
		fakeCollection{},
		fakeMembership{},
	).WithStubResolver(resolver)

	_, err := uc.Get(context.Background(), tid, "ru-RU")
	require.Error(t, err)
	assert.False(t, errors.Is(err, ports.ErrNotFound), "a transport error is 500, not 404")
	assert.Equal(t, 1, resolver.calls)
	assert.Equal(t, 1, canonReader.calls, "no re-read after a resolver error")
}

// TestUseCase_Get_KnownMovie_NoResolverCall proves the existing in-DB path is
// unchanged: a present canon never consults the resolver (no double-insert).
func TestUseCase_Get_KnownMovie_NoResolverCall(t *testing.T) {
	t.Parallel()
	tid := domain.TMDBID(693134)
	existing := movie.Canon{ID: domain.MovieID(42), TMDBID: &tid, Title: "Dune: Part Two", Hydration: movie.HydrationFull}
	canonReader := &stubSeqCanon{seq: []stubCanonStep{{canon: existing}}}
	resolver := &spyStubResolver{}

	uc := New(
		canonReader,
		fakeI18n{err: ports.ErrNotFound},
		fakeCollection{},
		fakeMembership{},
	).WithStubResolver(resolver)

	d, err := uc.Get(context.Background(), tid, "ru-RU")
	require.NoError(t, err)
	assert.Equal(t, 0, resolver.calls, "present canon → resolver never consulted")
	assert.Equal(t, 1, canonReader.calls, "single read, no re-read")
	assert.Equal(t, "Dune: Part Two", d.Title)
}

// TestUseCase_Get_UnknownTMDB_NoResolverWired_Still404 proves a usecase without a
// stub resolver keeps the pre-S2 behaviour (unknown tmdb → 404 bubbles unchanged).
func TestUseCase_Get_UnknownTMDB_NoResolverWired_Still404(t *testing.T) {
	t.Parallel()
	tid := domain.TMDBID(1315772)
	canonReader := &stubSeqCanon{seq: []stubCanonStep{{err: ports.ErrNotFound}}}

	uc := New(
		canonReader,
		fakeI18n{err: ports.ErrNotFound},
		fakeCollection{},
		fakeMembership{},
	) // no WithStubResolver

	_, err := uc.Get(context.Background(), tid, "ru-RU")
	require.Error(t, err)
	assert.True(t, errors.Is(err, ports.ErrNotFound), "unwired resolver → pre-S2 404")
	assert.Equal(t, 1, canonReader.calls)
}
