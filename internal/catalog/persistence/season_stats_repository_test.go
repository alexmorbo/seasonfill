package persistence

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/internal/catalog/domain/series"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
	"github.com/alexmorbo/seasonfill/internal/shared/testhelpers"
)

func sampleSeasonStat(instance domain.InstanceName, sonarrID domain.SonarrSeriesID, seasonNumber int) series.SeasonStat {
	return series.SeasonStat{
		InstanceName:      instance,
		SonarrSeriesID:    sonarrID,
		SeasonNumber:      seasonNumber,
		EpisodeCount:      10,
		EpisodeFileCount:  10,
		TotalEpisodeCount: 10,
		AiredEpisodeCount: 10,
		Monitored:         true,
		SizeOnDiskBytes:   42_300_000_000,
	}
}

func TestSeasonStatsRepository_UpsertAndList(t *testing.T) {
	t.Skip("pending D-4 catalog rewrite (D2-revised-roadmap.md)")
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			repo := NewSeasonStatsRepository(db)
			ctx := context.Background()

			require.NoError(t, repo.Upsert(ctx, sampleSeasonStat("homelab", 140, 1)))
			require.NoError(t, repo.Upsert(ctx, sampleSeasonStat("homelab", 140, 2)))
			require.NoError(t, repo.Upsert(ctx, sampleSeasonStat("homelab", 369, 1)))

			got, err := repo.ListBySeries(ctx, "homelab", 140)
			require.NoError(t, err)
			require.Len(t, got, 2)
			assert.Equal(t, 1, got[0].SeasonNumber)
			assert.Equal(t, 2, got[1].SeasonNumber)
			assert.Equal(t, 10, got[0].EpisodeFileCount)
			assert.Equal(t, int64(42_300_000_000), got[0].SizeOnDiskBytes)
		})
	}
}

func TestSeasonStatsRepository_Upsert_OverwritesCounters(t *testing.T) {
	t.Skip("pending D-4 catalog rewrite (D2-revised-roadmap.md)")
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			repo := NewSeasonStatsRepository(db)
			ctx := context.Background()

			first := sampleSeasonStat("homelab", 140, 1)
			first.EpisodeFileCount = 5
			first.SizeOnDiskBytes = 5_000_000_000
			require.NoError(t, repo.Upsert(ctx, first))

			second := sampleSeasonStat("homelab", 140, 1)
			second.EpisodeFileCount = 11
			second.SizeOnDiskBytes = 11_000_000_000
			require.NoError(t, repo.Upsert(ctx, second))

			got, err := repo.ListBySeries(ctx, "homelab", 140)
			require.NoError(t, err)
			require.Len(t, got, 1)
			assert.Equal(t, 11, got[0].EpisodeFileCount)
			assert.Equal(t, int64(11_000_000_000), got[0].SizeOnDiskBytes)
		})
	}
}

func TestSeasonStatsRepository_SoftDelete_HidesRows(t *testing.T) {
	t.Skip("pending D-4 catalog rewrite (D2-revised-roadmap.md)")
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			repo := NewSeasonStatsRepository(db)
			ctx := context.Background()

			require.NoError(t, repo.Upsert(ctx, sampleSeasonStat("homelab", 140, 1)))
			require.NoError(t, repo.Upsert(ctx, sampleSeasonStat("homelab", 140, 2)))

			n, err := repo.SoftDeleteBySeries(ctx, "homelab", 140)
			require.NoError(t, err)
			assert.Equal(t, 2, n)

			got, err := repo.ListBySeries(ctx, "homelab", 140)
			require.NoError(t, err)
			assert.Empty(t, got, "soft-deleted rows must not surface in List")
		})
	}
}

func TestSeasonStatsRepository_Upsert_ResurrectsSoftDeleted(t *testing.T) {
	t.Skip("pending D-4 catalog rewrite (D2-revised-roadmap.md)")
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			repo := NewSeasonStatsRepository(db)
			ctx := context.Background()

			require.NoError(t, repo.Upsert(ctx, sampleSeasonStat("homelab", 140, 1)))
			n, err := repo.SoftDeleteBySeries(ctx, "homelab", 140)
			require.NoError(t, err)
			require.Equal(t, 1, n)

			revived := sampleSeasonStat("homelab", 140, 1)
			revived.EpisodeFileCount = 9
			require.NoError(t, repo.Upsert(ctx, revived))

			got, err := repo.ListBySeries(ctx, "homelab", 140)
			require.NoError(t, err)
			require.Len(t, got, 1, "Upsert must clear deleted_at on conflict")
			assert.Equal(t, 9, got[0].EpisodeFileCount)
		})
	}
}

func TestSeasonStatsRepository_ListBySeries_FiltersByInstance(t *testing.T) {
	t.Skip("pending D-4 catalog rewrite (D2-revised-roadmap.md)")
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			repo := NewSeasonStatsRepository(db)
			ctx := context.Background()

			require.NoError(t, repo.Upsert(ctx, sampleSeasonStat("homelab", 140, 1)))
			require.NoError(t, repo.Upsert(ctx, sampleSeasonStat("other", 140, 1)))

			homelab, err := repo.ListBySeries(ctx, "homelab", 140)
			require.NoError(t, err)
			require.Len(t, homelab, 1)
			assert.Equal(t, domain.InstanceName("homelab"), homelab[0].InstanceName)

			other, err := repo.ListBySeries(ctx, "other", 140)
			require.NoError(t, err)
			require.Len(t, other, 1)
			assert.Equal(t, domain.InstanceName("other"), other[0].InstanceName)
		})
	}
}

func TestSeasonStatsRepository_Upsert_StampsUpdatedAt(t *testing.T) {
	t.Skip("pending D-4 catalog rewrite (D2-revised-roadmap.md)")
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			repo := NewSeasonStatsRepository(db)
			ctx := context.Background()

			before := time.Now().UTC().Add(-time.Minute)
			require.NoError(t, repo.Upsert(ctx, sampleSeasonStat("homelab", 140, 1)))
			got, err := repo.ListBySeries(ctx, "homelab", 140)
			require.NoError(t, err)
			require.Len(t, got, 1)
			assert.True(t, got[0].UpdatedAt.After(before), "updated_at must be stamped server-side")
		})
	}
}
