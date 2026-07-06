package seriesdetail_test

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/internal/catalog/domain/series"
	seriesdetail "github.com/alexmorbo/seasonfill/internal/seriesdetail/app"
	ports "github.com/alexmorbo/seasonfill/internal/shared/dataports"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
	"github.com/alexmorbo/seasonfill/internal/shared/http/dto"
)

// --- fakes -----------------------------------------------------------------

// fakeSeriesPort returns a canon that can MUTATE between reloads (to simulate an
// owner-write landing during a blocking fetch).
type fakeSeriesPort struct {
	mu    sync.Mutex
	canon series.Canon
	err   error
	getN  atomic.Int32
}

func (f *fakeSeriesPort) Get(_ context.Context, _ domain.SeriesID) (series.Canon, error) {
	f.getN.Add(1)
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.canon, f.err
}
func (f *fakeSeriesPort) set(c series.Canon) { f.mu.Lock(); f.canon = c; f.mu.Unlock() }

// The unused SeriesPort methods keep the fake satisfying the interface.
func (f *fakeSeriesPort) GetByTMDBID(_ context.Context, _ domain.TMDBID) (series.Canon, error) {
	return series.Canon{}, ports.ErrNotFound
}
func (f *fakeSeriesPort) ListByIDs(_ context.Context, _ []domain.SeriesID) ([]series.Canon, error) {
	return nil, nil
}
func (f *fakeSeriesPort) ListByTMDBIDs(_ context.Context, _ []domain.TMDBID) ([]series.Canon, error) {
	return nil, nil
}

// fakeRefresher records calls and can mutate the series port on Refresh/Handle
// (simulating the owner-write) + optionally block until released.
type fakeRefresher struct {
	calls  atomic.Int32
	onCall func() // e.g. port.set(withValue)
	block  chan struct{}
	err    error
}

func (f *fakeRefresher) run(_ context.Context) error {
	f.calls.Add(1)
	if f.block != nil {
		<-f.block
	}
	if f.onCall != nil {
		f.onCall()
	}
	return f.err
}
func (f *fakeRefresher) Refresh(ctx context.Context, _ domain.SeriesID) error   { return f.run(ctx) }
func (f *fakeRefresher) HandleHot(ctx context.Context, _ domain.SeriesID) error { return f.run(ctx) }
func (f *fakeRefresher) count() int32                                           { return f.calls.Load() }

func tmdbID(v int) *domain.TMDBID    { id := domain.TMDBID(v); return &id }
func imdbID(v string) *domain.IMDBID { id := domain.IMDBID(v); return &id }

func newUC(t *testing.T, port *fakeSeriesPort, tmdb, omdb *fakeRefresher, now time.Time) *seriesdetail.SeriesRatingsUseCase {
	t.Helper()
	deps := seriesdetail.SeriesRatingsDeps{
		Series: port,
		Now:    func() time.Time { return now },
		// Test-seam: shrink the viewer deadline (prod 3s) so the blocking /
		// single-flight timeout cases run fast. Background timeout likewise.
		FetchDeadline:     100 * time.Millisecond,
		BackgroundTimeout: time.Second,
	}
	if tmdb != nil {
		deps.TMDB = tmdb
	}
	if omdb != nil {
		deps.OMDb = omdb
	}
	uc, err := seriesdetail.NewSeriesRatingsUseCase(deps)
	require.NoError(t, err)
	return uc
}

// --- cases -----------------------------------------------------------------

func TestGetRatings_UnknownCanon_ReturnsNotFound(t *testing.T) {
	port := &fakeSeriesPort{err: ports.ErrNotFound}
	uc := newUC(t, port, &fakeRefresher{}, &fakeRefresher{}, time.Now())
	_, err := uc.GetRatings(context.Background(), 1)
	assert.ErrorIs(t, err, ports.ErrNotFound)
}

func TestGetRatings_Fresh_NoFetch(t *testing.T) {
	now := time.Now().UTC()
	synced := now.Add(-1 * time.Hour) // well within any TTL
	port := &fakeSeriesPort{canon: series.Canon{
		ID: 1, TMDBID: tmdbID(100), IMDBID: imdbID("tt1"),
		TMDBRating: new(8.4), TMDBVotes: new(1200), IMDBRating: new(8.7),
		TMDBRatingSyncedAt:     &synced,
		EnrichmentTMDBSyncedAt: &synced, EnrichmentOMDBSyncedAt: &synced,
	}}
	tmdb, omdb := &fakeRefresher{}, &fakeRefresher{}
	uc := newUC(t, port, tmdb, omdb, now)

	resp, err := uc.GetRatings(context.Background(), 1)
	require.NoError(t, err)
	assert.Equal(t, dto.RatingStatusFresh, resp.Sources.TMDB)
	assert.Equal(t, dto.RatingStatusFresh, resp.Sources.OMDb)
	assert.Equal(t, 8.4, *resp.TMDBRating)
	assert.EqualValues(t, 0, tmdb.count())
	assert.EqualValues(t, 0, omdb.count())
}

func TestGetRatings_Stale_ReturnsOldValue_KicksBackground(t *testing.T) {
	now := time.Now().UTC()
	old := now.AddDate(-5, 0, 0) // finished long ago → past TTL
	firstAir := now.AddDate(-10, 0, 0)
	port := &fakeSeriesPort{canon: series.Canon{
		ID: 1, TMDBID: tmdbID(100), IMDBID: imdbID("tt1"),
		TMDBRating: new(7.0), IMDBRating: new(7.5),
		FirstAirDate: &firstAir, LastAirDate: &firstAir,
		EnrichmentTMDBSyncedAt: &old, EnrichmentOMDBSyncedAt: &old,
	}}
	tmdb, omdb := &fakeRefresher{}, &fakeRefresher{}
	uc := newUC(t, port, tmdb, omdb, now)

	resp, err := uc.GetRatings(context.Background(), 1)
	require.NoError(t, err)
	// OLD value returned immediately, status revalidating.
	assert.Equal(t, dto.RatingStatusRevalidating, resp.Sources.TMDB)
	assert.Equal(t, dto.RatingStatusRevalidating, resp.Sources.OMDb)
	assert.Equal(t, 7.0, *resp.TMDBRating)
	// Background single-flight fired (async) — poll briefly.
	assert.Eventually(t, func() bool { return tmdb.count() == 1 && omdb.count() == 1 },
		time.Second, 5*time.Millisecond)
}

func TestGetRatings_EmptyWithID_BlockingSuccess_Fresh(t *testing.T) {
	now := time.Now().UTC()
	port := &fakeSeriesPort{canon: series.Canon{
		ID: 1, TMDBID: tmdbID(100), // never synced → stale → blocking
	}}
	tmdb := &fakeRefresher{onCall: func() {
		// simulate owner-write landing.
		port.set(series.Canon{ID: 1, TMDBID: tmdbID(100), TMDBRating: new(9.1),
			EnrichmentTMDBSyncedAt: &now})
	}}
	uc := newUC(t, port, tmdb, nil /* OMDb disabled */, now)

	resp, err := uc.GetRatings(context.Background(), 1)
	require.NoError(t, err)
	assert.Equal(t, dto.RatingStatusFresh, resp.Sources.TMDB)
	require.NotNil(t, resp.TMDBRating)
	assert.Equal(t, 9.1, *resp.TMDBRating)
	assert.EqualValues(t, 1, tmdb.count())
	// OMDb disabled (nil refresher) + no value → unavailable.
	assert.Equal(t, dto.RatingStatusUnavailable, resp.Sources.OMDb)
}

func TestGetRatings_EmptyWithID_BlockingTimeout_Pending_BackgroundContinues(t *testing.T) {
	now := time.Now().UTC()
	port := &fakeSeriesPort{canon: series.Canon{ID: 1, TMDBID: tmdbID(100)}}
	release := make(chan struct{})
	tmdb := &fakeRefresher{block: release, onCall: func() {
		port.set(series.Canon{ID: 1, TMDBID: tmdbID(100), TMDBRating: new(9.1), EnrichmentTMDBSyncedAt: &now})
	}}
	uc := newUC(t, port, tmdb, nil, now)

	// newUC shrinks FetchDeadline to 100ms; the refresher blocks past it so the
	// blocking fetch times out → pending while the flight keeps running detached.
	resp, err := uc.GetRatings(context.Background(), 1)
	require.NoError(t, err)
	assert.Equal(t, dto.RatingStatusPending, resp.Sources.TMDB)
	// Flight still running; release it → background completes the owner-write.
	close(release)
	assert.Eventually(t, func() bool {
		c, _ := port.Get(context.Background(), 1)
		return c.TMDBRating != nil
	}, time.Second, 5*time.Millisecond)
	assert.EqualValues(t, 1, tmdb.count())
}

func TestGetRatings_EmptyNoID_Unavailable_NoBackground(t *testing.T) {
	now := time.Now().UTC()
	port := &fakeSeriesPort{canon: series.Canon{ID: 1 /* no TMDBID, no IMDBID */}}
	tmdb, omdb := &fakeRefresher{}, &fakeRefresher{}
	uc := newUC(t, port, tmdb, omdb, now)

	resp, err := uc.GetRatings(context.Background(), 1)
	require.NoError(t, err)
	assert.Equal(t, dto.RatingStatusUnavailable, resp.Sources.TMDB)
	assert.Equal(t, dto.RatingStatusUnavailable, resp.Sources.OMDb)
	// give any (erroneous) goroutine a chance — must stay zero.
	time.Sleep(20 * time.Millisecond)
	assert.EqualValues(t, 0, tmdb.count())
	assert.EqualValues(t, 0, omdb.count())
}

func TestGetRatings_RepeatedWhilePending_SingleFlight_NoDuplicate(t *testing.T) {
	now := time.Now().UTC()
	port := &fakeSeriesPort{canon: series.Canon{ID: 1, TMDBID: tmdbID(100)}}
	release := make(chan struct{})
	tmdb := &fakeRefresher{block: release}
	uc := newUC(t, port, tmdb, nil, now)

	// Fire two concurrent requests while the first flight is blocked.
	var wg sync.WaitGroup
	for range 2 {
		wg.Go(func() { _, _ = uc.GetRatings(context.Background(), 1) })
	}
	// Both should time out to pending sharing ONE flight.
	wg.Wait()
	close(release)
	assert.Eventually(t, func() bool { return tmdb.count() >= 1 }, time.Second, 5*time.Millisecond)
	// The KEY assertion: single-flight collapsed the concurrent opens into ONE call.
	assert.EqualValues(t, 1, tmdb.count())
}

func TestGetRatings_AlwaysReturns200Shaped_NoError(t *testing.T) {
	// A fetch error must NOT bubble as a usecase error (handler-visible 5xx).
	now := time.Now().UTC()
	port := &fakeSeriesPort{canon: series.Canon{ID: 1, TMDBID: tmdbID(100)}}
	tmdb := &fakeRefresher{err: assertAnErr}
	uc := newUC(t, port, tmdb, nil, now)
	resp, err := uc.GetRatings(context.Background(), 1)
	require.NoError(t, err) // never errors on fetch failure
	assert.Equal(t, dto.RatingStatusPending, resp.Sources.TMDB)
}

var assertAnErr = &staticErr{"boom"}

type staticErr struct{ s string }

func (e *staticErr) Error() string { return e.s }
