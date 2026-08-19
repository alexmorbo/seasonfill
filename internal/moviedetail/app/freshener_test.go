package app

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/alexmorbo/seasonfill/internal/catalog/domain/movie"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
)

// blockingRefresher records HandleForced invocations and optionally blocks until
// release is closed. entered is signalled once per invocation so a test can
// synchronize on "the leader is inside HandleForced".
type blockingRefresher struct {
	calls   atomic.Int64
	entered chan struct{}
	release chan struct{}
	err     error
}

func (r *blockingRefresher) HandleForced(ctx context.Context, movieID int64) error {
	r.calls.Add(1)
	if r.entered != nil {
		select {
		case r.entered <- struct{}{}:
		default:
		}
	}
	if r.release != nil {
		select {
		case <-r.release:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return r.err
}

func staleCanon() movie.Canon {
	tid := domain.TMDBID(693134)
	// HydrationStub → MovieProbe marks every section stale ("stub").
	return movie.Canon{ID: domain.MovieID(42), TMDBID: &tid, Title: "Cold", Hydration: movie.HydrationStub}
}

func freshCanon(now time.Time) movie.Canon {
	tid := domain.TMDBID(693134)
	recent := now.Add(-1 * time.Hour)
	return movie.Canon{
		ID: domain.MovieID(42), TMDBID: &tid, Title: "Fresh", Hydration: movie.HydrationFull,
		EnrichmentTextSyncedAt:     new(recent),
		EnrichmentCastSyncedAt:     new(recent),
		EnrichmentRecsSyncedAt:     new(recent),
		EnrichmentMediaSyncedAt:    new(recent),
		EnrichmentKeywordsSyncedAt: new(recent),
	}
}

func TestMovieFreshener_Stale_RunsHandleForced_Refreshed(t *testing.T) {
	t.Parallel()
	r := &blockingRefresher{} // no block: returns immediately
	f := NewMovieFreshener(5*time.Second, time.Now, discardLog())
	f.Set(r)

	res := f.EnsureFresh(context.Background(), staleCanon(), "ru-RU")

	assert.True(t, res.Refreshed, "stale movie is refreshed synchronously")
	assert.False(t, res.Degraded)
	assert.Equal(t, int64(1), r.calls.Load(), "HandleForced called exactly once")
}

func TestMovieFreshener_Fresh_SkipsHandleForced(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 18, 12, 0, 0, 0, time.UTC)
	r := &blockingRefresher{}
	f := NewMovieFreshener(5*time.Second, fixedClock(now), discardLog())
	f.Set(r)

	res := f.EnsureFresh(context.Background(), freshCanon(now), "ru-RU")

	assert.True(t, res.Fresh, "fully-fresh movie needs no refresh")
	assert.False(t, res.Refreshed)
	assert.Equal(t, int64(0), r.calls.Load(), "HandleForced NOT called on a fresh movie")
}

func TestMovieFreshener_NoWorkerSet_Degraded(t *testing.T) {
	t.Parallel()
	f := NewMovieFreshener(5*time.Second, time.Now, discardLog())
	// Set intentionally NOT called — boot race.
	res := f.EnsureFresh(context.Background(), staleCanon(), "ru-RU")
	assert.True(t, res.Degraded, "unbound worker → degraded so the async fallback nudges")
	assert.False(t, res.Refreshed)
}

func TestMovieFreshener_TMDBLess_IsFreshNoop(t *testing.T) {
	t.Parallel()
	r := &blockingRefresher{}
	f := NewMovieFreshener(5*time.Second, time.Now, discardLog())
	f.Set(r)
	canon := movie.Canon{ID: domain.MovieID(11), TMDBID: nil, Hydration: movie.HydrationStub}

	res := f.EnsureFresh(context.Background(), canon, "ru-RU")

	assert.True(t, res.Fresh, "a tmdb-less movie has nothing to hydrate")
	assert.Equal(t, int64(0), r.calls.Load())
}

func TestMovieFreshener_Timeout_Degraded(t *testing.T) {
	t.Parallel()
	release := make(chan struct{})
	t.Cleanup(func() { close(release) }) // unblock the leaked leader goroutine
	r := &blockingRefresher{release: release, entered: make(chan struct{}, 1)}
	// Tiny budget so the blocked HandleForced trips the deadline fast.
	f := NewMovieFreshener(50*time.Millisecond, time.Now, discardLog())
	f.Set(r)

	start := time.Now()
	res := f.EnsureFresh(context.Background(), staleCanon(), "ru-RU")
	elapsed := time.Since(start)

	assert.True(t, res.Degraded, "HandleForced past SyncTimeout → degraded")
	assert.False(t, res.Refreshed)
	assert.Less(t, elapsed, 2*time.Second, "returns promptly at the budget, not blocking on the leader")
	assert.Equal(t, int64(1), r.calls.Load(), "the leader did start HandleForced")
}

func TestMovieFreshener_Singleflight_DedupsConcurrent(t *testing.T) {
	t.Parallel()
	release := make(chan struct{})
	r := &blockingRefresher{release: release, entered: make(chan struct{}, 1)}
	// Generous budget so neither caller times out before we release.
	f := NewMovieFreshener(5*time.Second, time.Now, discardLog())
	f.Set(r)
	canon := staleCanon()

	results := make([]FreshenResult, 2)
	var wg sync.WaitGroup

	// Caller 1 becomes the singleflight leader and blocks inside HandleForced.
	wg.Go(func() {
		results[0] = f.EnsureFresh(context.Background(), canon, "ru-RU")
	})

	// Wait until the leader is inside HandleForced, THEN launch caller 2 so it
	// coalesces onto the in-flight leader instead of starting a fresh call.
	select {
	case <-r.entered:
	case <-time.After(2 * time.Second):
		t.Fatal("leader never entered HandleForced")
	}

	wg.Go(func() {
		results[1] = f.EnsureFresh(context.Background(), canon, "ru-RU")
	})

	// Give caller 2 a beat to join the flight, then release the shared leader.
	time.Sleep(50 * time.Millisecond)
	close(release)
	wg.Wait()

	assert.Equal(t, int64(1), r.calls.Load(), "two concurrent opens coalesce to ONE HandleForced")
	assert.True(t, results[0].Refreshed)
	assert.True(t, results[1].Refreshed, "the coalesced follower shares the leader's success")
}

func TestMovieFreshener_HandleForcedError_Degraded(t *testing.T) {
	t.Parallel()
	r := &blockingRefresher{err: errors.New("tmdb 500")}
	f := NewMovieFreshener(5*time.Second, time.Now, discardLog())
	f.Set(r)

	res := f.EnsureFresh(context.Background(), staleCanon(), "ru-RU")

	assert.True(t, res.Degraded, "a HandleForced error degrades (async fallback covers it)")
	assert.False(t, res.Refreshed)
	assert.Equal(t, int64(1), r.calls.Load())
}

// stubCoverage is a scripted MovieI18nCoverageReader. gap is returned as-is; err
// (when set) exercises the fail-closed branch. recorded captures the recheckBefore
// passed so a test can assert the 7d window.
type stubCoverage struct {
	gap      bool
	err      error
	calls    atomic.Int64
	recorded time.Time
}

func (s *stubCoverage) HasLocalizedTextGap(_ context.Context, _ domain.MovieID, _ string, recheckBefore time.Time) (bool, error) {
	s.calls.Add(1)
	s.recorded = recheckBefore
	return s.gap, s.err
}

// U-1b: all 5 section stamps fresh, but the ru row has an empty title → the coverage
// reader reports a gap → EnsureFresh escalates to a synchronous HandleForced.
func TestMovieFreshener_TitleGap_TriggersHandleForced(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 19, 12, 0, 0, 0, time.UTC)
	r := &blockingRefresher{}
	cov := &stubCoverage{gap: true}
	f := NewMovieFreshener(5*time.Second, fixedClock(now), discardLog()).WithI18nCoverage(cov)
	f.Set(r)

	res := f.EnsureFresh(context.Background(), freshCanon(now), "ru-RU")

	assert.True(t, res.Refreshed, "section-fresh movie with a title gap is healed on view")
	assert.Equal(t, int64(1), r.calls.Load(), "HandleForced fired for the title gap")
	assert.Equal(t, int64(1), cov.calls.Load(), "coverage reader consulted once")
	assert.Equal(t, now.Add(-7*24*time.Hour), cov.recorded, "recheck window is 7d")
}

// U-1b: section-fresh + ru title present (no gap) → no HandleForced.
func TestMovieFreshener_NoTitleGap_NoOp(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 19, 12, 0, 0, 0, time.UTC)
	r := &blockingRefresher{}
	cov := &stubCoverage{gap: false}
	f := NewMovieFreshener(5*time.Second, fixedClock(now), discardLog()).WithI18nCoverage(cov)
	f.Set(r)

	res := f.EnsureFresh(context.Background(), freshCanon(now), "ru-RU")

	assert.True(t, res.Fresh, "no gap → Fresh")
	assert.Equal(t, int64(0), r.calls.Load(), "HandleForced NOT called when the row is healthy")
}

// U-1b: base language (en-US) is never gap-checked (it is the source of the ladder).
func TestMovieFreshener_BaseLang_SkipsCoverage(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 19, 12, 0, 0, 0, time.UTC)
	r := &blockingRefresher{}
	cov := &stubCoverage{gap: true} // would trigger if consulted
	f := NewMovieFreshener(5*time.Second, fixedClock(now), discardLog()).WithI18nCoverage(cov)
	f.Set(r)

	res := f.EnsureFresh(context.Background(), freshCanon(now), "en-US")

	assert.True(t, res.Fresh, "base lang skips the coverage read")
	assert.Equal(t, int64(0), cov.calls.Load(), "coverage reader NOT consulted for base lang")
	assert.Equal(t, int64(0), r.calls.Load())
}

// U-1b: a coverage read error fails CLOSED (treated as Fresh) so a flaky read never
// fires a 5s TMDB hydrate on the request path.
func TestMovieFreshener_CoverageError_FailsClosed(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 19, 12, 0, 0, 0, time.UTC)
	r := &blockingRefresher{}
	cov := &stubCoverage{gap: true, err: errors.New("db down")}
	f := NewMovieFreshener(5*time.Second, fixedClock(now), discardLog()).WithI18nCoverage(cov)
	f.Set(r)

	res := f.EnsureFresh(context.Background(), freshCanon(now), "ru-RU")

	assert.True(t, res.Fresh, "read error → Fresh (fail closed)")
	assert.Equal(t, int64(0), r.calls.Load(), "no HandleForced on a coverage read error")
}
