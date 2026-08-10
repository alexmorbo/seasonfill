package app

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	disco "github.com/alexmorbo/seasonfill/internal/discovery/domain"
	"github.com/alexmorbo/seasonfill/internal/shared/clients/tmdb"
	shareddomain "github.com/alexmorbo/seasonfill/internal/shared/domain"
)

type stubLoader struct {
	tmdb    []int64
	keyword []int64
	err     error
	calls   int
}

func (s *stubLoader) LoadBlockSets(_ context.Context) ([]int64, []int64, error) {
	s.calls++
	return s.tmdb, s.keyword, s.err
}

func tmdbID(v int) *shareddomain.TMDBID { id := shareddomain.TMDBID(v); return &id }

func TestBlocklistCache_FilterBlocked(t *testing.T) {
	t.Parallel()
	loader := &stubLoader{tmdb: []int64{1399, 500}, keyword: []int64{210024}}
	c := NewBlocklistCache(loader)
	require.NoError(t, c.Refresh(context.Background()))
	assert.Equal(t, uint64(1), c.Epoch())

	items := []disco.Item{
		{SeriesID: 1, TMDBID: tmdbID(1399)}, // blocked
		{SeriesID: 2, TMDBID: tmdbID(42)},   // kept
		{SeriesID: 3, TMDBID: nil},          // stub, never blocked
		{SeriesID: 4, TMDBID: tmdbID(500)},  // blocked
	}
	out := c.FilterBlocked(items)
	require.Len(t, out, 2)
	assert.Equal(t, shareddomain.SeriesID(2), out[0].SeriesID)
	assert.Equal(t, shareddomain.SeriesID(3), out[1].SeriesID)

	assert.ElementsMatch(t, []int64{210024}, c.BlockedKeywordIDs())
}

func TestBlocklistCache_EmptyAndNilPassthrough(t *testing.T) {
	t.Parallel()
	items := []disco.Item{{SeriesID: 1, TMDBID: tmdbID(1)}}

	// nil cache → unchanged.
	var nilCache *BlocklistCache
	assert.Equal(t, items, nilCache.FilterBlocked(items))
	assert.Zero(t, nilCache.Epoch())
	assert.Nil(t, nilCache.BlockedKeywordIDs())

	// empty blocklist → unchanged.
	c := NewBlocklistCache(&stubLoader{})
	require.NoError(t, c.Refresh(context.Background()))
	assert.Equal(t, items, c.FilterBlocked(items))
}

func TestBlocklistCache_ApplyKeywordBlocklist(t *testing.T) {
	t.Parallel()

	// Union with caller-supplied WithoutKeywords + dedupe. 55 is supplied by
	// the caller AND blocked → must appear exactly once.
	loader := &stubLoader{keyword: []int64{55, 210024}}
	c := NewBlocklistCache(loader)
	require.NoError(t, c.Refresh(context.Background()))

	in := tmdb.DiscoverFilter{WithGenres: []int{18}, WithoutKeywords: []int{55, 99}}
	got := c.ApplyKeywordBlocklist(in)
	assert.ElementsMatch(t, []int{55, 99, 210024}, got.WithoutKeywords)
	assert.Equal(t, []int{18}, got.WithGenres, "other fields preserved")
	// Caller's slice is not mutated in place.
	assert.Equal(t, []int{55, 99}, in.WithoutKeywords)

	// Empty keyword set → filter unchanged.
	empty := NewBlocklistCache(&stubLoader{})
	require.NoError(t, empty.Refresh(context.Background()))
	base := tmdb.DiscoverFilter{WithKeywords: []int{7}}
	assert.Equal(t, base, empty.ApplyKeywordBlocklist(base))

	// nil cache → filter unchanged.
	var nilCache *BlocklistCache
	assert.Equal(t, base, nilCache.ApplyKeywordBlocklist(base))
}

func TestBlocklistCache_RefreshErrorRetainsSnapshot(t *testing.T) {
	t.Parallel()
	loader := &stubLoader{tmdb: []int64{7}}
	c := NewBlocklistCache(loader)
	require.NoError(t, c.Refresh(context.Background()))
	assert.True(t, c.IsBlockedTMDB(7))
	e1 := c.Epoch()

	loader.err = errors.New("db down")
	require.Error(t, c.Refresh(context.Background()))
	// Snapshot + epoch unchanged after a failed refresh.
	assert.True(t, c.IsBlockedTMDB(7))
	assert.Equal(t, e1, c.Epoch())
}
