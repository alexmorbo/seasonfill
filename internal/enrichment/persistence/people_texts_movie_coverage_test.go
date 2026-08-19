package persistence

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/internal/catalog/domain/movie"
	"github.com/alexmorbo/seasonfill/internal/enrichment/domain/people"
	database "github.com/alexmorbo/seasonfill/internal/shared/db"
	"github.com/alexmorbo/seasonfill/internal/shared/testhelpers"
)

func movieCredit(personID int64, creditID string, tmdbMovieID int) database.PersonCreditModel {
	return database.PersonCreditModel{
		PersonID:     personID,
		TMDBCreditID: creditID,
		MediaType:    "movie",
		TMDBMediaID:  tmdbMovieID,
		Title:        "Coverage Movie",
		Kind:         "cast",
	}
}

func TestPeopleTextsRepository_MovieCastNameCoverage(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			db := backend.NewDB(t)
			peopleRepo := NewPeopleRepository(db)
			textsRepo := NewPeopleTextsRepository(db)
			creditsRepo := NewPersonCreditsRepository(db)
			movieRepo := NewMovieRepository(db)

			const movieTMDB = 360920
			movieID, err := movieRepo.UpsertStub(ctx, movie.Canon{
				TMDBID:    ptrTMDBID(movieTMDB),
				Hydration: movie.HydrationStub,
				Title:     "Grinch Coverage",
			})
			require.NoError(t, err)
			require.NotZero(t, movieID)

			pids := make([]int64, 0, 3)
			for i, name := range []string{"Alpha", "Bravo", "Charlie"} {
				pid, uerr := peopleRepo.Upsert(ctx, people.Person{
					Name:      name,
					Hydration: people.HydrationStub,
					TMDBID:    ptrTMDBID(9600 + i),
				})
				require.NoError(t, uerr)
				pids = append(pids, pid)
				_, cerr := creditsRepo.Upsert(ctx, movieCredit(pid, name+"-mcredit", movieTMDB))
				require.NoError(t, cerr)
			}

			// ru names for persons 0 and 1 → covered=2, total=3.
			require.NoError(t, textsRepo.BatchUpsert(ctx, []people.PersonText{
				{PersonID: pids[0], Language: "ru-RU", Name: new("Альфа")},
				{PersonID: pids[1], Language: "ru-RU", Name: new("Браво")},
			}))

			covered, total, err := textsRepo.MovieCastNameCoverage(ctx, movieID, "ru-RU")
			require.NoError(t, err)
			assert.Equal(t, 2, covered)
			assert.Equal(t, 3, total)

			// A movie with no cast → (0,0,nil).
			emptyID, err := movieRepo.UpsertStub(ctx, movie.Canon{
				TMDBID: ptrTMDBID(999001), Hydration: movie.HydrationStub, Title: "No Cast",
			})
			require.NoError(t, err)
			covered, total, err = textsRepo.MovieCastNameCoverage(ctx, emptyID, "ru-RU")
			require.NoError(t, err)
			assert.Zero(t, covered)
			assert.Zero(t, total)

			// A NULL-name people_texts row is NOT counted covered.
			require.NoError(t, textsRepo.BatchUpsert(ctx, []people.PersonText{
				{PersonID: pids[2], Language: "ru-RU", Name: nil},
			}))
			covered, total, err = textsRepo.MovieCastNameCoverage(ctx, movieID, "ru-RU")
			require.NoError(t, err)
			assert.Equal(t, 2, covered, "NULL-name row must not count as covered")
			assert.Equal(t, 3, total)

			// A series 'tv' credit for the same person must NOT leak into movie coverage.
			_, err = creditsRepo.Upsert(ctx, samplePersonCredit(pids[0], "tv-leak", "TV Show", movieTMDB))
			require.NoError(t, err)
			covered, total, err = textsRepo.MovieCastNameCoverage(ctx, movieID, "ru-RU")
			require.NoError(t, err)
			assert.Equal(t, 2, covered, "'tv' leak does not change movie covered count")
			assert.Equal(t, 3, total, "media_type discriminator keeps 'tv' credits out of movie coverage")
		})
	}
}
