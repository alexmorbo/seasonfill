package persistence

import (
	"context"
	"fmt"
	"math/rand"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"github.com/alexmorbo/seasonfill/internal/catalog/domain/series"
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

// TestRecommendationsRepository_BatchUpsert_NoDeadlockUnderConcurrency
// — B-26 regression. Recommendation stubs are upserted into the
// `series` table via SeriesRepository.UpsertStub (partial unique on
// tmdb_id). Two parallel series_worker txes hitting overlapping
// recommended_series produce SQLSTATE 40P01
// (`upsert recommendation stub: upsert stub series: deadlock detected`
// in 2026-06-22 audit). The contract: sort recommendation stubs by
// tmdb_id ASC before UpsertStub loop — same discipline as People/Genres.
// Postgres-only. Despite the BatchUpsert suffix, this exercises single-
// row UpsertStub concurrently — the contract under test is call-site
// ordering, not a batch API.
func TestRecommendationsRepository_BatchUpsert_NoDeadlockUnderConcurrency(t *testing.T) {
	t.Parallel()

	sawPostgres := false
	for _, backend := range testhelpers.AllBackends(t) {
		if backend.Name != "postgres" {
			continue
		}
		sawPostgres = true
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)

			const N = 4
			const iters = 30
			tmdbIDs := []int64{70001, 70002, 70003, 70004, 70005, 70006, 70007, 70008}

			var wg sync.WaitGroup
			errCh := make(chan error, N*iters)
			for w := range N {
				wg.Add(1)
				go func(workerSeed int) {
					defer wg.Done()
					rng := rand.New(rand.NewSource(int64(workerSeed) * 7919))
					for i := range iters {
						ids := append([]int64(nil), tmdbIDs...)
						rng.Shuffle(len(ids), func(a, b int) { ids[a], ids[b] = ids[b], ids[a] })

						// The contract: sort by tmdb_id ASC before the
						// per-row UpsertStub loop.
						slices.Sort(ids)

						err := db.Transaction(func(tx *gorm.DB) error {
							repo := NewSeriesRepository(tx)
							for _, id := range ids {
								tid := domain.TMDBID(id)
								c := series.Canon{
									OriginalTitle: new(fmt.Sprintf("Test Recommendation %d", id)),
									Hydration:     series.HydrationStub,
									TMDBID:        &tid,
								}
								if _, err := repo.UpsertStub(context.Background(), c); err != nil {
									return err
								}
							}
							return nil
						})
						if err != nil {
							errCh <- fmt.Errorf("worker %d iter %d: %w", workerSeed, i, err)
							return
						}
					}
				}(w)
			}
			wg.Wait()
			close(errCh)

			var failures []string
			for err := range errCh {
				failures = append(failures, err.Error())
			}
			require.Empty(t, failures,
				"no SQLSTATE 40P01 (deadlock) errors expected after sort discipline; got:\n%s",
				strings.Join(failures, "\n"))
		})
	}
	if !sawPostgres {
		t.Log("postgres backend not available; deadlock repro skipped")
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
