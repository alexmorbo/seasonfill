package persistence

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	ports "github.com/alexmorbo/seasonfill/internal/shared/dataports"
	database "github.com/alexmorbo/seasonfill/internal/shared/db"
	"github.com/alexmorbo/seasonfill/internal/shared/testhelpers"
)

func samplePersonCredit(personID int64, creditID, title string, tmdbMediaID int) database.PersonCreditModel {
	return database.PersonCreditModel{
		PersonID:      personID,
		TMDBCreditID:  creditID,
		MediaType:     "tv",
		TMDBMediaID:   tmdbMediaID,
		Title:         title,
		OriginalTitle: new("The Last of Us (Original)"),
		Year:          new(2024),
		CharacterName: new("Some Character"),
		Kind:          "cast",
		Department:    new("Production"),
		PosterPath:    new("/poster.jpg"),
		VoteAverage:   new(7.8),
		TMDBVotes:     new(12345),
		EpisodeCount:  new(10),
	}
}

func TestPersonCreditsRepository_UpsertAndGet(t *testing.T) {

	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			ctx := context.Background()
			personID, err := NewPeopleRepository(db).Upsert(ctx, samplePerson("Pedro Pascal"))
			require.NoError(t, err)
			repo := NewPersonCreditsRepository(db)

			id, err := repo.Upsert(ctx, samplePersonCredit(personID, "credit-001", "The Last of Us", 100088))
			require.NoError(t, err)
			require.NotZero(t, id)

			got, err := repo.Get(ctx, id)
			require.NoError(t, err)
			assert.Equal(t, "The Last of Us", got.Title)
			assert.Equal(t, "cast", got.Kind)
		})
	}
}

func TestPersonCreditsRepository_Get_NotFound(t *testing.T) {

	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			repo := NewPersonCreditsRepository(db)
			_, err := repo.Get(context.Background(), 9999)
			require.True(t, errors.Is(err, ports.ErrNotFound))
		})
	}
}

// TestPersonCreditsRepository_BatchUpsert_Idempotent covers the 50-row
// acceptance criterion: ONE INSERT, ids round-trip on re-batch, no
// duplicate rows.
func TestPersonCreditsRepository_BatchUpsert_Idempotent(t *testing.T) {

	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			ctx := context.Background()
			personID, err := NewPeopleRepository(db).Upsert(ctx, samplePerson("Pedro Pascal"))
			require.NoError(t, err)
			repo := NewPersonCreditsRepository(db)

			const n = 50
			credits := make([]database.PersonCreditModel, n)
			for i := range n {
				credits[i] = samplePersonCredit(personID, fmt.Sprintf("credit-%03d", i), fmt.Sprintf("Title %03d", i), 100000+i)
			}

			ids, err := repo.BatchUpsert(ctx, credits)
			require.NoError(t, err)
			require.Len(t, ids, n)

			// Re-batch — same ids, no new rows.
			ids2, err := repo.BatchUpsert(ctx, credits)
			require.NoError(t, err)
			require.Equal(t, ids, ids2,
				"second batch must resolve to the same ids by natural key")

			rows, err := repo.ListByPerson(ctx, personID)
			require.NoError(t, err)
			assert.Len(t, rows, n,
				"idempotent re-batch must NOT produce duplicate rows")
		})
	}
}

func TestPersonCreditsRepository_ListByPerson(t *testing.T) {

	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			ctx := context.Background()
			personID, err := NewPeopleRepository(db).Upsert(ctx, samplePerson("Pedro Pascal"))
			require.NoError(t, err)
			repo := NewPersonCreditsRepository(db)

			c2023 := samplePersonCredit(personID, "credit-2023", "The Last of Us", 100088)
			c2023.Year = new(2023)
			c2019 := samplePersonCredit(personID, "credit-2019", "The Mandalorian", 82856)
			c2019.Year = new(2019)
			c2024 := samplePersonCredit(personID, "credit-2024", "Gladiator II", 558449)
			c2024.MediaType = "movie"
			c2024.Year = new(2024)

			_, err = repo.Upsert(ctx, c2019)
			require.NoError(t, err)
			_, err = repo.Upsert(ctx, c2023)
			require.NoError(t, err)
			_, err = repo.Upsert(ctx, c2024)
			require.NoError(t, err)

			rows, err := repo.ListByPerson(ctx, personID)
			require.NoError(t, err)
			require.Len(t, rows, 3)
			// year DESC then title ASC.
			assert.Equal(t, "Gladiator II", rows[0].Title)
			assert.Equal(t, "The Last of Us", rows[1].Title)
			assert.Equal(t, "The Mandalorian", rows[2].Title)
		})
	}
}

// TestPersonCreditsRepository_ListByPersons_BatchMatchesLoop proves the batched
// person_id IN(?) query (Story 1070) returns exactly the per-person grouping the
// ListByPerson loop would build — including each group's (year DESC, title ASC)
// order — with no-credit persons absent and duplicate ids de-duped.
func TestPersonCreditsRepository_ListByPersons_BatchMatchesLoop(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			ctx := context.Background()
			peopleRepo := NewPeopleRepository(db)
			// Distinct TMDBIDs — samplePerson without one collapses both to a
			// single person row on Upsert (see ListByMedia test).
			person1 := samplePerson("Person One")
			person1.TMDBID = ptrTMDBID(70001)
			p1, err := peopleRepo.Upsert(ctx, person1)
			require.NoError(t, err)
			person2 := samplePerson("Person Two")
			person2.TMDBID = ptrTMDBID(70002)
			p2, err := peopleRepo.Upsert(ctx, person2)
			require.NoError(t, err)
			repo := NewPersonCreditsRepository(db)

			// person 1 → 2 credits (year 2020 "B Show" + year 2022 "A Show"),
			// person 2 → 1 credit, person 3 (p1+p2+100000) → 0 (never inserted).
			c1a := samplePersonCredit(p1, "c1a", "B Show", 11)
			c1a.Year = new(2020)
			c1b := samplePersonCredit(p1, "c1b", "A Show", 12)
			c1b.Year = new(2022)
			c2a := samplePersonCredit(p2, "c2a", "Film", 21)
			c2a.MediaType = "movie"
			c2a.Year = new(2019)
			_, err = repo.BatchUpsert(ctx, []database.PersonCreditModel{c1a, c1b, c2a})
			require.NoError(t, err)

			noCreditID := p1 + p2 + 100000         // never has credits
			ids := []int64{p1, p2, noCreditID, p1} // includes a duplicate + a no-credit person

			// Oracle: per-person ListByPerson (inline dedupe, keep self-contained).
			seen := map[int64]struct{}{}
			want := map[int64][]PersonCredit{}
			for _, pid := range ids {
				if _, ok := seen[pid]; ok {
					continue
				}
				seen[pid] = struct{}{}
				rows, lerr := repo.ListByPerson(ctx, pid)
				require.NoError(t, lerr)
				if len(rows) > 0 {
					want[pid] = rows
				}
			}

			got, err := repo.ListByPersons(ctx, ids)
			require.NoError(t, err)
			assert.Equal(t, want, got, "batched grouping must equal the per-person loop (incl. per-person order)")
			assert.NotContains(t, got, noCreditID, "person with no credits must be absent")
			// year DESC parity: person 1 yields "A Show" (2022) before "B Show" (2020).
			require.Len(t, got[p1], 2)
			assert.Equal(t, "A Show", got[p1][0].Title)
			assert.Equal(t, "B Show", got[p1][1].Title)

			empty, err := repo.ListByPersons(ctx, nil)
			require.NoError(t, err)
			assert.Empty(t, empty)
		})
	}
}

// TestPersonCreditsRepository_ListByMedia covers the reverse lookup
// "who from my library appears in this TMDB title?".
func TestPersonCreditsRepository_ListByMedia(t *testing.T) {

	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			ctx := context.Background()
			peopleRepo := NewPeopleRepository(db)
			repo := NewPersonCreditsRepository(db)

			pedro := samplePerson("Pedro Pascal")
			pedro.TMDBID = ptrTMDBID(10001)
			pedroID, err := peopleRepo.Upsert(ctx, pedro)
			require.NoError(t, err)

			bella := samplePerson("Bella Ramsey")
			bella.TMDBID = ptrTMDBID(10002)
			bellaID, err := peopleRepo.Upsert(ctx, bella)
			require.NoError(t, err)

			other := samplePerson("Other Person")
			other.TMDBID = ptrTMDBID(10003)
			otherID, err := peopleRepo.Upsert(ctx, other)
			require.NoError(t, err)

			const tlouTMDB = 100088
			_, err = repo.Upsert(ctx, samplePersonCredit(pedroID, "tlou-pedro", "The Last of Us", tlouTMDB))
			require.NoError(t, err)
			_, err = repo.Upsert(ctx, samplePersonCredit(bellaID, "tlou-bella", "The Last of Us", tlouTMDB))
			require.NoError(t, err)
			_, err = repo.Upsert(ctx, samplePersonCredit(otherID, "other-1", "Something Else", 99999))
			require.NoError(t, err)

			rows, err := repo.ListByMedia(ctx, "tv", tlouTMDB)
			require.NoError(t, err)
			require.Len(t, rows, 2,
				"reverse lookup must return both people credited on the TMDB title")
			personIDs := []int64{rows[0].PersonID, rows[1].PersonID}
			assert.Contains(t, personIDs, pedroID)
			assert.Contains(t, personIDs, bellaID)
		})
	}
}

// TestPersonCreditsRepository_NewFields_RoundTrip covers Story 307:
// migration 000038 added department / original_title / tmdb_votes
// to person_credits. The adapter writes them via DoUpdate columns;
// Get + ListByPerson read them back. Idempotent re-Upsert preserves
// the values (overwrite-with-same-value path).
func TestPersonCreditsRepository_NewFields_RoundTrip(t *testing.T) {

	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			ctx := context.Background()
			personID, err := NewPeopleRepository(db).Upsert(ctx, samplePerson("Pedro Pascal"))
			require.NoError(t, err)
			repo := NewPersonCreditsRepository(db)

			pc := samplePersonCredit(personID, "credit-001", "The Last of Us", 100088)
			pc.Department = new("Production")
			pc.OriginalTitle = new("The Last of Us (Original)")
			pc.TMDBVotes = new(12345)

			id, err := repo.Upsert(ctx, pc)
			require.NoError(t, err)
			require.NotZero(t, id)

			got, err := repo.Get(ctx, id)
			require.NoError(t, err)
			require.NotNil(t, got.Department)
			assert.Equal(t, "Production", *got.Department)
			require.NotNil(t, got.OriginalTitle)
			assert.Equal(t, "The Last of Us (Original)", *got.OriginalTitle)
			require.NotNil(t, got.TMDBVotes)
			assert.Equal(t, 12345, *got.TMDBVotes)

			// Idempotent re-Upsert — same row, same values.
			pc2 := pc
			id2, err := repo.Upsert(ctx, pc2)
			require.NoError(t, err)
			assert.Equal(t, id, id2, "re-Upsert by natural key must reuse the row")

			rows, err := repo.ListByPerson(ctx, personID)
			require.NoError(t, err)
			require.Len(t, rows, 1)
			require.NotNil(t, rows[0].Department)
			assert.Equal(t, "Production", *rows[0].Department)
			require.NotNil(t, rows[0].OriginalTitle)
			assert.Equal(t, "The Last of Us (Original)", *rows[0].OriginalTitle)
			require.NotNil(t, rows[0].TMDBVotes)
			assert.Equal(t, 12345, *rows[0].TMDBVotes)
		})
	}
}

// TestPersonCreditsRepository_NewFields_Nullable covers the rare
// "TMDB emitted an empty field" case — the mapper's nonEmptyPtr /
// nonZeroIntPtr helpers strip blanks to nil. Adapter writes nil;
// the column stays NULL; round-trip preserves nil.
func TestPersonCreditsRepository_NewFields_Nullable(t *testing.T) {

	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			ctx := context.Background()
			personID, err := NewPeopleRepository(db).Upsert(ctx, samplePerson("Pedro Pascal"))
			require.NoError(t, err)
			repo := NewPersonCreditsRepository(db)

			pc := samplePersonCredit(personID, "credit-002", "Cold Credit", 200022)
			pc.Department = nil
			pc.OriginalTitle = nil
			pc.TMDBVotes = nil

			id, err := repo.Upsert(ctx, pc)
			require.NoError(t, err)

			got, err := repo.Get(ctx, id)
			require.NoError(t, err)
			assert.Nil(t, got.Department)
			assert.Nil(t, got.OriginalTitle)
			assert.Nil(t, got.TMDBVotes)
		})
	}
}

// TestPersonCreditsRepository_BatchUpsert_DedupesByConflictKey is the
// B-19 regression: a batch containing 2+ rows that share
// (person_id, tmdb_credit_id) must NOT crash Postgres with SQLSTATE
// 21000 ("ON CONFLICT DO UPDATE command cannot affect row a second
// time"). The repository folds the duplicates client-side; the
// returned ids slice still mirrors input length so callers indexing by
// position survive.
//
// Producer in prod: series_worker.applyEpisodeCredits projects every
// per-episode crew credit, and a show-runner shares one TMDB credit_id
// across an entire season — the batch carries N copies of one
// composite key.
func TestPersonCreditsRepository_BatchUpsert_DedupesByConflictKey(t *testing.T) {

	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			ctx := context.Background()
			personID, err := NewPeopleRepository(db).Upsert(ctx, samplePerson("Vince Gilligan"))
			require.NoError(t, err)
			repo := NewPersonCreditsRepository(db)

			// 5 input rows:
			//   [0]  (personID, "credit-shared") title "Pilot"
			//   [1]  (personID, "credit-shared") title "Cat's in the Bag"   (dupe of [0])
			//   [2]  (personID, "credit-shared") title "Bit by a Dead Bee"  (dupe of [0])
			//   [3]  (personID, "credit-unique-A") — distinct key
			//   [4]  (personID, "credit-unique-B") — distinct key
			//
			// Expected DB state after BatchUpsert: 3 rows
			// (1 for "credit-shared" + 2 unique). ids slice length == 5;
			// ids[0]==ids[1]==ids[2] (all resolve to the row written
			// from the first occurrence); ids[3], ids[4] are distinct
			// and not equal to ids[0].
			credits := []database.PersonCreditModel{
				samplePersonCredit(personID, "credit-shared", "Pilot", 200001),
				samplePersonCredit(personID, "credit-shared", "Cat's in the Bag", 200002),
				samplePersonCredit(personID, "credit-shared", "Bit by a Dead Bee", 200003),
				samplePersonCredit(personID, "credit-unique-A", "Gray Matter", 200004),
				samplePersonCredit(personID, "credit-unique-B", "Crazy Handful of Nothin'", 200005),
			}

			ids, err := repo.BatchUpsert(ctx, credits)
			require.NoError(t, err, "duplicate composite keys must NOT raise SQLSTATE 21000")
			require.Len(t, ids, 5, "ids slice must mirror input length so callers indexing by position still work")

			assert.Equal(t, ids[0], ids[1], "duplicate at input[1] must resolve to the same row as input[0]")
			assert.Equal(t, ids[0], ids[2], "duplicate at input[2] must resolve to the same row as input[0]")
			assert.NotEqual(t, ids[0], ids[3], "unique key at input[3] must earn a distinct id")
			assert.NotEqual(t, ids[0], ids[4], "unique key at input[4] must earn a distinct id")
			assert.NotEqual(t, ids[3], ids[4], "two distinct unique keys must earn two distinct ids")

			rows, err := repo.ListByPerson(ctx, personID)
			require.NoError(t, err)
			assert.Len(t, rows, 3, "exactly 3 rows persisted: 1 shared-key + 2 unique")

			// Re-run the same batch — the repo must still survive and
			// the returned ids must match the first run (idempotent
			// natural-key resolution).
			ids2, err := repo.BatchUpsert(ctx, credits)
			require.NoError(t, err)
			require.Equal(t, ids, ids2, "second batch must resolve to the same ids by natural key")

			rows2, err := repo.ListByPerson(ctx, personID)
			require.NoError(t, err)
			assert.Len(t, rows2, 3, "idempotent re-batch must NOT add rows")
		})
	}
}

// TestPersonCreditsRepository_BatchUpsert_BatchSize_PostgresProtocolSafe is
// the N-2 regression guard: PG extended protocol caps query params at
// 65535. PersonCreditModel binds ~18 columns per row, so a single
// .Create(rows) round-trip hits the cap around 3640 rows. Rich casts
// (Rick and Morty fanned across tv_episode aggregation, etc.) regularly
// blew past that ceiling once the Discovery worker started dispatching
// enrichment in earnest. Repo now CreateInBatches at 1000 rows/batch —
// 4000 distinct rows MUST round-trip clean, every row earning a non-zero
// id, no protocol error.
//
// SQLite has no equivalent cap; the SQLite branch still exercises the
// batching mechanics (multiple sub-INSERTs, ids stay sequential).
// Postgres branch (opt-in via SEASONFILL_TEST_POSTGRES_ENABLE) actually
// proves the protocol-limit fix.
func TestPersonCreditsRepository_BatchUpsert_BatchSize_PostgresProtocolSafe(t *testing.T) {

	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			ctx := context.Background()
			personID, err := NewPeopleRepository(db).Upsert(ctx, samplePerson("Cast Member"))
			require.NoError(t, err)
			repo := NewPersonCreditsRepository(db)

			// 4000 unique rows — comfortably above the 3640-row ceiling
			// of a single round-trip, so the test fails loudly if
			// CreateInBatches ever regresses to .Create.
			const n = 4000
			credits := make([]database.PersonCreditModel, n)
			for i := range n {
				credits[i] = samplePersonCredit(
					personID,
					fmt.Sprintf("credit-batch-%05d", i),
					fmt.Sprintf("Title %05d", i),
					300000+i,
				)
			}

			ids, err := repo.BatchUpsert(ctx, credits)
			require.NoError(t, err,
				"batch of %d rows must NOT exceed Postgres' 65535 extended-protocol param cap", n)
			require.Len(t, ids, n)
			for i, id := range ids {
				require.NotZero(t, id, "row %d must earn a non-zero id from RETURNING", i)
			}

			rows, err := repo.ListByPerson(ctx, personID)
			require.NoError(t, err)
			assert.Len(t, rows, n, "every input row must persist as a distinct DB row")
		})
	}
}

// TestPersonCreditsRepository_CreditOrder_PersistAndCoalesce proves the
// Story 1087b column round-trips (migration + model + read SELECT) and that
// the COALESCE-guarded OnConflict preserves the series-worker billing order
// when a later order-less person-worker row re-upserts the same natural key.
func TestPersonCreditsRepository_CreditOrder_PersistAndCoalesce(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			ctx := context.Background()
			personID, err := NewPeopleRepository(db).Upsert(ctx, samplePerson("Billing Order"))
			require.NoError(t, err)
			repo := NewPersonCreditsRepository(db)

			// series-worker style row: carries a billing order.
			seriesRow := samplePersonCredit(personID, "credit-order", "Show", 900)
			seriesRow.CreditOrder = new(5)
			_, err = repo.BatchUpsert(ctx, []database.PersonCreditModel{seriesRow})
			require.NoError(t, err)

			// person-worker style row: SAME natural key, NO order (must not clobber).
			personRow := samplePersonCredit(personID, "credit-order", "Show", 900)
			personRow.CreditOrder = nil
			_, err = repo.BatchUpsert(ctx, []database.PersonCreditModel{personRow})
			require.NoError(t, err)

			rows, err := repo.ListByMediaWithTextFallback(ctx, "tv", 900, "en-US")
			require.NoError(t, err)
			require.Len(t, rows, 1)
			require.NotNil(t, rows[0].CreditOrder, "COALESCE must preserve the series-worker order")
			assert.Equal(t, 5, *rows[0].CreditOrder)
		})
	}
}

// TestPersonCreditsRepository_VoteAverage_PersistAndCoalesce proves the Story
// 1034 COALESCE-guard on vote_average: a person-worker row carrying the real
// TMDB rating must survive a LATER series-worker-style row that re-upserts the
// SAME natural key with a NULL rating (the series-worker person_credits(tv)
// build path historically emitted NULL, nulling the person-page ★rating).
// Symmetric to the CreditOrder guard test above. Also asserts the forward case
// (a non-NULL incoming rating updates a previously-NULL stored value) so the
// guard is proven to be COALESCE, not an unconditional keep-stored.
func TestPersonCreditsRepository_VoteAverage_PersistAndCoalesce(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			ctx := context.Background()
			personID, err := NewPeopleRepository(db).Upsert(ctx, samplePerson("Vote Average"))
			require.NoError(t, err)
			repo := NewPersonCreditsRepository(db)

			read := func(t *testing.T, mediaID int) *float64 {
				t.Helper()
				rows, err := repo.ListByMediaWithTextFallback(ctx, "tv", mediaID, "en-US")
				require.NoError(t, err)
				require.Len(t, rows, 1)
				return rows[0].VoteAverage
			}

			t.Run("later NULL must not clobber stored rating", func(t *testing.T) {
				// person-worker style row: carries the real TMDB rating.
				personRow := samplePersonCredit(personID, "va-a", "Show", 2001)
				personRow.VoteAverage = new(6.528)
				_, err := repo.BatchUpsert(ctx, []database.PersonCreditModel{personRow})
				require.NoError(t, err)

				// series-worker style row: SAME natural key, NULL rating.
				seriesRow := samplePersonCredit(personID, "va-a", "Show", 2001)
				seriesRow.VoteAverage = nil
				_, err = repo.BatchUpsert(ctx, []database.PersonCreditModel{seriesRow})
				require.NoError(t, err)

				got := read(t, 2001)
				require.NotNil(t, got, "COALESCE must preserve the stored rating against a NULL re-upsert")
				assert.InDelta(t, 6.528, *got, 1e-9)
			})

			t.Run("stored NULL, later non-NULL wins", func(t *testing.T) {
				// series-worker style row first: NULL rating.
				seriesRow := samplePersonCredit(personID, "va-b", "Show", 2002)
				seriesRow.VoteAverage = nil
				_, err := repo.BatchUpsert(ctx, []database.PersonCreditModel{seriesRow})
				require.NoError(t, err)

				// later row carries a real rating → must land.
				personRow := samplePersonCredit(personID, "va-b", "Show", 2002)
				personRow.VoteAverage = new(8.1)
				_, err = repo.BatchUpsert(ctx, []database.PersonCreditModel{personRow})
				require.NoError(t, err)

				got := read(t, 2002)
				require.NotNil(t, got, "a non-NULL incoming rating must update a previously-NULL stored value")
				assert.InDelta(t, 8.1, *got, 1e-9)
			})
		})
	}
}

// TestPersonCreditsRepository_LastAppearanceSeason_MaxMerge proves the Story
// 1090 column round-trips (migration 000039 + model + read SELECT) and that the
// portable CASE MAX-merge in OnConflict never regresses a stored value: a lower
// or NULL re-upload keeps the higher stored season, a NULL stored takes the
// incoming value, and a higher incoming value wins. Runs on BOTH backends to
// prove the CASE expression is dialect-portable (GREATEST/MAX would not be).
func TestPersonCreditsRepository_LastAppearanceSeason_MaxMerge(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			ctx := context.Background()
			personID, err := NewPeopleRepository(db).Upsert(ctx, samplePerson("Last Appearance"))
			require.NoError(t, err)
			repo := NewPersonCreditsRepository(db)

			upsert := func(t *testing.T, mediaID int, creditID string, season *int) {
				t.Helper()
				row := samplePersonCredit(personID, creditID, "Show", mediaID)
				row.LastAppearanceSeason = season
				_, err := repo.BatchUpsert(ctx, []database.PersonCreditModel{row})
				require.NoError(t, err)
			}
			read := func(t *testing.T, mediaID int) *int {
				t.Helper()
				rows, err := repo.ListByMediaWithTextFallback(ctx, "tv", mediaID, "en-US")
				require.NoError(t, err)
				require.Len(t, rows, 1)
				return rows[0].LastAppearanceSeason
			}

			t.Run("stored 3, current 2 -> stays 3", func(t *testing.T) {
				upsert(t, 1001, "la-a", new(3))
				upsert(t, 1001, "la-a", new(2))
				got := read(t, 1001)
				require.NotNil(t, got)
				assert.Equal(t, 3, *got)
			})
			t.Run("stored NULL, current 4 -> 4", func(t *testing.T) {
				upsert(t, 1002, "la-b", nil)
				upsert(t, 1002, "la-b", new(4))
				got := read(t, 1002)
				require.NotNil(t, got)
				assert.Equal(t, 4, *got)
			})
			t.Run("stored 3, current 5 -> 5", func(t *testing.T) {
				upsert(t, 1003, "la-c", new(3))
				upsert(t, 1003, "la-c", new(5))
				got := read(t, 1003)
				require.NotNil(t, got)
				assert.Equal(t, 5, *got)
			})
			t.Run("stored 3, current NULL -> stays 3", func(t *testing.T) {
				upsert(t, 1004, "la-d", new(3))
				upsert(t, 1004, "la-d", nil)
				got := read(t, 1004)
				require.NotNil(t, got, "a NULL incoming value must not clobber the stored season")
				assert.Equal(t, 3, *got)
			})
		})
	}
}
