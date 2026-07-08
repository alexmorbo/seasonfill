package wiring

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/internal/enrichment/domain/people"
	enrichpersistence "github.com/alexmorbo/seasonfill/internal/enrichment/persistence"
	"github.com/alexmorbo/seasonfill/internal/shared/testhelpers"
)

// TestPersonCreditsRepoAdapter_BatchUpsert_MapsSortColumns pins the
// domain→model field copy in PersonCreditsRepoAdapter.BatchUpsert. Both
// CreditOrder (?sort=credit / billing order) and LastAppearanceSeason
// (?sort=last_appearance) are computed upstream but were dropped by the
// struct literal, leaving both person_credits columns NULL for every
// media_type='tv' row on PROD — making both sorts no-ops. This test
// round-trips a credit carrying both values and asserts they persist.
func TestPersonCreditsRepoAdapter_BatchUpsert_MapsSortColumns(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			ctx := context.Background()

			personID, err := enrichpersistence.NewPeopleRepository(db).Upsert(ctx, people.Person{
				Name:      "Adam Scott",
				Hydration: people.HydrationStub,
			})
			require.NoError(t, err)

			repo := enrichpersistence.NewPersonCreditsRepository(db)
			adapter := PersonCreditsRepoAdapter{Inner: repo}

			creditOrder := 5
			lastAppearance := 3
			credit := people.PersonCredit{
				PersonID:             personID,
				MediaType:            "tv",
				TMDBMediaID:          100088,
				TMDBCreditID:         "credit-sort-001",
				Kind:                 people.SeriesCreditCast,
				Title:                "Severance",
				CreditOrder:          &creditOrder,
				LastAppearanceSeason: &lastAppearance,
			}

			ids, err := adapter.BatchUpsert(ctx, []people.PersonCredit{credit})
			require.NoError(t, err)
			require.Len(t, ids, 1)

			got, err := repo.Get(ctx, ids[0])
			require.NoError(t, err)

			require.NotNil(t, got.CreditOrder, "credit_order must persist, not NULL")
			assert.Equal(t, 5, *got.CreditOrder)
			require.NotNil(t, got.LastAppearanceSeason, "last_appearance_season must persist, not NULL")
			assert.Equal(t, 3, *got.LastAppearanceSeason)
		})
	}
}
