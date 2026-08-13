package persistence

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	database "github.com/alexmorbo/seasonfill/internal/shared/db"
	"github.com/alexmorbo/seasonfill/internal/shared/testhelpers"
)

// TestPersonCredits_MovieCreditOrderSurvivesNullUpsert proves that a movie-worker
// authoritative write of credit_order=N is NOT clobbered by a later incidental
// person-worker write that carries NO order (excluded.credit_order = NULL). This is
// the F-Ф1-04 invariant that lets Ф1.1a reuse BatchUpsertAuthoritative WITHOUT a new
// movie CASE branch: credit_order is COALESCE-guarded in both variants, and the
// media_type CASE pins 'tv' only (a 'movie' row is untouched).
func TestPersonCredits_MovieCreditOrderSurvivesNullUpsert(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			ctx := context.Background()

			personID, err := NewPeopleRepository(db).Upsert(ctx, samplePerson("Timothee"))
			require.NoError(t, err)
			repo := NewPersonCreditsRepository(db)

			// 1. Movie worker: authoritative write with credit_order=0 (lead billing).
			order := 0
			movieRow := database.PersonCreditModel{
				PersonID:     personID,
				TMDBCreditID: "movie-credit-1",
				MediaType:    "movie",
				TMDBMediaID:  42,
				Title:        "Dune",
				Kind:         "cast",
				CreditOrder:  &order,
			}
			ids, err := repo.BatchUpsertAuthoritative(ctx, []database.PersonCreditModel{movieRow})
			require.NoError(t, err)
			require.Len(t, ids, 1)

			// 2. Incidental person-worker write for the SAME (person_id, credit_id) with
			//    NO order (NULL) — mirrors personCreditFromMovie (sets no CreditOrder).
			nullOrderRow := database.PersonCreditModel{
				PersonID:     personID,
				TMDBCreditID: "movie-credit-1",
				MediaType:    "movie",
				TMDBMediaID:  42,
				Title:        "Dune",
				Kind:         "cast",
				CreditOrder:  nil,
			}
			_, err = repo.BatchUpsertAuthoritative(ctx, []database.PersonCreditModel{nullOrderRow})
			require.NoError(t, err)

			// 3. credit_order=0 must survive (COALESCE preserved it) AND media_type stays movie.
			got, err := repo.Get(ctx, ids[0])
			require.NoError(t, err)
			require.NotNil(t, got.CreditOrder, "credit_order must survive the null-order upsert")
			require.Equal(t, 0, *got.CreditOrder)
			require.Equal(t, "movie", got.MediaType, "media_type CASE pins 'tv' only; a movie row stays movie")
		})
	}
}
