package repositories

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/application/ports"
	"github.com/alexmorbo/seasonfill/internal/enrichment/domain/taxonomy"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
	"github.com/alexmorbo/seasonfill/internal/shared/testhelpers"
)

func sampleNetwork(name string, tmdbID int) taxonomy.Network {
	return taxonomy.Network{
		Name:          name,
		TMDBID:        ptrTMDBID(tmdbID),
		OriginCountry: new("US"),
	}
}

func TestNetworksRepository_UpsertInsertAndGet(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			repo := NewNetworksRepository(db)
			ctx := context.Background()

			id, err := repo.Upsert(ctx, sampleNetwork("Netflix", 213))
			require.NoError(t, err)
			require.NotZero(t, id)

			got, err := repo.Get(ctx, id)
			require.NoError(t, err)
			assert.Equal(t, "Netflix", got.Name)
			require.NotNil(t, got.TMDBID)
			assert.Equal(t, domain.TMDBID(213), *got.TMDBID)
		})
	}
}

func TestNetworksRepository_Get_NotFound(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			repo := NewNetworksRepository(db)
			_, err := repo.Get(context.Background(), 9999)
			require.True(t, errors.Is(err, ports.ErrNotFound))
		})
	}
}

func TestNetworksRepository_Upsert_Idempotent(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			repo := NewNetworksRepository(db)
			ctx := context.Background()

			first := sampleNetwork("HBO", 49)
			id1, err := repo.Upsert(ctx, first)
			require.NoError(t, err)
			id2, err := repo.Upsert(ctx, first)
			require.NoError(t, err)
			assert.Equal(t, id1, id2, "natural-key upsert must resolve to the same id")
		})
	}
}

func TestNetworksRepository_PartialUnique_AllowsNullTMDB(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			repo := NewNetworksRepository(db)
			ctx := context.Background()

			// Sonarr-fallback path — name from a string, no tmdb_id.
			orphanA := taxonomy.Network{Name: "Local Channel A"}
			orphanA.TMDBID = nil
			id1, err := repo.Upsert(ctx, orphanA)
			require.NoError(t, err)

			orphanB := taxonomy.Network{Name: "Local Channel B"}
			orphanB.TMDBID = nil
			id2, err := repo.Upsert(ctx, orphanB)
			require.NoError(t, err)
			assert.NotEqual(t, id1, id2,
				"two NULL-tmdb networks must coexist — partial unique excludes them")
		})
	}
}

func TestNetworksRepository_Set_ReplacesAndIdempotent(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			repo := NewNetworksRepository(db)
			ctx := context.Background()

			seriesID, err := NewSeriesRepository(db).Upsert(ctx, sampleCanon("Stranger Things"))
			require.NoError(t, err)
			nID1, err := repo.Upsert(ctx, sampleNetwork("Netflix", 213))
			require.NoError(t, err)
			nID2, err := repo.Upsert(ctx, sampleNetwork("Channel 4", 30))
			require.NoError(t, err)

			require.NoError(t, repo.Set(ctx, seriesID, []int64{nID1, nID2}))
			rows, err := repo.ListBySeries(ctx, seriesID)
			require.NoError(t, err)
			require.Equal(t, []int64{nID1, nID2}, rows, "position preserved by input order")

			// Re-Set with same ids — idempotent.
			require.NoError(t, repo.Set(ctx, seriesID, []int64{nID1, nID2}))
			rows, err = repo.ListBySeries(ctx, seriesID)
			require.NoError(t, err)
			require.Equal(t, []int64{nID1, nID2}, rows)

			// Re-Set with different ids — replaces fully (no orphans).
			require.NoError(t, repo.Set(ctx, seriesID, []int64{nID2}))
			rows, err = repo.ListBySeries(ctx, seriesID)
			require.NoError(t, err)
			require.Equal(t, []int64{nID2}, rows,
				"Set replaces the full set — previous ids must be gone")

			// Empty set clears.
			require.NoError(t, repo.Set(ctx, seriesID, nil))
			rows, err = repo.ListBySeries(ctx, seriesID)
			require.NoError(t, err)
			assert.Empty(t, rows)
		})
	}
}
