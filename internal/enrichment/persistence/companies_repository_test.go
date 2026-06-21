package persistence

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/internal/enrichment/domain/taxonomy"
	ports "github.com/alexmorbo/seasonfill/internal/shared/dataports"
	"github.com/alexmorbo/seasonfill/internal/shared/testhelpers"
)

func sampleCompany(name string, tmdbID int) taxonomy.ProductionCompany {
	return taxonomy.ProductionCompany{
		Name:          name,
		TMDBID:        ptrTMDBID(tmdbID),
		OriginCountry: new("US"),
	}
}

func TestCompaniesRepository_UpsertAndGet(t *testing.T) {

	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			repo := NewCompaniesRepository(db)
			ctx := context.Background()

			id, err := repo.Upsert(ctx, sampleCompany("Bad Robot", 11461))
			require.NoError(t, err)

			got, err := repo.Get(ctx, id)
			require.NoError(t, err)
			assert.Equal(t, "Bad Robot", got.Name)
		})
	}
}

func TestCompaniesRepository_Get_NotFound(t *testing.T) {

	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			repo := NewCompaniesRepository(db)
			_, err := repo.Get(context.Background(), 9999)
			require.True(t, errors.Is(err, ports.ErrNotFound))
		})
	}
}

func TestCompaniesRepository_Upsert_Idempotent(t *testing.T) {

	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			repo := NewCompaniesRepository(db)
			ctx := context.Background()

			first := sampleCompany("Mediawan", 7100)
			id1, err := repo.Upsert(ctx, first)
			require.NoError(t, err)
			id2, err := repo.Upsert(ctx, first)
			require.NoError(t, err)
			assert.Equal(t, id1, id2)
		})
	}
}

// TestCompaniesRepository_Upsert_OrphanBranch covers the no-PK +
// no-natural-key path (story 424a). Pre-fix this branch emitted a bare
// `ON CONFLICT DO UPDATE` which SQLite tolerated but Postgres rejected
// with SQLSTATE 42601. Two NULL-tmdb_id rows MUST coexist (partial
// unique index excludes them), proving the default branch issues a
// pure INSERT.
func TestCompaniesRepository_Upsert_OrphanBranch(t *testing.T) {

	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			repo := NewCompaniesRepository(db)
			ctx := context.Background()

			a := sampleCompany("Orphan Studios A", 0)
			a.TMDBID = nil
			id1, err := repo.Upsert(ctx, a)
			require.NoError(t, err, "default branch must issue a pure INSERT")
			require.NotZero(t, id1)

			b := sampleCompany("Orphan Studios B", 0)
			b.TMDBID = nil
			id2, err := repo.Upsert(ctx, b)
			require.NoError(t, err)
			assert.NotEqual(t, id1, id2,
				"two NULL-tmdb_id rows must coexist — partial unique excludes them")
		})
	}
}

func TestCompaniesRepository_Set_ReplacesAndIdempotent(t *testing.T) {

	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			repo := NewCompaniesRepository(db)
			ctx := context.Background()

			seriesID, err := NewSeriesRepository(db).Upsert(ctx, sampleCanon("Severance"))
			require.NoError(t, err)
			cID1, err := repo.Upsert(ctx, sampleCompany("Endeavor Content", 4111))
			require.NoError(t, err)
			cID2, err := repo.Upsert(ctx, sampleCompany("Red Hour Productions", 9999))
			require.NoError(t, err)

			require.NoError(t, repo.Set(ctx, seriesID, []int64{cID1, cID2}))
			require.NoError(t, repo.Set(ctx, seriesID, []int64{cID1, cID2}))
			rows, err := repo.ListBySeries(ctx, seriesID)
			require.NoError(t, err)
			assert.Equal(t, []int64{cID1, cID2}, rows,
				"idempotent re-Set must produce no row delta")
		})
	}
}
