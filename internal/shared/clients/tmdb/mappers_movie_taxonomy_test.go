package tmdb

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMapMovieGenres(t *testing.T) {
	m := &MovieResponse{Genres: []TVGenre{{ID: 18, Name: "Drama"}, {ID: 878, Name: "Science Fiction"}}}
	got := MapMovieGenres(m)
	require.Len(t, got, 2)
	require.NotNil(t, got[0].TMDBID)
	assert.EqualValues(t, 18, *got[0].TMDBID)
	assert.Equal(t, "Drama", got[0].Name)
	assert.EqualValues(t, 878, *got[1].TMDBID)
	assert.Equal(t, "Science Fiction", got[1].Name)

	assert.Nil(t, MapMovieGenres(nil))
	assert.Empty(t, MapMovieGenres(&MovieResponse{}))
}

func TestMapMovieKeywords(t *testing.T) {
	// movie shape: keywords.keywords[] (NOT results[]).
	m := &MovieResponse{Keywords: &MovieKeywords{Keywords: []TVKeyword{{ID: 4565, Name: "dystopia"}, {ID: 9951, Name: "alien"}}}}
	got := MapMovieKeywords(m)
	require.Len(t, got, 2)
	assert.EqualValues(t, 4565, *got[0].TMDBID)
	assert.Equal(t, "dystopia", got[0].Name)

	// nil sub-resource → nil (append token absent / no keywords).
	assert.Nil(t, MapMovieKeywords(&MovieResponse{}))
	assert.Nil(t, MapMovieKeywords(nil))
}

func TestMapMovieCompanies(t *testing.T) {
	m := &MovieResponse{ProductionCompanies: []TVCompany{
		{ID: 923, Name: "Legendary Pictures", LogoPath: "/logo.png", OriginCountry: "US"},
		{ID: 33, Name: "Universal", LogoPath: "", OriginCountry: ""},
	}}
	got := MapMovieCompanies(m)
	require.Len(t, got, 2)
	assert.EqualValues(t, 923, *got[0].TMDBID)
	assert.Equal(t, "Legendary Pictures", got[0].Name)
	require.NotNil(t, got[0].LogoAsset)
	assert.Equal(t, "/logo.png", *got[0].LogoAsset)
	require.NotNil(t, got[0].OriginCountry)
	assert.Equal(t, "US", *got[0].OriginCountry)
	// empty logo/origin → nil pointers (nonEmptyPtr).
	assert.Nil(t, got[1].LogoAsset)
	assert.Nil(t, got[1].OriginCountry)

	assert.Nil(t, MapMovieCompanies(nil))
}
