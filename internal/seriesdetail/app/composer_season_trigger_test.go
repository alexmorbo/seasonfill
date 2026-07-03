package seriesdetail

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/internal/catalog/domain/series"
	"github.com/alexmorbo/seasonfill/internal/seriesdetail/app/freshener"
)

// S-C: the single-season detail path MUST drive the freshener for exactly that
// season, ModeSync, force=false, BEFORE reading — this is the trigger that
// populates season_texts. Asserts against recFakeFreshener (records calls).
func TestComposer_GetSeason_DispatchesSeasonFreshener_ModeSync(t *testing.T) {
	deps, _, _ := baseDeps(t)
	deps.Seasons = &fakeSeasons{rows: []series.CanonSeason{
		{ID: 1, SeriesID: 42, SeasonNumber: 1},
		{ID: 2, SeriesID: 42, SeasonNumber: 2},
	}}
	deps.Episodes = &fakeEpisodes{rows: []series.CanonEpisode{
		{ID: 20, SeriesID: 42, SeasonNumber: 2, EpisodeNumber: 1},
	}}
	fresh := &recFakeFreshener{}
	deps.Freshener = fresh

	c := NewComposer(deps)
	_, err := c.GetSeason(context.Background(), "alpha", 1, 2, "ru-RU")
	require.NoError(t, err)

	calls := fresh.Calls()
	require.Len(t, calls, 1, "GetSeason must invoke the freshener exactly once")
	got := calls[0]
	assert.Equal(t, "ru-RU", got.lang, "request lang forwarded verbatim")
	assert.Equal(t, ModeSync, got.mode, "user is waiting on this season → ModeSync")
	assert.False(t, got.force, "force=false — TTL respected")
	require.Len(t, got.sections, 1)
	assert.Equal(t, freshener.SeasonSection(2), got.sections[0],
		"scope MUST be exactly season:2 (the opened season)")
}

// nil Freshener → no panic, still returns the season (Story 532 behaviour).
func TestComposer_GetSeason_NilFreshener_StillReads(t *testing.T) {
	deps, _, _ := baseDeps(t)
	deps.Seasons = &fakeSeasons{rows: []series.CanonSeason{{ID: 1, SeriesID: 42, SeasonNumber: 1}}}
	deps.Freshener = nil
	c := NewComposer(deps)
	d, err := c.GetSeason(context.Background(), "alpha", 1, 1, "en-US")
	require.NoError(t, err)
	require.Len(t, d.Seasons, 1)
}
