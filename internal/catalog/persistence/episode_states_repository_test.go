package persistence

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/internal/catalog/domain/series"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
	sharedErrors "github.com/alexmorbo/seasonfill/internal/shared/errors"
	"github.com/alexmorbo/seasonfill/internal/shared/testhelpers"
)

func TestEpisodeStatesRepository_UpsertAndGet(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			ctx := context.Background()
			seriesID, err := NewSeriesRepository(db).Upsert(ctx, sampleCanon("Andor"))
			require.NoError(t, err)
			epIDRaw, err := NewEpisodesRepository(db).Upsert(ctx, series.CanonEpisode{
				SeriesID: seriesID, SeasonNumber: 1, EpisodeNumber: 1,
			})
			require.NoError(t, err)
			epID := domain.EpisodeID(epIDRaw)
			repo := NewEpisodeStatesRepository(db)

			q := "WEBDL-1080p"
			sz := int64(123456789)
			require.NoError(t, repo.Upsert(ctx, series.EpisodeState{
				InstanceName: "main",
				EpisodeID:    epID,
				Monitored:    true,
				HasFile:      true,
				Quality:      &q,
				SizeBytes:    &sz,
			}))

			got, err := repo.Get(ctx, "main", epID)
			require.NoError(t, err)
			assert.True(t, got.HasFile)
			assert.True(t, got.Monitored)
			require.NotNil(t, got.Quality)
			assert.Equal(t, "WEBDL-1080p", *got.Quality)
			assert.Equal(t, int64(123456789), *got.SizeBytes)
		})
	}
}

func TestEpisodeStatesRepository_Get_NotFound(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			repo := NewEpisodeStatesRepository(db)
			_, err := repo.Get(context.Background(), "main", 9999)
			require.Error(t, err)

			var typedErr *sharedErrors.EpisodeNotFoundError
			require.True(t, errors.As(err, &typedErr),
				"Get NotFound must expose typed EpisodeNotFoundError via errors.As")
			assert.Equal(t, domain.EpisodeID(9999), typedErr.ID)
		})
	}
}

func TestEpisodeStatesRepository_Upsert_Idempotent_PerInstance(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			ctx := context.Background()
			seriesID, err := NewSeriesRepository(db).Upsert(ctx, sampleCanon("Foundation"))
			require.NoError(t, err)
			epIDRaw, err := NewEpisodesRepository(db).Upsert(ctx, series.CanonEpisode{
				SeriesID: seriesID, SeasonNumber: 1, EpisodeNumber: 1,
			})
			require.NoError(t, err)
			epID := domain.EpisodeID(epIDRaw)
			repo := NewEpisodeStatesRepository(db)

			st := series.EpisodeState{InstanceName: "main", EpisodeID: epID, Monitored: true}
			require.NoError(t, repo.Upsert(ctx, st))
			require.NoError(t, repo.Upsert(ctx, st)) // idempotent

			// Same episode, different instance — independent row by PK shape.
			require.NoError(t, repo.Upsert(ctx, series.EpisodeState{
				InstanceName: "main-4k", EpisodeID: epID, Monitored: false, HasFile: true,
			}))

			got1, err := repo.Get(ctx, "main", epID)
			require.NoError(t, err)
			assert.True(t, got1.Monitored)
			got2, err := repo.Get(ctx, "main-4k", epID)
			require.NoError(t, err)
			assert.True(t, got2.HasFile)
		})
	}
}

func TestEpisodeStatesRepository_ListBySeries(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			ctx := context.Background()
			seriesID, err := NewSeriesRepository(db).Upsert(ctx, sampleCanon("Severance"))
			require.NoError(t, err)
			repoEp := NewEpisodesRepository(db)
			repoSt := NewEpisodeStatesRepository(db)
			for i := 1; i <= 3; i++ {
				epIDRaw, err := repoEp.Upsert(ctx, series.CanonEpisode{
					SeriesID: seriesID, SeasonNumber: 1, EpisodeNumber: i,
				})
				require.NoError(t, err)
				require.NoError(t, repoSt.Upsert(ctx, series.EpisodeState{
					InstanceName: "main", EpisodeID: domain.EpisodeID(epIDRaw), HasFile: i == 1,
				}))
			}
			rows, err := repoSt.ListBySeries(ctx, "main", seriesID)
			require.NoError(t, err)
			require.Len(t, rows, 3)
		})
	}
}

// Story 374: Upsert must clear deleted_at on conflict so a soft-deleted
// row is resurrected by the next scan tick. Before this fix the
// SoftDeleteBySeries cascade (story 218) left rows hidden forever
// because the DO UPDATE SET did not include deleted_at.
func TestEpisodeStatesRepository_Upsert_ResurrectsSoftDeleted(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			ctx := context.Background()

			sr := NewSeriesRepository(db)
			// Seed the cache row first; its resolveOrCreateCanon will pick or
			// create the canon series_id. We then look it up and seed the
			// episode against the same id so SoftDeleteBySeries' JOIN walks
			// episodes → series_cache through a real (cache.series_id =
			// episode.series_id) edge.
			scr := NewSeriesCacheRepository(db, sr)
			require.NoError(t, scr.Upsert(ctx, series.CacheEntry{
				InstanceName:   "main",
				SonarrSeriesID: 42,
				Title:          "X",
				TitleSlug:      "x",
			}))
			cached, err := scr.Get(ctx, "main", 42)
			require.NoError(t, err)
			require.NotNil(t, cached.SeriesID, "cache row must resolve to a canon series_id")

			epIDRaw, err := NewEpisodesRepository(db).Upsert(ctx, series.CanonEpisode{
				SeriesID: *cached.SeriesID, SeasonNumber: 1, EpisodeNumber: 1,
			})
			require.NoError(t, err)
			epID := domain.EpisodeID(epIDRaw)

			repo := NewEpisodeStatesRepository(db)
			st := series.EpisodeState{
				InstanceName: "main",
				EpisodeID:    epID,
				Monitored:    true,
				HasFile:      true,
			}
			require.NoError(t, repo.Upsert(ctx, st))
			_, err = repo.Get(ctx, "main", epID)
			require.NoError(t, err, "row should be visible after insert")

			n, err := repo.SoftDeleteBySeries(ctx, "main", 42)
			require.NoError(t, err)
			require.Equal(t, 1, n)
			_, err = repo.Get(ctx, "main", epID)
			require.Error(t, err, "row should be hidden after soft-delete")
			var softNF *sharedErrors.EpisodeNotFoundError
			require.True(t, errors.As(err, &softNF),
				"soft-delete must surface typed EpisodeNotFoundError via errors.As")

			// Story 374 fix: re-upserting must clear deleted_at.
			require.NoError(t, repo.Upsert(ctx, st))
			got, err := repo.Get(ctx, "main", epID)
			require.NoError(t, err, "row must be visible after resurrecting Upsert")
			require.True(t, got.HasFile)
		})
	}
}

func TestEpisodeStatesRepository_MediaMeta_RoundTrip(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			ctx := context.Background()
			seriesID, err := NewSeriesRepository(db).Upsert(ctx, sampleCanon("MediaMeta"))
			require.NoError(t, err)
			epIDRaw, err := NewEpisodesRepository(db).Upsert(ctx, series.CanonEpisode{
				SeriesID: seriesID, SeasonNumber: 5, EpisodeNumber: 1,
			})
			require.NoError(t, err)
			epID := domain.EpisodeID(epIDRaw)
			repo := NewEpisodeStatesRepository(db)

			vc := "HEVC"
			ac := "DDP"
			ach := "5.1"
			rg := "RARBG"
			st := series.EpisodeState{
				InstanceName:  "main",
				EpisodeID:     epID,
				Monitored:     true,
				HasFile:       true,
				VideoCodec:    &vc,
				AudioCodec:    &ac,
				AudioChannels: &ach,
				ReleaseGroup:  &rg,
			}
			require.NoError(t, repo.Upsert(ctx, st))

			got, err := repo.Get(ctx, "main", epID)
			require.NoError(t, err)
			require.Equal(t, &vc, got.VideoCodec)
			require.Equal(t, &ac, got.AudioCodec)
			require.Equal(t, &ach, got.AudioChannels)
			require.Equal(t, &rg, got.ReleaseGroup)
		})
	}
}
