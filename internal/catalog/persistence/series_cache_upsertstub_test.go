package persistence

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/internal/catalog/domain/series"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
	"github.com/alexmorbo/seasonfill/internal/shared/testhelpers"
)

// richCacheEntry builds a fully-populated cache entry (non-default
// monitored + all four stats set) with external IDs derived from id so
// two distinct sonarr ids never collapse onto one canon row. thinStub
// below reuses the SAME external ids for a given id so UpsertStub
// resolves the identical canon on the conflict path.
func richCacheEntry(instance domain.InstanceName, id domain.SonarrSeriesID) series.CacheEntry {
	tvdb := domain.TVDBID(70000 + int(id))
	tmdb := domain.TMDBID(80000 + int(id))
	return series.CacheEntry{
		InstanceName:      instance,
		SonarrSeriesID:    id,
		Title:             "Rich Series",
		TitleSlug:         "rich-series",
		TVDBID:            &tvdb,
		TMDBID:            &tmdb,
		Monitored:         false,
		MissingCount:      5,
		EpisodeFileCount:  12,
		SizeOnDiskBytes:   999,
		AiredEpisodeCount: 20,
	}
}

// thinStubEntry mirrors what webhookSeriesToCacheEntry produces: monitored
// hardcoded true, all stats zero, external ids present so canon resolves to
// the same row as richCacheEntry(id). slug is intentionally changed so the
// test can prove the stub DID refresh the columns in its subset.
func thinStubEntry(instance domain.InstanceName, id domain.SonarrSeriesID) series.CacheEntry {
	tvdb := domain.TVDBID(70000 + int(id))
	tmdb := domain.TMDBID(80000 + int(id))
	return series.CacheEntry{
		InstanceName:   instance,
		SonarrSeriesID: id,
		Title:          "Rich Series",
		TitleSlug:      "rich-series-renamed",
		TVDBID:         &tvdb,
		TMDBID:         &tmdb,
		Monitored:      true,
	}
}

// UpsertStub on a NEW (instance, sonarr_series_id) inserts the row —
// the struct's monitored:true + zero stats land unmodified (self-heals
// on the next scan).
func TestSeriesCacheRepository_UpsertStub_InsertPath(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			repo := NewSeriesCacheRepository(db, NewSeriesRepository(db))
			ctx := context.Background()

			require.NoError(t, repo.UpsertStub(ctx, thinStubEntry("main", 700)))

			got, err := repo.Get(ctx, "main", 700)
			require.NoError(t, err)
			assert.Equal(t, "rich-series-renamed", got.TitleSlug)
			assert.True(t, got.Monitored, "insert path: monitored:true lands")
			assert.Equal(t, 0, got.MissingCount)
			assert.Equal(t, 0, got.EpisodeFileCount)
			assert.Equal(t, int64(0), got.SizeOnDiskBytes)
			assert.Equal(t, 0, got.AiredEpisodeCount)
		})
	}
}

// REGRESSION GUARD: a thin UpsertStub over an EXISTING rich row must
// preserve monitored + every stat, while still refreshing title_slug /
// updated_at. This is the core SI-6 guarantee — a webhook SeriesAdd can
// no longer zero real cached stats.
func TestSeriesCacheRepository_UpsertStub_PreservesRichStatsOnConflict(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			repo := NewSeriesCacheRepository(db, NewSeriesRepository(db))
			ctx := context.Background()

			// 1) rich writer lands real monitored + stats.
			require.NoError(t, repo.Upsert(ctx, richCacheEntry("main", 701)))
			before, err := repo.Get(ctx, "main", 701)
			require.NoError(t, err)
			require.False(t, before.Monitored)
			require.Equal(t, 5, before.MissingCount)
			require.Equal(t, 12, before.EpisodeFileCount)
			require.Equal(t, int64(999), before.SizeOnDiskBytes)
			require.Equal(t, 20, before.AiredEpisodeCount)

			// 2) thin stub over the same PK — must NOT touch the stats.
			require.NoError(t, repo.UpsertStub(ctx, thinStubEntry("main", 701)))

			after, err := repo.Get(ctx, "main", 701)
			require.NoError(t, err)

			// preserved (omitted from the stub conflict set):
			assert.False(t, after.Monitored, "monitored preserved")
			assert.Equal(t, 5, after.MissingCount, "missing_count preserved")
			assert.Equal(t, 12, after.EpisodeFileCount, "episode_file_count preserved")
			assert.Equal(t, int64(999), after.SizeOnDiskBytes, "size_on_disk_bytes preserved")
			assert.Equal(t, 20, after.AiredEpisodeCount, "aired_episode_count preserved")

			// refreshed (in the stub conflict set):
			assert.Equal(t, "rich-series-renamed", after.TitleSlug, "title_slug refreshed")
			assert.False(t, after.UpdatedAt.Before(before.UpdatedAt), "updated_at refreshed")
		})
	}
}
