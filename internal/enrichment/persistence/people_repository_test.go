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

	"github.com/alexmorbo/seasonfill/internal/enrichment/domain/people"
	ports "github.com/alexmorbo/seasonfill/internal/shared/dataports"
	database "github.com/alexmorbo/seasonfill/internal/shared/db"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
	"github.com/alexmorbo/seasonfill/internal/shared/testhelpers"
)

func samplePerson(name string) people.Person {
	return people.Person{
		Name:               name,
		Hydration:          people.HydrationStub,
		TMDBID:             ptrTMDBID(7001),
		IMDBID:             new("nm0000001"),
		OriginalName:       new("orig: " + name),
		Gender:             new(2),
		KnownForDepartment: new("Acting"),
		Popularity:         new(12.5),
	}
}

func TestPeopleRepository_UpsertInsertAndGet(t *testing.T) {

	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			repo := NewPeopleRepository(db)
			ctx := context.Background()

			id, err := repo.Upsert(ctx, samplePerson("Pedro Pascal"))
			require.NoError(t, err)
			require.NotZero(t, id)

			got, err := repo.Get(ctx, id, "en-US")
			require.NoError(t, err)
			// Story 1084: Get resolves the DISPLAY name via COALESCE
			// (people_texts[req]→[en]→original_name→name). With no people_texts
			// row seeded, it falls back to original_name ("orig: Pedro Pascal").
			assert.Equal(t, "orig: Pedro Pascal", got.Name)
			assert.Equal(t, people.HydrationStub, got.Hydration)
			require.NotNil(t, got.TMDBID)
			assert.Equal(t, domain.TMDBID(7001), *got.TMDBID)
			assert.Empty(t, got.Biography)
			assert.Empty(t, got.BiographyLanguage)
		})
	}
}

func TestPeopleRepository_Get_NotFound(t *testing.T) {

	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			repo := NewPeopleRepository(db)
			_, err := repo.Get(context.Background(), 9999, "en-US")
			require.True(t, errors.Is(err, ports.ErrNotFound))
		})
	}
}

func TestPeopleRepository_Upsert_Idempotent(t *testing.T) {

	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			repo := NewPeopleRepository(db)
			ctx := context.Background()

			first := samplePerson("Florence Pugh")
			id1, err := repo.Upsert(ctx, first)
			require.NoError(t, err)
			got1, err := repo.Get(ctx, id1, "en-US")
			require.NoError(t, err)

			id2, err := repo.Upsert(ctx, first)
			require.NoError(t, err)
			assert.Equal(t, id1, id2, "natural-key upsert must resolve to the same id")

			got2, err := repo.Get(ctx, id2, "en-US")
			require.NoError(t, err)
			assert.Equal(t, got1.Name, got2.Name)
			assert.Equal(t, got1.CreatedAt.Unix(), got2.CreatedAt.Unix(),
				"created_at must NOT shift on a no-op upsert")
			assert.True(t, !got2.UpdatedAt.Before(got1.UpdatedAt))
		})
	}
}

func TestPeopleRepository_GetByTMDBID(t *testing.T) {

	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			repo := NewPeopleRepository(db)
			ctx := context.Background()

			_, err := repo.Upsert(ctx, samplePerson("Cillian Murphy"))
			require.NoError(t, err)

			got, err := repo.GetByTMDBID(ctx, 7001)
			require.NoError(t, err)
			assert.Equal(t, "Cillian Murphy", got.Name)

			_, err = repo.GetByTMDBID(ctx, 9999)
			assert.True(t, errors.Is(err, ports.ErrNotFound))
		})
	}
}

func TestPeopleRepository_ListByIDs(t *testing.T) {

	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			repo := NewPeopleRepository(db)
			ctx := context.Background()

			a := samplePerson("Actor A")
			a.TMDBID = ptrTMDBID(1001)
			id1, err := repo.Upsert(ctx, a)
			require.NoError(t, err)

			b := samplePerson("Actor B")
			b.TMDBID = ptrTMDBID(1002)
			id2, err := repo.Upsert(ctx, b)
			require.NoError(t, err)

			rows, err := repo.ListByIDs(ctx, []int64{id1, id2, 99999})
			require.NoError(t, err)
			require.Len(t, rows, 2, "missing ids are silently skipped")
			assert.Equal(t, id1, rows[0].ID)
			assert.Equal(t, id2, rows[1].ID)
		})
	}
}

// TestPeopleRepository_Upsert_PreservesFullHydration covers the
// stub-downgrade defence: a series_enrichment_worker stub upsert
// over an existing full row must NOT clobber hydration back to stub.
func TestPeopleRepository_Upsert_PreservesFullHydration(t *testing.T) {

	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			repo := NewPeopleRepository(db)
			ctx := context.Background()

			full := samplePerson("Pascal")
			full.Hydration = people.HydrationFull
			_, err := repo.Upsert(ctx, full)
			require.NoError(t, err)

			stub := samplePerson("Pascal Updated")
			stub.Hydration = people.HydrationStub
			id, err := repo.Upsert(ctx, stub)
			require.NoError(t, err)

			got, err := repo.Get(ctx, id, "en-US")
			require.NoError(t, err)
			assert.Equal(t, people.HydrationFull, got.Hydration,
				"stub upsert MUST NOT downgrade a full-hydrated row")
			// Story 1084: Get resolves the display name via COALESCE. No
			// people_texts row is seeded, so it returns original_name — which
			// merged from "orig: Pascal" to "orig: Pascal Updated", proving the
			// stub upsert still applied its non-hydration columns.
			assert.Equal(t, "orig: Pascal Updated", got.Name,
				"non-hydration fields still merge (original_name updated)")
		})
	}
}

func TestPeopleRepository_Upsert_PartialUnique(t *testing.T) {

	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			repo := NewPeopleRepository(db)
			ctx := context.Background()

			a := samplePerson("Orphan A")
			a.TMDBID = nil
			a.IMDBID = new("nm9000001")
			id1, err := repo.Upsert(ctx, a)
			require.NoError(t, err)

			b := samplePerson("Orphan B")
			b.TMDBID = nil
			b.IMDBID = new("nm9000002")
			id2, err := repo.Upsert(ctx, b)
			require.NoError(t, err)
			assert.NotEqual(t, id1, id2,
				"two NULL-tmdb rows must coexist — partial index excludes them")
		})
	}
}

// TestPeopleRepository_BatchUpsert_NoDeadlockUnderConcurrency proves
// that the cross-tx deadlock (SQLSTATE 40P01) observed in production
// on 2026-06-22 is suppressed when callers sort their per-tx Upsert
// loop by tmdb_id ASC before iterating. This is the discipline
// series_worker.go now enforces (B-26 patch 2). Despite the BatchUpsert
// suffix, this exercises single-row Upsert concurrently — the contract
// under test is call-site ordering, not a batch API.
//
// Postgres-only: SQLite does not have row-level UPDATE locks and
// cannot reproduce the deadlock. The test SKIPS the sqlite backend
// rather than asserting a no-op, to keep the failure trail focused
// on the lane that actually exercises the invariant.
//
// Repro mechanics: N=4 goroutines × 30 iters each, all upserting the
// same 8 tmdb_ids in independently-shuffled order — BEFORE the
// sort.Slice call, deadlock detector fires within ~5s. AFTER sort,
// all 120 transactions complete cleanly.
func TestPeopleRepository_BatchUpsert_NoDeadlockUnderConcurrency(t *testing.T) {
	t.Parallel()

	sawPostgres := false
	for _, backend := range testhelpers.AllBackends(t) {
		if backend.Name != "postgres" {
			// sqlite has no row-level deadlock detector — skip.
			continue
		}
		sawPostgres = true
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)

			const N = 4
			const iters = 30
			tmdbIDs := []int64{1001, 1002, 1003, 1004, 1005, 1006, 1007, 1008}

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

						// The contract under test: callers sort by tmdb_id ASC
						// BEFORE the per-row Upsert loop. Removing this line
						// re-introduces the deadlock — the test catches that
						// regression on the next pre-merge run.
						slices.Sort(ids)

						err := db.Transaction(func(tx *gorm.DB) error {
							repo := NewPeopleRepository(tx)
							for _, id := range ids {
								tid := domain.TMDBID(id)
								p := people.Person{
									Name:      fmt.Sprintf("Test Person %d", id),
									Hydration: people.HydrationStub,
									TMDBID:    &tid,
								}
								if _, err := repo.Upsert(context.Background(), p); err != nil {
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
		t.Log("postgres backend not available (set SEASONFILL_TEST_POSTGRES_ENABLE=1 to enable); deadlock repro skipped")
	}
}

// TestPeopleRepository_Upsert_AtomicNoDeadlock_B37 proves the B-37
// hybrid fix: callers that have N>1 persons to write in a single tx
// MUST go through BatchUpsert (which sorts by tmdb_id ASC internally
// to enforce global lock ordering); paired with the atomic CASE upsert
// + tx-level deadlock-retry helper, the burst path tolerates the
// pathological worst case below WITHOUT the per-callsite slices.Sort
// the B-26 patch added.
//
// Why BatchUpsert is required to make this contract true: Postgres
// takes row-level EXCLUSIVE locks during INSERT ... ON CONFLICT DO
// UPDATE in row-arrival order. Two concurrent txes that issue per-row
// upserts in disagreeing orders will deadlock at the row-lock layer
// regardless of any retry budget — the lock graph cycle is structural
// (40P01 aborts the whole tx unconditionally). BatchUpsert centralises
// the sort so every burst-writing caller composes a globally-consistent
// lock acquisition order. The single-row Upsert path is reserved for
// callers that genuinely write one row per tx (person_worker.applyAll).
//
// Postgres-only: SQLite has no row-level deadlock detector. The test
// SKIPS the sqlite backend and logs a sentinel line when no Postgres
// backend is available so CI keeps visibility on the gating.
//
// Repro mechanics: N=4 goroutines × 30 iters each, all upserting the
// same 8 tmdb_ids in INDEPENDENTLY-SHUFFLED order. Models the post-N-2
// prod shape: series_worker + person_worker + Discovery + scan loop
// racing on the same person rows from independent transactions. With
// pre-B-37 (probe-then-insert) OR with B-37's atomic CASE but per-row
// Upsert in a loop, this workload reproduces SQLSTATE 40P01 within
// ~5s. With BatchUpsert + the tx-retry safety net, all 120 txes
// commit cleanly.
func TestPeopleRepository_Upsert_AtomicNoDeadlock_B37(t *testing.T) {
	t.Parallel()

	sawPostgres := false
	for _, backend := range testhelpers.AllBackends(t) {
		if backend.Name != "postgres" {
			// sqlite has no row-level deadlock detector — skip.
			continue
		}
		sawPostgres = true
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)

			const N = 4
			const iters = 30
			tmdbIDs := []int64{2001, 2002, 2003, 2004, 2005, 2006, 2007, 2008}

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

						// DELIBERATELY DO NOT SORT at the callsite. The
						// contract under test: BatchUpsert sorts internally,
						// so concurrent burst writers reach a globally
						// consistent lock acquisition order without any
						// per-callsite sort discipline. Removing the
						// BatchUpsert sort, or downgrading the loop body
						// to per-row Upsert (mirroring an unsorted hot
						// burst), re-introduces the deadlock; the test
						// catches the regression on the next pre-merge run.
						persons := make([]people.Person, len(ids))
						for idx, id := range ids {
							tid := domain.TMDBID(id)
							persons[idx] = people.Person{
								Name:      fmt.Sprintf("B37 Person %d", id),
								Hydration: people.HydrationStub,
								TMDBID:    &tid,
							}
						}
						err := database.TransactWithDeadlockRetry(
							db,
							database.DefaultDeadlockRetryAttempts,
							func(tx *gorm.DB) error {
								repo := NewPeopleRepository(tx)
								_, err := repo.BatchUpsert(context.Background(), persons)
								return err
							},
						)
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
				"B-37: BatchUpsert + atomic CASE + tx-retry must tolerate unsorted concurrent bursts; got:\n%s",
				strings.Join(failures, "\n"))
		})
	}
	if !sawPostgres {
		t.Log("postgres backend not available (set SEASONFILL_TEST_POSTGRES_ENABLE=1 to enable); B-37 deadlock repro skipped")
	}
}

// TestPeopleRepository_Get_NameFallback — Story 1084 (Phase A). The person-page
// read (PeopleRepository.Get) must resolve the DISPLAY name via the same 4-tier
// COALESCE ListByIDsWithNameFallback uses: requested-lang → en-US →
// people.original_name → people.name. Biography resolution must keep working.
func TestPeopleRepository_Get_NameFallback(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()

			// seed installs ONE person (base people.name + original_name) plus any
			// people_texts rows, and returns the repo + assigned id.
			seed := func(t *testing.T, baseName string, originalName *string, texts []people.PersonText) (*PeopleRepository, int64) {
				t.Helper()
				db := backend.NewDB(t)
				repo := NewPeopleRepository(db)
				pid, err := repo.Upsert(ctx, people.Person{
					Name:         baseName,
					OriginalName: originalName,
					Hydration:    people.HydrationStub,
					TMDBID:       ptrTMDBID(9300),
				})
				require.NoError(t, err)
				require.Greater(t, pid, int64(0))
				if len(texts) > 0 {
					for i := range texts {
						texts[i].PersonID = pid
					}
					require.NoError(t, NewPeopleTextsRepository(db).BatchUpsert(ctx, texts))
				}
				return repo, pid
			}

			// Case 1 — lang=ru-RU AND people_texts[ru] present → Cyrillic.
			t.Run("ru_present_resolves_cyrillic", func(t *testing.T) {
				repo, pid := seed(t, "Адам Скотт", new("Noah Wyle"), []people.PersonText{
					{Language: "en-US", Name: new("Noah Wyle")},
					{Language: "ru-RU", Name: new("Ноа Уайли")},
				})
				got, err := repo.Get(ctx, pid, "ru-RU")
				require.NoError(t, err)
				assert.Equal(t, "Ноа Уайли", got.Name)
			})

			// Case 2 — lang=ru-RU AND people_texts[ru] absent → en-US tier.
			t.Run("ru_absent_falls_back_to_en", func(t *testing.T) {
				repo, pid := seed(t, "base", new("orig"), []people.PersonText{
					{Language: "en-US", Name: new("Noah Wyle")},
				})
				got, err := repo.Get(ctx, pid, "ru-RU")
				require.NoError(t, err)
				assert.Equal(t, "Noah Wyle", got.Name)
			})

			// Case 3 — no people_texts at all → original_name.
			t.Run("no_texts_falls_back_to_original_name", func(t *testing.T) {
				repo, pid := seed(t, "Адам Скотт", new("Noah Wyle"), nil)
				got, err := repo.Get(ctx, pid, "ru-RU")
				require.NoError(t, err)
				assert.Equal(t, "Noah Wyle", got.Name)
			})

			// Case 4 — lang=en-US → en-US texts row.
			t.Run("en_us_resolves_en_text", func(t *testing.T) {
				repo, pid := seed(t, "Адам Скотт", new("Noah Wyle"), []people.PersonText{
					{Language: "en-US", Name: new("Noah Wyle")},
					{Language: "ru-RU", Name: new("Ноа Уайли")},
				})
				got, err := repo.Get(ctx, pid, "en-US")
				require.NoError(t, err)
				assert.Equal(t, "Noah Wyle", got.Name)
			})

			// Case 5 — lang="" normalises to en-US.
			t.Run("empty_lang_normalises_to_en", func(t *testing.T) {
				repo, pid := seed(t, "base", new("orig"), []people.PersonText{
					{Language: "en-US", Name: new("Noah Wyle")},
					{Language: "ru-RU", Name: new("Ноа Уайли")},
				})
				got, err := repo.Get(ctx, pid, "")
				require.NoError(t, err)
				assert.Equal(t, "Noah Wyle", got.Name)
			})

			// Case 6 — unknown id → ports.ErrNotFound (m.ID==0 guard on Raw().Scan).
			t.Run("unknown_id_returns_not_found", func(t *testing.T) {
				repo, _ := seed(t, "x", nil, nil)
				_, err := repo.Get(ctx, 987654, "ru-RU")
				require.True(t, errors.Is(err, ports.ErrNotFound))
			})

			// Biography resolution still works alongside the name COALESCE.
			t.Run("biography_still_resolves", func(t *testing.T) {
				db := backend.NewDB(t)
				repo := NewPeopleRepository(db)
				pid, err := repo.Upsert(ctx, people.Person{
					Name:      "Adam Scott",
					Hydration: people.HydrationStub,
					TMDBID:    ptrTMDBID(9301),
				})
				require.NoError(t, err)
				require.NoError(t, NewPeopleTextsRepository(db).BatchUpsert(ctx, []people.PersonText{
					{PersonID: pid, Language: "ru-RU", Name: new("Адам Скотт")},
				}))
				require.NoError(t, NewPersonBiographiesRepository(db).Upsert(ctx, people.PersonBiography{
					PersonID:  pid,
					Language:  "en-US",
					Biography: new("An American actor."),
				}))
				got, err := repo.Get(ctx, pid, "ru-RU")
				require.NoError(t, err)
				assert.Equal(t, "Адам Скотт", got.Name, "name resolves ru")
				assert.Equal(t, "An American actor.", got.Biography, "bio falls back to en-US")
				assert.Equal(t, "en-US", got.BiographyLanguage)
			})
		})
	}
}

// TestPeopleRepository_Get_ResolvesBiographyViaFallback proves the
// people.Get path JOINs through the shared §5.6 helper.
func TestPeopleRepository_Get_ResolvesBiographyViaFallback(t *testing.T) {

	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			ctx := context.Background()
			repo := NewPeopleRepository(db)
			bioRepo := NewPersonBiographiesRepository(db)

			id, err := repo.Upsert(ctx, samplePerson("Pedro Pascal"))
			require.NoError(t, err)
			require.NoError(t, bioRepo.Upsert(ctx, people.PersonBiography{
				PersonID:  id,
				Language:  "en-US",
				Biography: new("Chilean-American actor."),
			}))

			// Request ru-RU — only en-US row exists, helper returns en-US.
			got, err := repo.Get(ctx, id, "ru-RU")
			require.NoError(t, err)
			assert.Equal(t, "en-US", got.BiographyLanguage)
			assert.Equal(t, "Chilean-American actor.", got.Biography)
		})
	}
}

// TestPeopleRepository_Get_BiographyFallsBackToEnUS proves the reader used by
// people/usecase (PeopleRepository.Get → pickLanguageFallback) resolves a
// ru-RU request to the en-US biography when no ru-RU row exists — the exact
// contract that lets PersonWorker skip writing an empty ru row (S-H). No
// person_biographies reader change was required for the all-langs writer.
func TestPeopleRepository_Get_BiographyFallsBackToEnUS(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			ctx := context.Background()

			peopleRepo := NewPeopleRepository(db)
			personID, err := peopleRepo.Upsert(ctx, samplePerson("Bryan Cranston"))
			require.NoError(t, err)

			// Seed ONLY the en-US biography — mirrors the S-H writer skipping
			// an absent/empty ru-RU translation entry.
			bios := NewPersonBiographiesRepository(db)
			require.NoError(t, bios.Upsert(ctx, people.PersonBiography{
				PersonID:  personID,
				Language:  "en-US",
				Biography: new("An American actor."),
			}))

			// A ru-RU read must fall back to the en-US row.
			got, err := peopleRepo.Get(ctx, personID, "ru-RU")
			require.NoError(t, err)
			assert.Equal(t, "An American actor.", got.Biography)
			assert.Equal(t, "en-US", got.BiographyLanguage)
		})
	}
}
