package tmdb

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPickBestTrailer_PrefersOfficialYouTubeTrailer(t *testing.T) {
	vids := []TVVideo{
		{ID: "a", Site: "YouTube", Key: "k1", Type: "Teaser", Official: true, PublishedAt: "2024-01-01T00:00:00.000Z"},
		{ID: "b", Site: "YouTube", Key: "k2", Type: "Trailer", Official: false, PublishedAt: "2024-02-01T00:00:00.000Z"},
		{ID: "c", Site: "YouTube", Key: "k3", Type: "Trailer", Official: true, PublishedAt: "2024-01-15T00:00:00.000Z"},
		{ID: "d", Site: "Vimeo", Key: "k4", Type: "Trailer", Official: true, PublishedAt: "2025-01-01T00:00:00.000Z"},
	}
	got, ok := pickBestTrailer(vids)
	require.True(t, ok)
	// official Trailer on YouTube wins over the newer non-official Trailer (b),
	// the official Teaser (a), and the non-YouTube Trailer (d).
	assert.Equal(t, "c", got.ID)
}

func TestPickBestTrailer_NewestOfficialTrailerWins(t *testing.T) {
	vids := []TVVideo{
		{ID: "old", Site: "youtube", Key: "k1", Type: "Trailer", Official: true, PublishedAt: "2023-01-01T00:00:00.000Z"},
		{ID: "new", Site: "YouTube", Key: "k2", Type: "Trailer", Official: true, PublishedAt: "2024-06-01T00:00:00.000Z"},
	}
	got, ok := pickBestTrailer(vids)
	require.True(t, ok)
	assert.Equal(t, "new", got.ID)
}

func TestPickBestTrailer_SkipsNonYouTubeAndKeyless(t *testing.T) {
	vids := []TVVideo{
		{ID: "vimeo", Site: "Vimeo", Key: "k1", Type: "Trailer", Official: true},
		{ID: "nokey", Site: "YouTube", Key: "", Type: "Trailer", Official: true},
	}
	_, ok := pickBestTrailer(vids)
	assert.False(t, ok, "no YouTube-with-key candidate → no trailer")
}

func TestMapMovieBestTrailer_NilWhenNoVideos(t *testing.T) {
	assert.Nil(t, MapMovieBestTrailer(nil))
	assert.Nil(t, MapMovieBestTrailer(&MovieResponse{}))
	assert.Nil(t, MapMovieBestTrailer(&MovieResponse{Videos: &TVVideos{}}))
}

func TestMapMovieBestTrailer_MapsChosenFields(t *testing.T) {
	m := &MovieResponse{Videos: &TVVideos{Results: []TVVideo{
		{ID: "vid1", ISO6391: "en", Site: "YouTube", Key: "abc123", Name: "Official Trailer",
			Type: "Trailer", Official: true, PublishedAt: "2024-02-01T00:00:00.000Z"},
	}}}
	got := MapMovieBestTrailer(m)
	require.NotNil(t, got)
	require.NotNil(t, got.TMDBVideoID)
	assert.Equal(t, "vid1", *got.TMDBVideoID)
	assert.Equal(t, "Official Trailer", got.Name)
	require.NotNil(t, got.Key)
	assert.Equal(t, "abc123", *got.Key)
	assert.True(t, got.Official)
	require.NotNil(t, got.PublishedAt)
}

func TestMapMovieRecommendations_MapsStubsInTMDBRankOrder(t *testing.T) {
	m := &MovieResponse{Recommendations: &MovieRecommendations{Results: []MovieRecommendation{
		{ID: 603, Title: "The Matrix", OriginalLanguage: "en", ReleaseDate: "1999-03-30", VoteAverage: 8.2, VoteCount: 20000},
		{ID: 604, Title: "The Matrix Reloaded", ReleaseDate: "2003-05-15"},
		{ID: 0, Title: "junk"}, // zero id skipped
	}}}
	stubs, order := MapMovieRecommendations(m)
	require.Len(t, stubs, 2)
	require.Len(t, order, 2)
	assert.EqualValues(t, 603, order[0])
	assert.EqualValues(t, 604, order[1])
	assert.Equal(t, "The Matrix", stubs[0].Title)
	assert.Equal(t, "stub", string(stubs[0].Hydration))
	require.NotNil(t, stubs[0].Year)
	assert.Equal(t, 1999, *stubs[0].Year)
	require.NotNil(t, stubs[0].TMDBRating)

	s, o := MapMovieRecommendations(nil)
	assert.Nil(t, s)
	assert.Nil(t, o)
	s, o = MapMovieRecommendations(&MovieResponse{})
	assert.Nil(t, s)
	assert.Nil(t, o)
}
