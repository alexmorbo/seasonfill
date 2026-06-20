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
}

func TestMap_NAValues_AllFieldsNil(t *testing.T) {
	t.Parallel()
	resp := &Response{
		IMDBRating: "N/A",
		IMDBVotes:  "N/A",
		Rated:      "N/A",
		Awards:     "N/A",
	}
	out := Map(resp)
	assert.Nil(t, out.IMDBRating)
	assert.Nil(t, out.IMDBVotes)
	assert.Nil(t, out.OMDbRated)
	assert.Nil(t, out.OMDbAwards)
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
}
