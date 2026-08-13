package tmdb

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/internal/enrichment/domain/people"
)

// TestMapMovieCast_CarriesOrderAndDedupes proves the Ф1.1a mapper:
//   - sets CreditOrder unconditionally (0 = lead is kept, not dropped),
//   - keeps credits parallel to tmdbPersonIDs,
//   - dedupes person stubs by tmdb id while keeping one credit per cast row,
//   - drops rows with a zero person id or empty credit_id,
//   - stamps movie identity (media_type=movie, tmdb_media_id, title, poster, rating).
func TestMapMovieCast_CarriesOrderAndDedupes(t *testing.T) {
	m := &MovieResponse{
		ID:          42,
		Title:       "Dune",
		PosterPath:  "/dune.jpg",
		ReleaseDate: "2021-10-22",
		VoteAverage: 8.1,
		VoteCount:   9001,
		Credits: &MovieCredits{
			Cast: []MovieCastMember{
				{ID: 100, Name: "Lead", CreditID: "c-lead", Character: "Paul", Order: 0, ProfilePath: "/p.jpg"},
				{ID: 200, Name: "Second", CreditID: "c-2", Character: "Chani", Order: 1},
				{ID: 100, Name: "Lead", CreditID: "c-lead-dual", Character: "Muad'Dib", Order: 5}, // same person, 2nd credit
				{ID: 0, Name: "NoID", CreditID: "c-bad", Order: 2},                                // dropped (zero id)
				{ID: 300, Name: "NoCredit", CreditID: "", Order: 3},                               // dropped (empty credit_id)
			},
		},
	}

	credits, stubs, tmdbPersonIDs := MapMovieCast(m)

	require.Len(t, credits, 3, "3 valid cast credits (2 for person 100, 1 for 200)")
	require.Len(t, tmdbPersonIDs, 3, "tmdbPersonIDs parallel to credits")
	require.Len(t, stubs, 2, "person 100 deduped to a single stub")

	// Lead: order 0 MUST be present (not dropped as zero).
	require.NotNil(t, credits[0].CreditOrder)
	assert.Equal(t, 0, *credits[0].CreditOrder)
	assert.Equal(t, MediaTypeMovie, credits[0].MediaType)
	assert.Equal(t, int64(42), credits[0].TMDBMediaID)
	assert.Equal(t, "Dune", credits[0].Title)
	assert.Equal(t, people.SeriesCreditCast, credits[0].Kind)
	require.NotNil(t, credits[0].PosterAsset)
	assert.Equal(t, "/dune.jpg", *credits[0].PosterAsset)
	require.NotNil(t, credits[0].TMDBRating)
	assert.InDelta(t, 8.1, *credits[0].TMDBRating, 0.001)
	require.NotNil(t, credits[0].TMDBVotes)
	assert.Equal(t, 9001, *credits[0].TMDBVotes)
	assert.Equal(t, int64(100), tmdbPersonIDs[0])

	require.NotNil(t, credits[1].CreditOrder)
	assert.Equal(t, 1, *credits[1].CreditOrder)
	assert.Equal(t, int64(200), tmdbPersonIDs[1])

	require.NotNil(t, credits[2].CreditOrder)
	assert.Equal(t, 5, *credits[2].CreditOrder)
	assert.Equal(t, int64(100), tmdbPersonIDs[2], "2nd credit still maps to person 100")

	// PersonID is unresolved at mapper stage.
	for _, c := range credits {
		assert.Zero(t, c.PersonID)
	}
}

// TestMapMovieCast_NilCredits returns all-nil for a movie without credits.
func TestMapMovieCast_NilCredits(t *testing.T) {
	credits, stubs, ids := MapMovieCast(&MovieResponse{ID: 1, Title: "X"})
	assert.Nil(t, credits)
	assert.Nil(t, stubs)
	assert.Nil(t, ids)
}
