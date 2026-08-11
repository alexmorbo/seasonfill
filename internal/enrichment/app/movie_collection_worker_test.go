package enrichment

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/internal/catalog/domain/movie"
	"github.com/alexmorbo/seasonfill/internal/shared/clients/tmdb"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
)

type fakeCollectionTMDB struct {
	resp    *tmdb.CollectionResponse
	err     error
	gotID   int64
	gotLang string
	calls   int
}

func (f *fakeCollectionTMDB) GetCollection(_ context.Context, id int64, lang string) (*tmdb.CollectionResponse, error) {
	f.calls++
	f.gotID = id
	f.gotLang = lang
	return f.resp, f.err
}

type fakeCollectionUpserter struct {
	calls    int
	upserted movie.CollectionCanon
	err      error
}

func (f *fakeCollectionUpserter) UpsertCollection(_ context.Context, c movie.CollectionCanon) error {
	f.calls++
	f.upserted = c
	return f.err
}

// recordingMovieCanon captures every part Upsert; failTMDB triggers a per-part
// error for that tmdb id (to prove partial-failure tolerance).
type recordingMovieCanon struct {
	fakeMovieCanon
	upserts  []movie.Canon
	failTMDB int
}

func (r *recordingMovieCanon) Upsert(_ context.Context, c movie.Canon) (domain.MovieID, error) {
	r.upserts = append(r.upserts, c)
	if c.TMDBID != nil && int(*c.TMDBID) == r.failTMDB {
		return 0, errors.New("boom")
	}
	return c.ID, nil
}

func collectionRespFixture() *tmdb.CollectionResponse {
	return &tmdb.CollectionResponse{
		ID: 726871, Name: "Dune Collection", Overview: "Epic saga.",
		PosterPath: "/coll_p.jpg",
		Parts: []tmdb.CollectionPart{
			{ID: 438631, Title: "Dune", ReleaseDate: "2021-10-22", VoteAverage: 7.8},
			{ID: 693134, Title: "Dune: Part Two", ReleaseDate: "2024-02-27", VoteAverage: 8.2},
		},
	}
}

func TestMovieCollectionWorker_PopulateCollection_HappyPath(t *testing.T) {
	ftmdb := &fakeCollectionTMDB{resp: collectionRespFixture()}
	fups := &fakeCollectionUpserter{}
	movies := &recordingMovieCanon{}

	w, err := NewMovieCollectionWorker(MovieCollectionWorkerDeps{
		TMDB: ftmdb, Collections: fups, Movies: movies,
	})
	require.NoError(t, err)

	require.NoError(t, w.PopulateCollection(context.Background(), 726871))

	assert.Equal(t, int64(726871), ftmdb.gotID)
	assert.Equal(t, tmdb.DefaultLanguage, ftmdb.gotLang)
	require.Equal(t, 1, fups.calls)
	assert.Equal(t, 726871, fups.upserted.TMDBCollectionID)

	require.Len(t, movies.upserts, 2)
	for _, u := range movies.upserts {
		require.NotNil(t, u.CollectionID)
		assert.Equal(t, 726871, *u.CollectionID, "each part links to the collection")
		assert.Equal(t, movie.HydrationStub, u.Hydration)
	}
}

func TestMovieCollectionWorker_PopulateCollection_PartialPartFailureCollected(t *testing.T) {
	ftmdb := &fakeCollectionTMDB{resp: collectionRespFixture()}
	movies := &recordingMovieCanon{failTMDB: 438631}

	w, err := NewMovieCollectionWorker(MovieCollectionWorkerDeps{
		TMDB: ftmdb, Collections: &fakeCollectionUpserter{}, Movies: movies,
	})
	require.NoError(t, err)

	err = w.PopulateCollection(context.Background(), 726871)
	require.Error(t, err, "the failing part surfaces as a joined error")
	assert.Len(t, movies.upserts, 2, "the batch is NOT aborted on one part failure")
}

func TestMovieCollectionWorker_PopulateCollection_FetchErr(t *testing.T) {
	ftmdb := &fakeCollectionTMDB{err: errors.New("upstream 500")}
	fups := &fakeCollectionUpserter{}
	w, err := NewMovieCollectionWorker(MovieCollectionWorkerDeps{
		TMDB: ftmdb, Collections: fups, Movies: &recordingMovieCanon{},
	})
	require.NoError(t, err)

	require.Error(t, w.PopulateCollection(context.Background(), 726871))
	assert.Equal(t, 0, fups.calls, "no collection upsert when the fetch failed")
}

func TestMovieCollectionWorker_PopulateCollection_ZeroIDNoop(t *testing.T) {
	ftmdb := &fakeCollectionTMDB{}
	w, err := NewMovieCollectionWorker(MovieCollectionWorkerDeps{
		TMDB: ftmdb, Collections: &fakeCollectionUpserter{}, Movies: &recordingMovieCanon{},
	})
	require.NoError(t, err)

	require.NoError(t, w.PopulateCollection(context.Background(), 0))
	assert.Equal(t, 0, ftmdb.calls, "no fetch for a zero collection id")
}

func TestNewMovieCollectionWorker_RequiresPorts(t *testing.T) {
	_, err := NewMovieCollectionWorker(MovieCollectionWorkerDeps{Collections: &fakeCollectionUpserter{}, Movies: &recordingMovieCanon{}})
	require.Error(t, err)
	_, err = NewMovieCollectionWorker(MovieCollectionWorkerDeps{TMDB: &fakeCollectionTMDB{}, Movies: &recordingMovieCanon{}})
	require.Error(t, err)
	_, err = NewMovieCollectionWorker(MovieCollectionWorkerDeps{TMDB: &fakeCollectionTMDB{}, Collections: &fakeCollectionUpserter{}})
	require.Error(t, err)
}
