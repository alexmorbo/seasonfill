package persistence

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/internal/enrichment/domain/people"
	"github.com/alexmorbo/seasonfill/internal/shared/testhelpers"
)

func TestPeopleTextsRepository_BatchUpsert_COALESCE(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			db := backend.NewDB(t)
			peopleRepo := NewPeopleRepository(db)
			textsRepo := NewPeopleTextsRepository(db)

			pid, err := peopleRepo.Upsert(ctx, people.Person{
				Name: "Base", Hydration: people.HydrationStub, TMDBID: ptrTMDBID(9200),
			})
			require.NoError(t, err)

			// Seed en-US name.
			require.NoError(t, textsRepo.BatchUpsert(ctx, []people.PersonText{
				{PersonID: pid, Language: "en-US", Name: new("Adam Scott")},
			}))

			// A nil name upsert on the same PK must NOT wipe the stored value.
			require.NoError(t, textsRepo.BatchUpsert(ctx, []people.PersonText{
				{PersonID: pid, Language: "en-US", Name: nil},
			}))

			rows, err := peopleRepo.ListByIDsWithNameFallback(ctx, []int64{pid}, "en-US")
			require.NoError(t, err)
			require.Len(t, rows, 1)
			assert.Equal(t, "Adam Scott", rows[0].Name, "COALESCE must preserve prior name on nil write")

			// Validation: zero person_id and empty language are rejected.
			require.Error(t, textsRepo.BatchUpsert(ctx, []people.PersonText{{PersonID: 0, Language: "en-US"}}))
			require.Error(t, textsRepo.BatchUpsert(ctx, []people.PersonText{{PersonID: pid, Language: ""}}))
		})
	}
}
