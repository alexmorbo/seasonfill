package enrichment

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/internal/enrichment/domain/people"
	"github.com/alexmorbo/seasonfill/internal/shared/clients/tmdb"
)

// TestMapSeriesCreditsToPersonCredits_PopulatesCreditOrder proves the Story
// 1087b write mapper carries the aggregate_credits billing order from the
// SeriesCredit source onto the persisted PersonCredit row.
func TestMapSeriesCreditsToPersonCredits_PopulatesCreditOrder(t *testing.T) {
	t.Parallel()
	ord := 3
	eps := 9
	creds := []people.SeriesCredit{{
		PersonID:     7,
		Kind:         people.SeriesCreditCast,
		TMDBCreditID: "c7",
		CreditOrder:  &ord,
		EpisodeCount: &eps,
	}}
	out := mapSeriesCreditsToPersonCredits(creds, &tmdb.TVResponse{Name: "X"}, 900, nil)
	require.Len(t, out, 1)
	require.NotNil(t, out[0].CreditOrder)
	assert.Equal(t, 3, *out[0].CreditOrder)
}

// TestMapSeriesCreditsToPersonCredits_PopulatesLastAppearance proves the Story
// 1090 mapper threads a per-person max season number from lastAppByPerson onto
// the persisted PersonCredit row, and leaves it nil for persons absent from the
// map (so the writer's MAX-merge preserves any stored value).
func TestMapSeriesCreditsToPersonCredits_PopulatesLastAppearance(t *testing.T) {
	t.Parallel()
	creds := []people.SeriesCredit{
		{PersonID: 7, Kind: people.SeriesCreditCast, TMDBCreditID: "c7"},
		{PersonID: 8, Kind: people.SeriesCreditCast, TMDBCreditID: "c8"},
	}
	lastApp := map[int64]int{7: 4} // person 8 absent → nil
	out := mapSeriesCreditsToPersonCredits(creds, &tmdb.TVResponse{Name: "X"}, 900, lastApp)
	require.Len(t, out, 2)
	require.NotNil(t, out[0].LastAppearanceSeason)
	assert.Equal(t, 4, *out[0].LastAppearanceSeason)
	assert.Nil(t, out[1].LastAppearanceSeason, "person not in map → nil (MAX-merge preserves stored)")
}

// TestMapSeriesCreditsToPersonCredits_PopulatesRating proves the Story 1034
// mapper threads the series' own TMDB vote_average onto every persisted
// PersonCredit row (the show's rating IS the person-credit ★rating), so the
// series-worker write path no longer emits a NULL that blanks the person-page
// "other credits" card rating.
func TestMapSeriesCreditsToPersonCredits_PopulatesRating(t *testing.T) {
	t.Parallel()
	creds := []people.SeriesCredit{
		{PersonID: 7, Kind: people.SeriesCreditCast, TMDBCreditID: "c7"},
		{PersonID: 8, Kind: people.SeriesCreditCrew, TMDBCreditID: "c8"},
	}
	out := mapSeriesCreditsToPersonCredits(creds, &tmdb.TVResponse{Name: "X", VoteAverage: 6.528}, 900, nil)
	require.Len(t, out, 2)
	for i := range out {
		require.NotNil(t, out[i].TMDBRating, "every row carries the show rating")
		assert.InDelta(t, 6.528, *out[i].TMDBRating, 1e-9)
	}
}

// TestMapSeriesCreditsToPersonCredits_ZeroRatingStaysNil proves an absent TMDB
// rating (vote_average 0.0) maps to a nil TMDBRating rather than a fake 0-star,
// so the repository COALESCE-guard can preserve any stored value instead of
// being clobbered by a synthetic zero.
func TestMapSeriesCreditsToPersonCredits_ZeroRatingStaysNil(t *testing.T) {
	t.Parallel()
	creds := []people.SeriesCredit{{PersonID: 7, Kind: people.SeriesCreditCast, TMDBCreditID: "c7"}}
	out := mapSeriesCreditsToPersonCredits(creds, &tmdb.TVResponse{Name: "X", VoteAverage: 0}, 900, nil)
	require.Len(t, out, 1)
	assert.Nil(t, out[0].TMDBRating, "vote_average 0.0 → nil, not a fake 0-star")
}
