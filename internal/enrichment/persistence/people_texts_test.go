package persistence

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/internal/catalog/domain/series"
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

// TestPeopleTextsRepository_CastNameCoverage — Story 1084 (Phase A). Coverage
// query that drives the SectionCast people_texts probe. total = distinct
// person_id credited to the series; covered = those with a people_texts row
// (language == lang AND name IS NOT NULL). D-7 (468a) dropped series_people, so
// the cast surface is person_credits(media_type='tv', tmdb_media_id) JOINed to
// series on tmdb_id. Dialect-sensitive (JOIN + DISTINCT) → run on sqlite +
// testcontainers Postgres.
func TestPeopleTextsRepository_CastNameCoverage(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			db := backend.NewDB(t)
			peopleRepo := NewPeopleRepository(db)
			textsRepo := NewPeopleTextsRepository(db)
			creditsRepo := NewPersonCreditsRepository(db)
			seriesRepo := NewSeriesRepository(db)

			const seriesTMDB = 700123
			seriesID, err := seriesRepo.UpsertStub(ctx, series.Canon{
				TMDBID:           ptrTMDBID(seriesTMDB),
				Hydration:        series.HydrationStub,
				OriginalTitle:    new("Coverage Show"),
				OriginalLanguage: new("en"),
			})
			require.NoError(t, err)
			require.NotZero(t, seriesID)

			// Seed 3 persons, each credited to the series (tv person_credits row).
			pids := make([]int64, 0, 3)
			for i, name := range []string{"Alpha", "Bravo", "Charlie"} {
				pid, err := peopleRepo.Upsert(ctx, people.Person{
					Name:      name,
					Hydration: people.HydrationStub,
					TMDBID:    ptrTMDBID(9400 + i),
				})
				require.NoError(t, err)
				pids = append(pids, pid)
				_, err = creditsRepo.Upsert(ctx, samplePersonCredit(pid, name+"-credit", "Coverage Show", seriesTMDB))
				require.NoError(t, err)
			}

			// ru-RU names for persons 0 and 1 only → covered=2, total=3.
			require.NoError(t, textsRepo.BatchUpsert(ctx, []people.PersonText{
				{PersonID: pids[0], Language: "ru-RU", Name: new("Альфа")},
				{PersonID: pids[1], Language: "ru-RU", Name: new("Браво")},
			}))

			covered, total, err := textsRepo.CastNameCoverage(ctx, seriesID, "ru-RU")
			require.NoError(t, err)
			assert.Equal(t, 2, covered)
			assert.Equal(t, 3, total)

			// A series with no cast credits → (0, 0, nil).
			emptySeriesID, err := seriesRepo.UpsertStub(ctx, series.Canon{
				TMDBID:           ptrTMDBID(700999),
				Hydration:        series.HydrationStub,
				OriginalTitle:    new("No Cast Show"),
				OriginalLanguage: new("en"),
			})
			require.NoError(t, err)
			covered, total, err = textsRepo.CastNameCoverage(ctx, emptySeriesID, "ru-RU")
			require.NoError(t, err)
			assert.Equal(t, 0, covered)
			assert.Equal(t, 0, total)

			// A people_texts row whose name IS NULL is NOT counted as covered.
			require.NoError(t, textsRepo.BatchUpsert(ctx, []people.PersonText{
				{PersonID: pids[2], Language: "ru-RU", Name: nil},
			}))
			covered, total, err = textsRepo.CastNameCoverage(ctx, seriesID, "ru-RU")
			require.NoError(t, err)
			assert.Equal(t, 2, covered, "NULL-name people_texts row must not count as covered")
			assert.Equal(t, 3, total)
		})
	}
}
