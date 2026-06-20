package repositories

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/internal/shared/domain"
	"github.com/alexmorbo/seasonfill/internal/shared/testhelpers"
)

func TestRecommendationsRepository_Set_ReplacesAndIdempotent(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			ctx := context.Background()
			seriesRepo := NewSeriesRepository(db)
			repo := NewRecommendationsRepository(db)

			source, err := seriesRepo.Upsert(ctx, sampleCanon("The Last of Us"))
			require.NoError(t, err)

			// Three stub-recommendation series.
			recIDs := make([]domain.SeriesID, 0, 3)
			for i, title := range []string{"Fallout", "The Walking Dead", "Station Eleven"} {
				c := sampleCanon(title)
				c.TMDBID = ptrTMDBID(70000 + i)
				rid, err := seriesRepo.Upsert(ctx, c)
				require.NoError(t, err)
				recIDs = append(recIDs, rid)
			}

			require.NoError(t, repo.Set(ctx, source, recIDs))
			rows, err := repo.ListBySeries(ctx, source)
			require.NoError(t, err)
			assert.Equal(t, recIDs, rows, "position preserved by input order")

			// Re-Set with same ids — idempotent.
			require.NoError(t, repo.Set(ctx, source, recIDs))
			rows, err = repo.ListBySeries(ctx, source)
			require.NoError(t, err)
			assert.Equal(t, recIDs, rows)

			// Re-Set with subset — orphans removed.
			require.NoError(t, repo.Set(ctx, source, recIDs[:1]))
			rows, err = repo.ListBySeries(ctx, source)
			require.NoError(t, err)
			assert.Equal(t, recIDs[:1], rows)

			// Empty set clears.
			require.NoError(t, repo.Set(ctx, source, nil))
			rows, err = repo.ListBySeries(ctx, source)
			require.NoError(t, err)
			assert.Empty(t, rows)
		})
	}
}

func TestRecommendationsRepository_Set_RejectsSelfReference(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			ctx := context.Background()
			seriesID, err := NewSeriesRepository(db).Upsert(ctx, sampleCanon("Severance"))
			require.NoError(t, err)
			repo := NewRecommendationsRepository(db)

			err = repo.Set(ctx, seriesID, []domain.SeriesID{seriesID})
			require.Error(t, err, "Set must reject self-reference")
		})
	}
}

func TestRecommendationsRepository_Upsert_SingleRow(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			ctx := context.Background()
			seriesRepo := NewSeriesRepository(db)
			repo := NewRecommendationsRepository(db)

			src := sampleCanon("Foundation")
			src.TMDBID = ptrTMDBID(90001)
			source, err := seriesRepo.Upsert(ctx, src)
			require.NoError(t, err)
			recCanon := sampleCanon("Severance")
			recCanon.TMDBID = ptrTMDBID(90002)
			rec, err := seriesRepo.Upsert(ctx, recCanon)
			require.NoError(t, err)

			pos := 0
			row := SeriesRecommendation{
				SeriesID:            source,
				RecommendedSeriesID: rec,
				Position:            &pos,
			}
			require.NoError(t, repo.Upsert(ctx, row))
			// Idempotent.
			require.NoError(t, repo.Upsert(ctx, row))

			rows, err := repo.ListBySeries(ctx, source)
			require.NoError(t, err)
			require.Len(t, rows, 1)
			assert.Equal(t, rec, rows[0])
		})
	}
}

func TestRecommendationsRepository_Set_RejectsSelfReferenceInBatch(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			ctx := context.Background()
			seriesRepo := NewSeriesRepository(db)
			repo := NewRecommendationsRepository(db)

			source, err := seriesRepo.Upsert(ctx, sampleCanon("Source"))
			require.NoError(t, err)

			otherIDs := make([]domain.SeriesID, 0, 2)
			for i, title := range []string{"Other A", "Other B"} {
				c := sampleCanon(title)
				c.TMDBID = ptrTMDBID(80000 + i)
				oid, err := seriesRepo.Upsert(ctx, c)
				require.NoError(t, err)
				otherIDs = append(otherIDs, oid)
			}

			// Inject the source id into the batch — must reject.
			bad := append([]domain.SeriesID{source}, otherIDs...)
			require.Error(t, repo.Set(ctx, source, bad),
				"Set must reject a batch that contains the source id")

			// Confirm nothing was inserted (transaction rolled back).
			rows, err := repo.ListBySeries(ctx, source)
			require.NoError(t, err)
			assert.Empty(t, rows, fmt.Sprintf("expected zero rows after rollback, got %d", len(rows)))
		})
	}
}
