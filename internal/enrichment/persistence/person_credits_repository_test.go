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
