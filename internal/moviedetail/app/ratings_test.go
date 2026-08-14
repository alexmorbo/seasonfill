package app_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/internal/catalog/domain/movie"
	mdapp "github.com/alexmorbo/seasonfill/internal/moviedetail/app"
	ports "github.com/alexmorbo/seasonfill/internal/shared/dataports"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
)

type fakeRatingsCanon struct {
	canon movie.Canon
	err   error
}

func (f fakeRatingsCanon) GetByTMDBID(_ context.Context, _ domain.TMDBID) (movie.Canon, error) {
	return f.canon, f.err
}

// All six rating fields present → all copied through nil-preserving.
func TestRatingsUseCase_Get_AllPresent(t *testing.T) {
	tid := domain.TMDBID(603)
	canon := fakeRatingsCanon{canon: movie.Canon{
		ID: 7, TMDBID: &tid, Title: "The Matrix",
		TMDBRating: new(8.2), TMDBVotes: new(24000),
		IMDBRating: new(8.7), IMDBVotes: new(1900000),
		OMDBRated: new("R"), OMDBAwards: new("Won 4 Oscars."),
	}}
	uc := mdapp.NewRatingsUseCase(canon)

	page, err := uc.Get(context.Background(), tid)
	require.NoError(t, err)
	require.NotNil(t, page.TMDBRating)
	assert.InDelta(t, 8.2, *page.TMDBRating, 1e-9)
	require.NotNil(t, page.TMDBVotes)
	assert.Equal(t, 24000, *page.TMDBVotes)
	require.NotNil(t, page.IMDBRating)
	assert.InDelta(t, 8.7, *page.IMDBRating, 1e-9)
	require.NotNil(t, page.IMDBVotes)
	assert.Equal(t, 1900000, *page.IMDBVotes)
	require.NotNil(t, page.Rated)
	assert.Equal(t, "R", *page.Rated)
	require.NotNil(t, page.Awards)
	assert.Equal(t, "Won 4 Oscars.", *page.Awards)
}

// No ratings synced yet → every field nil, no error.
func TestRatingsUseCase_Get_AllAbsent(t *testing.T) {
	tid := domain.TMDBID(603)
	canon := fakeRatingsCanon{canon: movie.Canon{ID: 7, TMDBID: &tid, Title: "Canon Title"}}
	uc := mdapp.NewRatingsUseCase(canon)

	page, err := uc.Get(context.Background(), tid)
	require.NoError(t, err)
	assert.Nil(t, page.TMDBRating)
	assert.Nil(t, page.TMDBVotes)
	assert.Nil(t, page.IMDBRating)
	assert.Nil(t, page.IMDBVotes)
	assert.Nil(t, page.Rated)
	assert.Nil(t, page.Awards)
}

// TMDB present, OMDb absent → the two sources are independent (mixed presence).
func TestRatingsUseCase_Get_PartialTMDBOnly(t *testing.T) {
	tid := domain.TMDBID(603)
	canon := fakeRatingsCanon{canon: movie.Canon{
		ID: 7, TMDBID: &tid, Title: "The Matrix",
		TMDBRating: new(8.2), TMDBVotes: new(24000),
	}}
	uc := mdapp.NewRatingsUseCase(canon)

	page, err := uc.Get(context.Background(), tid)
	require.NoError(t, err)
	require.NotNil(t, page.TMDBRating)
	assert.Nil(t, page.IMDBRating)
	assert.Nil(t, page.Rated)
}

func TestRatingsUseCase_Get_NotFoundBubbles(t *testing.T) {
	canon := fakeRatingsCanon{err: ports.ErrNotFound}
	uc := mdapp.NewRatingsUseCase(canon)

	_, err := uc.Get(context.Background(), domain.TMDBID(1))
	require.ErrorIs(t, err, ports.ErrNotFound)
}
