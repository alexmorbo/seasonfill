package persistence

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"github.com/alexmorbo/seasonfill/internal/enrichment/domain/taxonomy"
	ports "github.com/alexmorbo/seasonfill/internal/shared/dataports"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
	"github.com/alexmorbo/seasonfill/internal/shared/testhelpers"
)

func TestGenresRepository_UpsertAndGet(t *testing.T) {

	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			ctx := context.Background()
			repo := NewGenresRepository(db)
			i18n := NewGenresI18nRepository(db)

			id, err := repo.Upsert(ctx, taxonomy.Genre{TMDBID: ptrTMDBID(18)})
			require.NoError(t, err)
			require.NotZero(t, id)

			require.NoError(t, i18n.Upsert(ctx, taxonomy.GenreI18n{
				GenreID: id, Language: "en-US", Name: "Drama",
			}))

			got, err := repo.Get(ctx, id, "en-US")
			require.NoError(t, err)
			assert.Equal(t, "Drama", got.Name)
			assert.Equal(t, "en-US", got.Language)
		})
	}
}

func TestGenresRepository_Get_NotFound(t *testing.T) {

	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			repo := NewGenresRepository(db)
			_, err := repo.Get(context.Background(), 9999, "en-US")
			require.True(t, errors.Is(err, ports.ErrNotFound))
		})
	}
}

func TestGenresRepository_Upsert_Idempotent(t *testing.T) {

	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			ctx := context.Background()
			repo := NewGenresRepository(db)

			g := taxonomy.Genre{TMDBID: ptrTMDBID(35)}
			id1, err := repo.Upsert(ctx, g)
			require.NoError(t, err)
			id2, err := repo.Upsert(ctx, g)
			require.NoError(t, err)
			assert.Equal(t, id1, id2, "natural-key upsert must resolve to the same id")
		})
	}
}

// TestGenresRepository_ResolveByName_PRD54Fallback covers the PRD §5.4
// Sonarr-genre fallback contract: an en-US name resolves to the same
// canonical genres.id as a TMDB-sourced upsert.
func TestGenresRepository_ResolveByName_PRD54Fallback(t *testing.T) {

	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			ctx := context.Background()
			repo := NewGenresRepository(db)
			i18n := NewGenresI18nRepository(db)

			// Simulate the C-2 enrichment path: TMDB upserts genre id=18 with
			// en-US name "Drama".
			id, err := repo.Upsert(ctx, taxonomy.Genre{TMDBID: ptrTMDBID(18)})
			require.NoError(t, err)
			require.NoError(t, i18n.Upsert(ctx, taxonomy.GenreI18n{
				GenreID: id, Language: "en-US", Name: "Drama",
			}))

			// E-1 (future) writes a Sonarr-grabbed series whose genres list
			// contains the string "Drama". It calls ResolveByName, which MUST
			// return the same canonical id.
			resolved, err := repo.ResolveByName(ctx, "en-US", "Drama")
			require.NoError(t, err)
			assert.Equal(t, id, resolved,
				"PRD §5.4 fallback: Sonarr-genre string must resolve to the canonical TMDB-sourced row")

			// Negative case — unknown name returns ErrNotFound.
			_, err = repo.ResolveByName(ctx, "en-US", "Nonexistent")
			require.True(t, errors.Is(err, ports.ErrNotFound))

			// Negative case — wrong language returns ErrNotFound (v1 case-
			// sensitive, language-exact match).
			_, err = repo.ResolveByName(ctx, "ru-RU", "Drama")
			require.True(t, errors.Is(err, ports.ErrNotFound),
				"v1 ResolveByName is language-exact; no fallback on the resolve path")
		})
	}
}

func TestGenresRepository_Get_FallbackToEnUS(t *testing.T) {

	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			ctx := context.Background()
			repo := NewGenresRepository(db)
			i18n := NewGenresI18nRepository(db)

			id, err := repo.Upsert(ctx, taxonomy.Genre{TMDBID: ptrTMDBID(10765)})
			require.NoError(t, err)
			require.NoError(t, i18n.Upsert(ctx, taxonomy.GenreI18n{
				GenreID: id, Language: "en-US", Name: "Sci-Fi & Fantasy",
			}))

			// Request ru-RU — only en-US exists; helper falls back.
			got, err := repo.Get(ctx, id, "ru-RU")
			require.NoError(t, err)
			assert.Equal(t, "en-US", got.Language)
			assert.Equal(t, "Sci-Fi & Fantasy", got.Name)
		})
	}
}

func TestGenresRepository_Get_NoI18nRows(t *testing.T) {

	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			ctx := context.Background()
			repo := NewGenresRepository(db)

			// Bare genre stub with no i18n rows — Get returns the row with
			// empty Name / Language (NOT an error).
			id, err := repo.Upsert(ctx, taxonomy.Genre{TMDBID: ptrTMDBID(99)})
			require.NoError(t, err)

			got, err := repo.Get(ctx, id, "en-US")
			require.NoError(t, err)
			assert.Empty(t, got.Name)
			assert.Empty(t, got.Language)
		})
	}
}

// TestGenresRepository_Upsert_OrphanBranch covers the no-PK +
// no-natural-key path (story 424a). Pre-fix this branch emitted a bare
// `ON CONFLICT DO UPDATE` which SQLite tolerated but Postgres rejected
// with SQLSTATE 42601. Sonarr-fallback genre upserts may carry a NULL
// tmdb_id when the Sonarr API hasn't joined a TMDB id yet — two such
// rows MUST coexist (partial unique excludes NULL).
func TestGenresRepository_Upsert_OrphanBranch(t *testing.T) {

	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			repo := NewGenresRepository(db)
			ctx := context.Background()

			id1, err := repo.Upsert(ctx, taxonomy.Genre{TMDBID: nil})
			require.NoError(t, err, "default branch must issue a pure INSERT")
			require.NotZero(t, id1)

			id2, err := repo.Upsert(ctx, taxonomy.Genre{TMDBID: nil})
			require.NoError(t, err)
			assert.NotEqual(t, id1, id2,
				"two NULL-tmdb_id rows must coexist — partial unique excludes them")
		})
	}
}

// TestGenresRepository_BatchUpsert_NoDeadlockUnderConcurrency — B-26
// regression. Same shape as the People test; the contract is identical
// (sort by tmdb_id ASC before per-row Upsert loop). Postgres-only.
// Despite the BatchUpsert suffix, this exercises single-row Upsert
// concurrently — the contract under test is call-site ordering.
func TestGenresRepository_BatchUpsert_NoDeadlockUnderConcurrency(t *testing.T) {
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
			// 8 genre tmdb_ids — TMDB has 16 TV genres so 8 is a realistic
			// hot-overlap surface.
			tmdbIDs := []int64{18, 80, 99, 18000, 35, 9648, 10765, 10759}

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
						// per-row Upsert loop.
						slices.Sort(ids)

						err := db.Transaction(func(tx *gorm.DB) error {
							repo := NewGenresRepository(tx)
							for _, id := range ids {
								tid := domain.TMDBID(id)
								g := taxonomy.Genre{TMDBID: &tid}
								if _, err := repo.Upsert(context.Background(), g); err != nil {
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

func TestGenresRepository_Set_ReplacesAndIdempotent(t *testing.T) {

	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			ctx := context.Background()
			repo := NewGenresRepository(db)

			seriesID, err := NewSeriesRepository(db).Upsert(ctx, sampleCanon("Foundation"))
			require.NoError(t, err)
			gID1, err := repo.Upsert(ctx, taxonomy.Genre{TMDBID: ptrTMDBID(18)})
			require.NoError(t, err)
			gID2, err := repo.Upsert(ctx, taxonomy.Genre{TMDBID: ptrTMDBID(10765)})
			require.NoError(t, err)

			require.NoError(t, repo.Set(ctx, seriesID, []int64{gID1, gID2}))
			require.NoError(t, repo.Set(ctx, seriesID, []int64{gID1, gID2}))
			rows, err := repo.ListBySeries(ctx, seriesID)
			require.NoError(t, err)
			assert.Equal(t, []int64{gID1, gID2}, rows)
		})
	}
}

// Story 552 (E-1 Z3) — batched ListByIDsWithFallback tests.

func TestGenresRepository_ListByIDsWithFallback_LangPreferred(t *testing.T) {

	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			ctx := context.Background()
			repo := NewGenresRepository(db)
			i18n := NewGenresI18nRepository(db)

			idA, err := repo.Upsert(ctx, taxonomy.Genre{TMDBID: ptrTMDBID(18)})
			require.NoError(t, err)
			idB, err := repo.Upsert(ctx, taxonomy.Genre{TMDBID: ptrTMDBID(35)})
			require.NoError(t, err)

			require.NoError(t, i18n.Upsert(ctx, taxonomy.GenreI18n{
				GenreID: idA, Language: "en-US", Name: "Drama",
			}))
			require.NoError(t, i18n.Upsert(ctx, taxonomy.GenreI18n{
				GenreID: idA, Language: "ru-RU", Name: "Драма",
			}))
			// idB has en-US only — exercises the fill-in pass.
			require.NoError(t, i18n.Upsert(ctx, taxonomy.GenreI18n{
				GenreID: idB, Language: "en-US", Name: "Comedy",
			}))

			got, err := repo.ListByIDsWithFallback(ctx, []int64{idA, idB}, "ru-RU")
			require.NoError(t, err)
			require.Len(t, got, 2)

			byID := map[int64]taxonomy.Genre{got[0].ID: got[0], got[1].ID: got[1]}
			assert.Equal(t, "Драма", byID[idA].Name)
			assert.Equal(t, "ru-RU", byID[idA].Language)
			assert.Equal(t, "Comedy", byID[idB].Name)
			assert.Equal(t, "en-US", byID[idB].Language)
		})
	}
}

func TestGenresRepository_ListByIDsWithFallback_MissingIdDropped(t *testing.T) {

	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			ctx := context.Background()
			repo := NewGenresRepository(db)
			i18n := NewGenresI18nRepository(db)

			id, err := repo.Upsert(ctx, taxonomy.Genre{TMDBID: ptrTMDBID(99)})
			require.NoError(t, err)
			require.NoError(t, i18n.Upsert(ctx, taxonomy.GenreI18n{
				GenreID: id, Language: "en-US", Name: "Real",
			}))

			// Mix a non-existent id with a real one — silently dropped, no error.
			got, err := repo.ListByIDsWithFallback(ctx, []int64{id, 999999}, "en-US")
			require.NoError(t, err)
			require.Len(t, got, 1)
			assert.Equal(t, id, got[0].ID)
			assert.Equal(t, "Real", got[0].Name)
		})
	}
}

func TestGenresRepository_ListByIDsWithFallback_EmptyInput(t *testing.T) {

	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			ctx := context.Background()
			repo := NewGenresRepository(db)

			got, err := repo.ListByIDsWithFallback(ctx, nil, "en-US")
			require.NoError(t, err)
			assert.Nil(t, got)
		})
	}
}

func TestGenresRepository_ListByIDsWithFallback_NoI18nRows(t *testing.T) {

	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			ctx := context.Background()
			repo := NewGenresRepository(db)

			id, err := repo.Upsert(ctx, taxonomy.Genre{TMDBID: ptrTMDBID(42)})
			require.NoError(t, err)

			got, err := repo.ListByIDsWithFallback(ctx, []int64{id}, "en-US")
			require.NoError(t, err)
			require.Len(t, got, 1)
			assert.Equal(t, id, got[0].ID)
			assert.Empty(t, got[0].Name)
			assert.Empty(t, got[0].Language)
		})
	}
}

func TestGenresRepository_ListByIDsWithFallback_LangIsEnUS_OneRoundTrip(t *testing.T) {

	// Implicit invariant: when lang == en-US, the en-US fill-in pass
	// is short-circuited. We assert it ships byte-equal to the
	// requested-lang-is-en-US path by feeding en-US-only data and
	// verifying no spurious behaviour.
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			ctx := context.Background()
			repo := NewGenresRepository(db)
			i18n := NewGenresI18nRepository(db)

			id, err := repo.Upsert(ctx, taxonomy.Genre{TMDBID: ptrTMDBID(7)})
			require.NoError(t, err)
			require.NoError(t, i18n.Upsert(ctx, taxonomy.GenreI18n{
				GenreID: id, Language: "en-US", Name: "Action",
			}))

			got, err := repo.ListByIDsWithFallback(ctx, []int64{id}, "en-US")
			require.NoError(t, err)
			require.Len(t, got, 1)
			assert.Equal(t, "Action", got[0].Name)
			assert.Equal(t, "en-US", got[0].Language)
		})
	}
}
