package app

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	disco "github.com/alexmorbo/seasonfill/internal/discovery/domain"
	"github.com/alexmorbo/seasonfill/internal/shared/clients/tmdb"
	shareddomain "github.com/alexmorbo/seasonfill/internal/shared/domain"
)

func tmdbID(v int) *shareddomain.TMDBID { id := shareddomain.TMDBID(v); return &id }

// Since Ф8-U-5 the blocklist is per-user; the boot cache holds no user context
// so Refresh is a NO-OP that publishes an empty snapshot and bumps the epoch.
// FilterBlocked / IsBlockedTMDB / BlockedKeywordIDs / ApplyKeywordBlocklist are
// pass-throughs over that empty snapshot until U-5b restores the per-user
// read-time load on the discover handlers.

func TestBlocklistCache_RefreshEmptyNoop(t *testing.T) {
	t.Parallel()
	c := NewBlocklistCache()
	require.NoError(t, c.Refresh(context.Background()))
	assert.Equal(t, uint64(1), c.Epoch(), "epoch bumps once per Refresh")
	// A second refresh bumps again.
	require.NoError(t, c.Refresh(context.Background()))
	assert.Equal(t, uint64(2), c.Epoch())

	items := []disco.Item{
		{SeriesID: 1, TMDBID: tmdbID(1399)},
		{SeriesID: 2, TMDBID: tmdbID(42)},
		{SeriesID: 3, TMDBID: nil},
	}
	assert.Equal(t, items, c.FilterBlocked(items), "empty snapshot → passthrough")
	assert.False(t, c.IsBlockedTMDB(1399), "nothing blocked over empty snapshot")
	assert.Nil(t, c.BlockedKeywordIDs())
}

func TestBlocklistCache_EmptyAndNilPassthrough(t *testing.T) {
	t.Parallel()
	items := []disco.Item{{SeriesID: 1, TMDBID: tmdbID(1)}}

	// nil cache → unchanged.
	var nilCache *BlocklistCache
	assert.Equal(t, items, nilCache.FilterBlocked(items))
	assert.Zero(t, nilCache.Epoch())
	assert.Nil(t, nilCache.BlockedKeywordIDs())

	// empty (fresh) cache → unchanged.
	c := NewBlocklistCache()
	require.NoError(t, c.Refresh(context.Background()))
	assert.Equal(t, items, c.FilterBlocked(items))
}

func TestBlocklistCache_ApplyKeywordBlocklist(t *testing.T) {
	t.Parallel()

	// Over the empty snapshot the caller's filter is returned unchanged.
	c := NewBlocklistCache()
	require.NoError(t, c.Refresh(context.Background()))
	in := tmdb.DiscoverFilter{WithGenres: []int{18}, WithoutKeywords: []int{55, 99}}
	got := c.ApplyKeywordBlocklist(in)
	assert.ElementsMatch(t, []int{55, 99}, got.WithoutKeywords, "no blocked keywords added")
	assert.Equal(t, []int{18}, got.WithGenres, "other fields preserved")

	// nil cache → filter unchanged.
	var nilCache *BlocklistCache
	base := tmdb.DiscoverFilter{WithKeywords: []int{7}}
	assert.Equal(t, base, nilCache.ApplyKeywordBlocklist(base))
}
