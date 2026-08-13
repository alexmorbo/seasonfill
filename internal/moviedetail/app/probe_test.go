package app

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/alexmorbo/seasonfill/internal/catalog/domain/movie"
)

// verdictBySection indexes a dense verdict slice for assertions.
func verdictBySection(vs []MovieSectionVerdict) map[MovieSection]MovieSectionVerdict {
	m := make(map[MovieSection]MovieSectionVerdict, len(vs))
	for _, v := range vs {
		m[v.Section] = v
	}
	return m
}

func TestMovieProbe_AllNilStamps_AllStaleNever(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 14, 12, 0, 0, 0, time.UTC)
	canon := movie.Canon{ID: 1, Hydration: movie.HydrationFull} // every stamp nil

	vs := MovieProbe(canon, now)
	assert.Len(t, vs, len(MovieFixedSections))
	assert.True(t, AnyStale(vs))
	for _, v := range vs {
		assert.True(t, v.Stale, "section %s must be stale", v.Section)
		assert.Equal(t, "never", v.Reason, "section %s", v.Section)
	}
}

func TestMovieProbe_AllFreshStamps_NoneStale(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 14, 12, 0, 0, 0, time.UTC)
	recent := now.Add(-1 * time.Hour)
	canon := movie.Canon{
		ID:                         1,
		Hydration:                  movie.HydrationFull,
		EnrichmentTextSyncedAt:     new(recent),
		EnrichmentCastSyncedAt:     new(recent),
		EnrichmentRecsSyncedAt:     new(recent),
		EnrichmentMediaSyncedAt:    new(recent),
		EnrichmentKeywordsSyncedAt: new(recent),
	}

	vs := MovieProbe(canon, now)
	assert.False(t, AnyStale(vs))
	for _, v := range vs {
		assert.False(t, v.Stale, "section %s must be fresh", v.Section)
		assert.Equal(t, "fresh", v.Reason, "section %s", v.Section)
	}
}

func TestMovieProbe_Stub_AllStaleStub(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 14, 12, 0, 0, 0, time.UTC)
	recent := now.Add(-1 * time.Hour)
	// Even with fresh stamps, a stub movie is all-stale.
	canon := movie.Canon{
		ID:                     1,
		Hydration:              movie.HydrationStub,
		EnrichmentTextSyncedAt: new(recent),
	}

	vs := MovieProbe(canon, now)
	assert.True(t, AnyStale(vs))
	for _, v := range vs {
		assert.True(t, v.Stale, "section %s", v.Section)
		assert.Equal(t, "stub", v.Reason, "section %s", v.Section)
	}
}

func TestMovieProbe_MixedFreshAndMissing(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 14, 12, 0, 0, 0, time.UTC)
	recent := now.Add(-1 * time.Hour)
	canon := movie.Canon{
		ID:                     1,
		Hydration:              movie.HydrationFull,
		EnrichmentTextSyncedAt: new(recent), // fresh
		// cast/recs/media/keywords nil → stale
	}

	vs := MovieProbe(canon, now)
	assert.True(t, AnyStale(vs))
	by := verdictBySection(vs)
	assert.False(t, by[MovieSectionText].Stale, "text fresh")
	assert.True(t, by[MovieSectionCast].Stale, "cast missing → stale")
	assert.Equal(t, "never", by[MovieSectionCast].Reason)
}

func TestMovieProbe_ExpiredByTTL(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 14, 12, 0, 0, 0, time.UTC)
	// text TTL 7d — a 10d-old text stamp is expired; cast TTL 30d — 10d cast fresh.
	tenDaysAgo := now.Add(-10 * 24 * time.Hour)
	canon := movie.Canon{
		ID:                         1,
		Hydration:                  movie.HydrationFull,
		EnrichmentTextSyncedAt:     new(tenDaysAgo),
		EnrichmentCastSyncedAt:     new(tenDaysAgo),
		EnrichmentRecsSyncedAt:     new(tenDaysAgo),
		EnrichmentMediaSyncedAt:    new(tenDaysAgo),
		EnrichmentKeywordsSyncedAt: new(tenDaysAgo),
	}

	by := verdictBySection(MovieProbe(canon, now))
	assert.True(t, by[MovieSectionText].Stale, "text 10d > 7d TTL → expired")
	assert.Equal(t, "expired", by[MovieSectionText].Reason)
	assert.False(t, by[MovieSectionCast].Stale, "cast 10d < 30d TTL → fresh")
}
