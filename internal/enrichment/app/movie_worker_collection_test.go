package enrichment

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/internal/shared/clients/tmdb"
)

// fakeCollectionPopulator records the collection id and can force a failure to
// prove the worker's fail-soft policy.
type fakeCollectionPopulator struct {
	calls int
	gotID int
	err   error
}

func (f *fakeCollectionPopulator) PopulateCollection(_ context.Context, id int) error {
	f.calls++
	f.gotID = id
	return f.err
}

func movieRespWithCollection(tmdbID int, collectionID int) *tmdb.MovieResponse {
	return &tmdb.MovieResponse{
		ID:                  int64(tmdbID),
		Title:               "Dune",
		Status:              "Released",
		ReleaseDate:         "2021-10-22",
		BelongsToCollection: &tmdb.MovieCollectionRef{ID: collectionID, Name: "Dune Collection"},
	}
}

// TestMovieWorker_HandleForced_FiresCollectionPopulate asserts the branch fires
// with the mapped collection id when the hydrated movie belongs to a collection.
func TestMovieWorker_HandleForced_FiresCollectionPopulate(t *testing.T) {
	tmdbClient := &fakeMovieTMDB{resp: movieRespWithCollection(438631, 726871)}
	canonRepo := &fakeMovieCanon{getResp: movieCanonWithTMDB(7, 438631)}
	pop := &fakeCollectionPopulator{}

	w, err := NewMovieWorker(MovieWorkerDeps{
		TMDB: tmdbClient, Movies: canonRepo, Collections: pop,
	})
	require.NoError(t, err)

	require.NoError(t, w.HandleForced(context.Background(), 7))
	assert.Equal(t, 1, pop.calls)
	assert.Equal(t, 726871, pop.gotID)
	require.Equal(t, 1, canonRepo.markCalls, "movie hydrate committed")
}

// TestMovieWorker_HandleForced_CollectionPopulateFailSoft asserts a populator
// error is swallowed: the movie hydrate still succeeds (nil return, stamp written).
func TestMovieWorker_HandleForced_CollectionPopulateFailSoft(t *testing.T) {
	tmdbClient := &fakeMovieTMDB{resp: movieRespWithCollection(438631, 726871)}
	canonRepo := &fakeMovieCanon{getResp: movieCanonWithTMDB(7, 438631)}
	pop := &fakeCollectionPopulator{err: errors.New("collection upstream 500")}

	w, err := NewMovieWorker(MovieWorkerDeps{
		TMDB: tmdbClient, Movies: canonRepo, Collections: pop,
	})
	require.NoError(t, err)

	require.NoError(t, w.HandleForced(context.Background(), 7),
		"collection populate failure must not fail the movie hydrate")
	assert.Equal(t, 1, pop.calls)
	assert.Equal(t, 1, canonRepo.markCalls, "stamp still written")
}

// TestMovieWorker_HandleForced_NoCollectionSkipsPopulate asserts a movie with no
// belongs_to_collection never calls the populator (nil CollectionID).
func TestMovieWorker_HandleForced_NoCollectionSkipsPopulate(t *testing.T) {
	tmdbClient := &fakeMovieTMDB{resp: &tmdb.MovieResponse{ID: 155, Title: "The Dark Knight"}}
	canonRepo := &fakeMovieCanon{getResp: movieCanonWithTMDB(9, 155)}
	pop := &fakeCollectionPopulator{}

	w, err := NewMovieWorker(MovieWorkerDeps{
		TMDB: tmdbClient, Movies: canonRepo, Collections: pop,
	})
	require.NoError(t, err)

	require.NoError(t, w.HandleForced(context.Background(), 9))
	assert.Equal(t, 0, pop.calls, "no collection → no populate")
}
