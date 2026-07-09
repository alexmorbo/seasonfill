package wiring

import (
	"context"
	"reflect"
	"testing"
	"time"

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

// TestPersonCreditsRepoAdapter_BatchUpsert_MapsEveryColumn is the #1093-class
// completeness guard. PersonCreditsRepoAdapter.BatchUpsert is a hand-maintained
// domain→GORM struct literal; #1093 shipped because two fields (credit_order,
// last_appearance_season) were silently omitted from that literal. This test
// round-trips a FULLY-populated domain PersonCredit and reflect-asserts that
// every bindable PersonCreditModel column (all except the repo-managed ID +
// created_at/updated_at) is non-zero after the write. When a future column is
// added to PersonCreditModel and the adapter literal forgets to map it, the
// written value stays zero and this test fails naming the field — catching the
// omission before it reaches PROD as a silent NULL column.
func TestPersonCreditsRepoAdapter_BatchUpsert_MapsEveryColumn(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			ctx := context.Background()

			personID, err := enrichpersistence.NewPeopleRepository(db).Upsert(ctx, people.Person{
				Name:      "Mark Scout",
				Hydration: people.HydrationStub,
			})
			require.NoError(t, err)

			repo := enrichpersistence.NewPersonCreditsRepository(db)
			adapter := PersonCreditsRepoAdapter{Inner: repo}

			// Every nullable domain field set non-nil/non-zero so every mapped
			// model column comes back non-zero. Year is derived from ReleaseDate,
			// PosterPath from PosterAsset, VoteAverage from TMDBRating.
			releaseDate := time.Date(2020, 5, 1, 0, 0, 0, 0, time.UTC)
			originalTitle := "Severance (Original)"
			characterName := "Mark S."
			department := "Acting"
			job := "Actor"
			episodeCount := 18
			creditOrder := 2
			lastAppearance := 3
			posterAsset := "/posters/severance.jpg"
			tmdbRating := 8.4
			tmdbVotes := 1200

			credit := people.PersonCredit{
				PersonID:             personID,
				MediaType:            "tv",
				TMDBMediaID:          100088,
				TMDBCreditID:         "credit-full-001",
				Kind:                 people.SeriesCreditCast,
				Title:                "Severance",
				OriginalTitle:        &originalTitle,
				CharacterName:        &characterName,
				Department:           &department,
				Job:                  &job,
				EpisodeCount:         &episodeCount,
				CreditOrder:          &creditOrder,
				LastAppearanceSeason: &lastAppearance,
				ReleaseDate:          &releaseDate,
				PosterAsset:          &posterAsset,
				TMDBRating:           &tmdbRating,
				TMDBVotes:            &tmdbVotes,
			}

			ids, err := adapter.BatchUpsert(ctx, []people.PersonCredit{credit})
			require.NoError(t, err)
			require.Len(t, ids, 1)

			got, err := repo.Get(ctx, ids[0])
			require.NoError(t, err)

			// Repo-managed columns are expected zero-until-persisted / not part
			// of the adapter mapping surface.
			skip := map[string]bool{
				"ID":        true,
				"CreatedAt": true,
				"UpdatedAt": true,
			}
			v := reflect.ValueOf(got)
			tp := v.Type()
			for i := 0; i < v.NumField(); i++ {
				name := tp.Field(i).Name
				if skip[name] {
					continue
				}
				assert.Falsef(t, v.Field(i).IsZero(),
					"PersonCreditModel.%s is zero after BatchUpsert round-trip — "+
						"did PersonCreditsRepoAdapter.BatchUpsert forget to map it? "+
						"(add the field to the struct literal in internal/wiring/enrichment.go)",
					name)
			}
		})
	}
}
