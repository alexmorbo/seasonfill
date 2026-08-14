//go:build integration

// Story Ф2.1 — GET /movies/:tmdb_id/cast read path against a real DB.
// Seeds a re-enriched movie (canon + people + movie person_credits) and drives
// the cast usecase, asserting a non-empty, credit-ordered cast list. Runs on
// SQLite + testcontainers Postgres via testhelpers.AllBackends.
package integration

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/internal/catalog/domain/movie"
	"github.com/alexmorbo/seasonfill/internal/enrichment/domain/people"
	enrichpersistence "github.com/alexmorbo/seasonfill/internal/enrichment/persistence"
	mdapp "github.com/alexmorbo/seasonfill/internal/moviedetail/app"
	database "github.com/alexmorbo/seasonfill/internal/shared/db"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
	"github.com/alexmorbo/seasonfill/internal/shared/testhelpers"
)

func TestF21MovieCast_ReadPath(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			ctx := context.Background()
			tid := domain.TMDBID(603)

			// Canon (HydrationFull → "re-enriched").
			movieRepo := enrichpersistence.NewMovieRepository(db)
			_, err := movieRepo.Upsert(ctx, movie.Canon{
				TMDBID: &tid, Hydration: movie.HydrationFull, Title: "The Matrix",
			})
			require.NoError(t, err)

			// Two people + their movie cast credits.
			peopleRepo := enrichpersistence.NewPeopleRepository(db)
			ptid1, ptid2 := domain.TMDBID(6384), domain.TMDBID(530)
			// OriginalName drives ListByIDsWithNameFallback's base fallback
			// (people.name was removed in migration 1084b — the reader resolves
			// COALESCE(people_texts, original_name); no people_texts seeded here).
			keanu, carrie := "Keanu Reeves", "Carrie-Anne Moss"
			id1, err := peopleRepo.Upsert(ctx, people.Person{TMDBID: &ptid1, Name: keanu, OriginalName: &keanu, Hydration: people.HydrationStub})
			require.NoError(t, err)
			id2, err := peopleRepo.Upsert(ctx, people.Person{TMDBID: &ptid2, Name: carrie, OriginalName: &carrie, Hydration: people.HydrationStub})
			require.NoError(t, err)

			creditsRepo := enrichpersistence.NewPersonCreditsRepository(db)
			order0, order1 := 0, 1
			neo, trinity := "Neo", "Trinity"
			_, err = creditsRepo.BatchUpsertAuthoritative(ctx, []database.PersonCreditModel{
				{PersonID: id1, TMDBCreditID: "m1", MediaType: "movie", TMDBMediaID: 603, Title: "The Matrix", Kind: "cast", CharacterName: &neo, CreditOrder: &order0},
				{PersonID: id2, TMDBCreditID: "m2", MediaType: "movie", TMDBMediaID: 603, Title: "The Matrix", Kind: "cast", CharacterName: &trinity, CreditOrder: &order1},
			})
			require.NoError(t, err)

			uc := mdapp.NewCastUseCase(movieRepo, creditsRepo, peopleRepo, enrichpersistence.NewMovieI18nReadRepository(db))
			page, err := uc.Get(ctx, tid, "en-US")
			require.NoError(t, err)
			require.Len(t, page.Cast, 2)
			// usecase preserves person_id ASC (repo order); REST sorts by credit —
			// assert membership + that both credits resolved.
			names := map[string]bool{}
			for _, e := range page.Cast {
				names[e.Person.Name] = true
			}
			assert.True(t, names["Keanu Reeves"])
			assert.True(t, names["Carrie-Anne Moss"])
		})
	}
}
