package tmdb

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestFixture_TV117648_ImagesShape validates the S-A fixture parses into the
// typed TVImages slices with the expected iso_639_1 tagging + vote metadata.
func TestFixture_TV117648_ImagesShape(t *testing.T) {
	tv := loadTV(t, "tv_117648.json")

	require.Equal(t, int64(117648), tv.ID)
	assert.Equal(t, "/ru_root_ferma.jpg", tv.PosterPath)
	assert.Equal(t, "/ru_root_backdrop.jpg", tv.BackdropPath)

	require.NotNil(t, tv.Images)
	require.Len(t, tv.Images.Posters, 5)
	require.Len(t, tv.Images.Backdrops, 2)

	// First poster is the high-vote EN entry.
	p0 := tv.Images.Posters[0]
	assert.Equal(t, "/en_high.jpg", p0.FilePath)
	require.NotNil(t, p0.ISO6391)
	assert.Equal(t, "en", *p0.ISO6391)
	assert.Equal(t, 8.1, p0.VoteAverage)
	assert.Equal(t, 40, p0.VoteCount)

	// A language-agnostic poster ships iso_639_1 == nil.
	var sawNil bool
	for _, p := range tv.Images.Posters {
		if p.ISO6391 == nil {
			sawNil = true
		}
	}
	assert.True(t, sawNil, "fixture must carry a language-agnostic poster")

	// First backdrop is language-agnostic (nil).
	assert.Nil(t, tv.Images.Backdrops[0].ISO6391)
}
