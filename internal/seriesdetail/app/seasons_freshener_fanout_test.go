package seriesdetail

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/internal/catalog/domain/series"
	"github.com/alexmorbo/seasonfill/internal/seriesdetail/app/freshener"
)

// S-C phase 2: after the skeleton call, SeasonsComposer must drive the
// freshener with a SeasonSection(n) for EVERY listed season, ModeSync.
func TestSeasonsComposer_FansOutSeasonSections_ModeSync(t *testing.T) {
	t.Parallel()
	fresh := &recFakeFreshener{}
	c := NewSeasonsComposer(SeasonsDeps{
		Series: &seasonsFakeSeries{canon: fullCanon()},
		Seasons: &seasonsFakeSeasons{rows: []series.CanonSeason{
			{SeasonNumber: 1},
			{SeasonNumber: 2},
			{SeasonNumber: 3},
		}},
		SeasonTexts: &seasonsFakeTexts{},
		Aggregates:  &seasonsFakeAgg{},
		Freshener:   fresh,
		Logger:      seasonsQuietLogger(),
	})

	_, err := c.Compose(context.Background(), 42, "ru-RU")
	require.NoError(t, err)

	calls := fresh.Calls()
	require.Len(t, calls, 2, "phase 1 (skeleton) + phase 2 (season fan-out)")

	// Phase 1 = skeleton.
	assert.Equal(t, []freshener.Section{freshener.SectionSkeleton}, calls[0].sections)
	assert.Equal(t, ModeSync, calls[0].mode)

	// Phase 2 = one season:N section per season, ModeSync, force=false.
	assert.Equal(t, ModeSync, calls[1].mode)
	assert.False(t, calls[1].force)
	assert.Equal(t, "ru-RU", calls[1].lang)
	assert.ElementsMatch(t,
		[]freshener.Section{
			freshener.SeasonSection(1),
			freshener.SeasonSection(2),
			freshener.SeasonSection(3),
		},
		calls[1].sections,
		"phase 2 must carry a season:N section for every listed season")
}

// No seasons → no phase-2 call (nothing to fan out); skeleton call still fires.
func TestSeasonsComposer_NoSeasons_NoFanout(t *testing.T) {
	t.Parallel()
	fresh := &recFakeFreshener{}
	c := NewSeasonsComposer(SeasonsDeps{
		Series:      &seasonsFakeSeries{canon: fullCanon()},
		Seasons:     &seasonsFakeSeasons{rows: []series.CanonSeason{}},
		SeasonTexts: &seasonsFakeTexts{},
		Aggregates:  &seasonsFakeAgg{},
		Freshener:   fresh,
		Logger:      seasonsQuietLogger(),
	})
	_, err := c.Compose(context.Background(), 42, "en-US")
	require.NoError(t, err)
	require.Len(t, fresh.Calls(), 1, "only the skeleton call; no season fan-out")
}
