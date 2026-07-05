package omdb

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMap_BreakingBadFixture(t *testing.T) {
	t.Parallel()
	resp := &Response{
		IMDBRating: "9.5",
		IMDBVotes:  "2,034,123",
		Rated:      "TV-MA",
		Awards:     "Won 16 Primetime Emmys",
		Ratings: []Rating{
			{Source: "Internet Movie Database", Value: "9.5/10"},
			{Source: "Rotten Tomatoes", Value: "96%"},
			{Source: "Metacritic", Value: "73/100"},
		},
	}
	out := Map(resp)
	require.NotNil(t, out.IMDBRating)
	assert.InDelta(t, 9.5, *out.IMDBRating, 1e-9)
	require.NotNil(t, out.IMDBVotes)
	assert.Equal(t, int64(2034123), *out.IMDBVotes)
	require.NotNil(t, out.OMDbRated)
	assert.Equal(t, "TV-MA", *out.OMDbRated)
	require.NotNil(t, out.OMDbAwards)
	assert.Equal(t, "Won 16 Primetime Emmys", *out.OMDbAwards)
	require.NotNil(t, out.OMDbRTRating)
	assert.Equal(t, 96, *out.OMDbRTRating)
	require.NotNil(t, out.OMDbMetacritic)
	assert.Equal(t, 73, *out.OMDbMetacritic)
}

func TestMap_NAValues_AllFieldsNil(t *testing.T) {
	t.Parallel()
	resp := &Response{
		IMDBRating: "N/A",
		IMDBVotes:  "N/A",
		Rated:      "N/A",
		Awards:     "N/A",
		Ratings: []Rating{
			{Source: "Rotten Tomatoes", Value: "N/A"},
			{Source: "Metacritic", Value: "N/A"},
		},
	}
	out := Map(resp)
	assert.Nil(t, out.IMDBRating)
	assert.Nil(t, out.IMDBVotes)
	assert.Nil(t, out.OMDbRated)
	assert.Nil(t, out.OMDbAwards)
	assert.Nil(t, out.OMDbRTRating)
	assert.Nil(t, out.OMDbMetacritic)
}

func TestMap_Rating_ParseFailure_ReturnsNil(t *testing.T) {
	t.Parallel()
	resp := &Response{IMDBRating: "not a number"}
	out := Map(resp)
	assert.Nil(t, out.IMDBRating)
}

func TestMap_Votes_CommaFormatted(t *testing.T) {
	t.Parallel()
	resp := &Response{IMDBVotes: "2,034,123"}
	out := Map(resp)
	require.NotNil(t, out.IMDBVotes)
	assert.Equal(t, int64(2034123), *out.IMDBVotes)
}

func TestMap_Votes_NoCommas(t *testing.T) {
	t.Parallel()
	resp := &Response{IMDBVotes: "500"}
	out := Map(resp)
	require.NotNil(t, out.IMDBVotes)
	assert.Equal(t, int64(500), *out.IMDBVotes)
}

func TestMap_Votes_ParseFailure_ReturnsNil(t *testing.T) {
	t.Parallel()
	resp := &Response{IMDBVotes: "xyz"}
	out := Map(resp)
	assert.Nil(t, out.IMDBVotes)
}

func TestMap_NilResponse_ReturnsZero(t *testing.T) {
	t.Parallel()
	out := Map(nil)
	assert.Equal(t, Enrichment{}, out)
}

func TestMap_WhitespaceTrim(t *testing.T) {
	t.Parallel()
	resp := &Response{IMDBRating: "  9.5  "}
	out := Map(resp)
	require.NotNil(t, out.IMDBRating)
	assert.InDelta(t, 9.5, *out.IMDBRating, 1e-9)

	resp2 := &Response{Rated: "  N/A  "}
	out2 := Map(resp2)
	assert.Nil(t, out2.OMDbRated)
}

// TestMap_EmptyResponse_AllFieldsNil — an empty payload (no string
// values present) yields the zero Enrichment, matching the contract
// for "OMDb returned nothing useful".
func TestMap_EmptyResponse_AllFieldsNil(t *testing.T) {
	t.Parallel()
	out := Map(&Response{})
	assert.Nil(t, out.IMDBRating)
	assert.Nil(t, out.IMDBVotes)
	assert.Nil(t, out.OMDbRated)
	assert.Nil(t, out.OMDbAwards)
	assert.Nil(t, out.OMDbRTRating)
	assert.Nil(t, out.OMDbMetacritic)
}

func TestMap_Ratings_AllThreeSources(t *testing.T) {
	t.Parallel()
	resp := &Response{
		Ratings: []Rating{
			{Source: "Internet Movie Database", Value: "8.0/10"},
			{Source: "Rotten Tomatoes", Value: "91%"},
			{Source: "Metacritic", Value: "69/100"},
		},
		ResponseFlag: "True",
	}
	out := Map(resp)
	require.NotNil(t, out.OMDbRTRating)
	assert.Equal(t, 91, *out.OMDbRTRating)
	require.NotNil(t, out.OMDbMetacritic)
	assert.Equal(t, 69, *out.OMDbMetacritic)
}

func TestMap_Ratings_MissingArray_BothNil(t *testing.T) {
	t.Parallel()
	out := Map(&Response{ResponseFlag: "True"})
	assert.Nil(t, out.OMDbRTRating)
	assert.Nil(t, out.OMDbMetacritic)
}

func TestMap_Ratings_OnlyIMDbEntry_BothNil(t *testing.T) {
	t.Parallel()
	resp := &Response{
		Ratings: []Rating{
			{Source: "Internet Movie Database", Value: "8.0/10"},
		},
	}
	out := Map(resp)
	assert.Nil(t, out.OMDbRTRating)
	assert.Nil(t, out.OMDbMetacritic)
}

func TestMap_Ratings_RTValue_EmptyOrNA_Nil(t *testing.T) {
	t.Parallel()
	for _, v := range []string{"N/A", "n/a", "", "  "} {
		out := Map(&Response{Ratings: []Rating{{Source: "Rotten Tomatoes", Value: v}}})
		assert.Nil(t, out.OMDbRTRating, "value=%q", v)
	}
}

func TestMap_Ratings_MetacriticValue_EmptyOrNA_Nil(t *testing.T) {
	t.Parallel()
	for _, v := range []string{"N/A", "n/a", "", "  "} {
		out := Map(&Response{Ratings: []Rating{{Source: "Metacritic", Value: v}}})
		assert.Nil(t, out.OMDbMetacritic, "value=%q", v)
	}
}

func TestMap_Ratings_UnparseableValue_Nil(t *testing.T) {
	t.Parallel()
	out := Map(&Response{Ratings: []Rating{
		{Source: "Rotten Tomatoes", Value: "unrated"},
		{Source: "Metacritic", Value: "tbd"},
	}})
	assert.Nil(t, out.OMDbRTRating)
	assert.Nil(t, out.OMDbMetacritic)
}
