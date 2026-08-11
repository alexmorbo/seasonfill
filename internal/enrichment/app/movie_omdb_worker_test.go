package enrichment

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/internal/catalog/domain/movie"
	"github.com/alexmorbo/seasonfill/internal/shared/clients/omdb"
	"github.com/alexmorbo/seasonfill/internal/shared/clients/tmdb"
	ports "github.com/alexmorbo/seasonfill/internal/shared/dataports"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
)

// fakeMovieOMDbRepo records the OMDb-owned write + serves a canon row.
type fakeMovieOMDbRepo struct {
	canon      movie.Canon
	getErr     error
	writeCalls int
	wroteID    domain.MovieID
	wrote      omdb.Enrichment
	wroteNow   time.Time
	writeErr   error
}

func (f *fakeMovieOMDbRepo) Get(_ context.Context, _ domain.MovieID) (movie.Canon, error) {
	return f.canon, f.getErr
}

func (f *fakeMovieOMDbRepo) UpdateMovieOMDbColumns(_ context.Context, id domain.MovieID, e omdb.Enrichment, now time.Time) error {
	f.writeCalls++
	f.wroteID = id
	f.wrote = e
	f.wroteNow = now
	return f.writeErr
}

func imdbCanon(id domain.MovieID, imdb string) movie.Canon {
	v := domain.IMDBID(imdb)
	return movie.Canon{ID: id, IMDBID: &v, Title: "m"}
}

// TestMovieOMDbWorker_HappyPath asserts a movie with an imdb_id fetches OMDb,
// maps, and writes the four columns via UpdateMovieOMDbColumns with the clock's now.
func TestMovieOMDbWorker_HappyPath(t *testing.T) {
	client := &fakeOMDbClient{resp: &omdb.Response{
		IMDBRating:   "8.4",
		IMDBVotes:    "2,034,123",
		Rated:        "PG-13",
		Awards:       "Won 2 Oscars",
		ResponseFlag: "True",
	}}
	repo := &fakeMovieOMDbRepo{canon: imdbCanon(7, "tt15239678")}
	fixed := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)

	w, err := NewMovieOMDbWorker(MovieOMDbWorkerDeps{
		Client: func() OMDbClient { return client },
		Movies: repo,
		Clock:  func() time.Time { return fixed },
	})
	require.NoError(t, err)

	require.NoError(t, w.HandleMovieOMDb(context.Background(), 7))
	assert.Equal(t, 1, client.calls)
	require.Equal(t, 1, repo.writeCalls)
	assert.Equal(t, domain.MovieID(7), repo.wroteID)
	assert.Equal(t, fixed, repo.wroteNow)
	require.NotNil(t, repo.wrote.IMDBRating)
	assert.InDelta(t, 8.4, *repo.wrote.IMDBRating, 1e-9)
	require.NotNil(t, repo.wrote.IMDBVotes)
	assert.Equal(t, int64(2034123), *repo.wrote.IMDBVotes)
}

// TestMovieOMDbWorker_AllNA_StillWrites asserts an all-N/A upstream response
// still writes (all-nil Enrichment) so a stale rating is cleared.
func TestMovieOMDbWorker_AllNA_StillWrites(t *testing.T) {
	client := &fakeOMDbClient{resp: &omdb.Response{
		IMDBRating: "N/A", IMDBVotes: "N/A", Rated: "N/A", Awards: "N/A", ResponseFlag: "True",
	}}
	repo := &fakeMovieOMDbRepo{canon: imdbCanon(7, "tt1")}

	w, err := NewMovieOMDbWorker(MovieOMDbWorkerDeps{
		Client: func() OMDbClient { return client }, Movies: repo,
	})
	require.NoError(t, err)

	require.NoError(t, w.HandleMovieOMDb(context.Background(), 7))
	require.Equal(t, 1, repo.writeCalls)
	assert.Nil(t, repo.wrote.IMDBRating)
	assert.Nil(t, repo.wrote.OMDbRated)
}

// TestMovieOMDbWorker_NoIMDB_Skips asserts an imdb-less movie is a clean skip:
// no fetch, no write, no error.
func TestMovieOMDbWorker_NoIMDB_Skips(t *testing.T) {
	client := &fakeOMDbClient{}
	repo := &fakeMovieOMDbRepo{canon: movie.Canon{ID: 9, Title: "orphan"}}

	w, err := NewMovieOMDbWorker(MovieOMDbWorkerDeps{
		Client: func() OMDbClient { return client }, Movies: repo,
	})
	require.NoError(t, err)

	require.NoError(t, w.HandleMovieOMDb(context.Background(), 9))
	assert.Equal(t, 0, client.calls)
	assert.Equal(t, 0, repo.writeCalls)
}

// TestMovieOMDbWorker_NotFound_NoWrite asserts an upstream not_found logs + skips
// (no write, nil error — a lookup miss must not clear a prior rating).
func TestMovieOMDbWorker_NotFound_NoWrite(t *testing.T) {
	client := &fakeOMDbClient{err: omdb.ErrNotFound}
	repo := &fakeMovieOMDbRepo{canon: imdbCanon(7, "tt404")}

	w, err := NewMovieOMDbWorker(MovieOMDbWorkerDeps{
		Client: func() OMDbClient { return client }, Movies: repo,
	})
	require.NoError(t, err)

	require.NoError(t, w.HandleMovieOMDb(context.Background(), 7))
	assert.Equal(t, 1, client.calls)
	assert.Equal(t, 0, repo.writeCalls, "not_found must not write")
}

// TestMovieOMDbWorker_TransientError_NoWrite asserts a generic/auth error logs +
// returns nil WITHOUT writing (row stays unstamped so the next tick retries).
func TestMovieOMDbWorker_TransientError_NoWrite(t *testing.T) {
	for _, upErr := range []error{omdb.ErrInvalidKey, omdb.ErrDailyLimit, errors.New("boom")} {
		client := &fakeOMDbClient{err: upErr}
		repo := &fakeMovieOMDbRepo{canon: imdbCanon(7, "tt7")}
		w, err := NewMovieOMDbWorker(MovieOMDbWorkerDeps{
			Client: func() OMDbClient { return client }, Movies: repo,
		})
		require.NoError(t, err)
		require.NoError(t, w.HandleMovieOMDb(context.Background(), 7))
		assert.Equal(t, 0, repo.writeCalls)
	}
}

// TestMovieOMDbWorker_WriteError_Bubbles asserts a write failure propagates (so
// the caller's best-effort hook can log it).
func TestMovieOMDbWorker_WriteError_Bubbles(t *testing.T) {
	client := &fakeOMDbClient{resp: &omdb.Response{IMDBRating: "8.4", ResponseFlag: "True"}}
	repo := &fakeMovieOMDbRepo{canon: imdbCanon(7, "tt7"), writeErr: errors.New("db down")}

	w, err := NewMovieOMDbWorker(MovieOMDbWorkerDeps{
		Client: func() OMDbClient { return client }, Movies: repo,
	})
	require.NoError(t, err)
	require.Error(t, w.HandleMovieOMDb(context.Background(), 7))
}

// TestMovieOMDbWorker_MovieMissing asserts a ports.ErrNotFound on load is a clean
// skip (no write, nil error).
func TestMovieOMDbWorker_MovieMissing(t *testing.T) {
	client := &fakeOMDbClient{}
	repo := &fakeMovieOMDbRepo{getErr: ports.ErrNotFound}
	w, err := NewMovieOMDbWorker(MovieOMDbWorkerDeps{
		Client: func() OMDbClient { return client }, Movies: repo,
	})
	require.NoError(t, err)
	require.NoError(t, w.HandleMovieOMDb(context.Background(), 7))
	assert.Equal(t, 0, client.calls)
	assert.Equal(t, 0, repo.writeCalls)
}

// TestMovieOMDbWorker_ClientNil_Skips asserts a nil client getter result (OMDb
// disabled / reload race) skips without error or write.
func TestMovieOMDbWorker_ClientNil_Skips(t *testing.T) {
	repo := &fakeMovieOMDbRepo{canon: imdbCanon(7, "tt7")}
	w, err := NewMovieOMDbWorker(MovieOMDbWorkerDeps{
		Client: func() OMDbClient { return nil }, Movies: repo,
	})
	require.NoError(t, err)
	require.NoError(t, w.HandleMovieOMDb(context.Background(), 7))
	assert.Equal(t, 0, repo.writeCalls)
}

// TestNewMovieOMDbWorker_RequiresDeps asserts the constructor rejects missing deps.
func TestNewMovieOMDbWorker_RequiresDeps(t *testing.T) {
	_, err := NewMovieOMDbWorker(MovieOMDbWorkerDeps{Movies: &fakeMovieOMDbRepo{}})
	require.Error(t, err)
	_, err = NewMovieOMDbWorker(MovieOMDbWorkerDeps{Client: func() OMDbClient { return nil }})
	require.Error(t, err)
}

// --- MovieWorker post-hydrate OMDb hook (enqueue-after-imdb) ---

// fakeMovieOMDbHook records HandleMovieOMDb invocations from the TMDB worker hook.
type fakeMovieOMDbHook struct {
	calls  int
	gotID  int64
	retErr error
}

func (f *fakeMovieOMDbHook) HandleMovieOMDb(_ context.Context, movieID int64) error {
	f.calls++
	f.gotID = movieID
	return f.retErr
}

// TestMovieWorker_FiresOMDbHookWhenIMDBPresent asserts the TMDB worker fires the
// OMDb follow-up after MarkTMDBSynced when the hydrated movie has an imdb_id.
func TestMovieWorker_FiresOMDbHookWhenIMDBPresent(t *testing.T) {
	tmdbClient := &fakeMovieTMDB{resp: tmdbMovieRespWithIMDB()}
	canonRepo := &fakeMovieCanon{getResp: movieCanonWithTMDB(7, 693134)}
	hook := &fakeMovieOMDbHook{}

	w, err := NewMovieWorker(MovieWorkerDeps{
		TMDB: tmdbClient, Movies: canonRepo, OMDb: hook,
		Clock: func() time.Time { return time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC) },
	})
	require.NoError(t, err)

	require.NoError(t, w.HandleForced(context.Background(), 7))
	require.Equal(t, 1, hook.calls, "OMDb follow-up fires after hydrate")
	assert.Equal(t, int64(7), hook.gotID)
}

// TestMovieWorker_OMDbHookErrorDoesNotFailHydrate asserts an OMDb hook error is
// swallowed (best-effort) and does not fail the committed TMDB hydrate.
func TestMovieWorker_OMDbHookErrorDoesNotFailHydrate(t *testing.T) {
	tmdbClient := &fakeMovieTMDB{resp: tmdbMovieRespWithIMDB()}
	canonRepo := &fakeMovieCanon{getResp: movieCanonWithTMDB(7, 693134)}
	hook := &fakeMovieOMDbHook{retErr: errors.New("omdb boom")}

	w, err := NewMovieWorker(MovieWorkerDeps{TMDB: tmdbClient, Movies: canonRepo, OMDb: hook})
	require.NoError(t, err)

	require.NoError(t, w.HandleForced(context.Background(), 7), "hook failure must not fail hydrate")
	require.Equal(t, 1, canonRepo.markCalls, "hydrate still stamped despite hook error")
	assert.Equal(t, 1, hook.calls)
}

// TestMovieWorker_SkipsOMDbHookWhenNoIMDB asserts the hook does NOT fire when
// neither the TMDB response nor the canon carries an imdb_id.
func TestMovieWorker_SkipsOMDbHookWhenNoIMDB(t *testing.T) {
	tmdbClient := &fakeMovieTMDB{resp: tmdbMovieRespNoIMDB()}
	canonRepo := &fakeMovieCanon{getResp: movieCanonWithTMDB(7, 693134)}
	hook := &fakeMovieOMDbHook{}

	w, err := NewMovieWorker(MovieWorkerDeps{TMDB: tmdbClient, Movies: canonRepo, OMDb: hook})
	require.NoError(t, err)

	require.NoError(t, w.HandleForced(context.Background(), 7))
	assert.Equal(t, 0, hook.calls, "no imdb_id ⇒ no OMDb follow-up")
}

func tmdbMovieRespWithIMDB() *tmdb.MovieResponse {
	return &tmdb.MovieResponse{ID: 693134, IMDBID: "tt15239678", Title: "Dune", ReleaseDate: "2024-02-27"}
}

func tmdbMovieRespNoIMDB() *tmdb.MovieResponse {
	return &tmdb.MovieResponse{ID: 693134, Title: "Dune", ReleaseDate: "2024-02-27"}
}
