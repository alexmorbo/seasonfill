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
	out := mapSeriesCreditsToPersonCredits(creds, &tmdb.TVResponse{Name: "X"}, 900)
	require.Len(t, out, 1)
	require.NotNil(t, out[0].CreditOrder)
	assert.Equal(t, 3, *out[0].CreditOrder)
}
