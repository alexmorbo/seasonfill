package repositories

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/application/ports"
	"github.com/alexmorbo/seasonfill/domain/people"
)

func TestSeriesPeopleRepository_UpsertAndGet(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
	ctx := context.Background()
	seriesID, err := NewSeriesRepository(db).Upsert(ctx, sampleCanon("Andor"))
	require.NoError(t, err)
	personID, err := NewPeopleRepository(db).Upsert(ctx, samplePerson("Diego Luna"))
	require.NoError(t, err)
	repo := NewSeriesPeopleRepository(db)

	id, err := repo.Upsert(ctx, people.SeriesCredit{
		SeriesID:      seriesID,
		PersonID:      personID,
		Kind:          people.SeriesCreditCast,
		TMDBCreditID:  "5fbf6d3d1c4b5b00415d3b4f",
		CharacterName: ptrString("Cassian Andor"),
		CreditOrder:   ptrInt(0),
		EpisodeCount:  ptrInt(12),
	})
	require.NoError(t, err)
	require.NotZero(t, id)

	got, err := repo.Get(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, seriesID, got.SeriesID)
	assert.Equal(t, personID, got.PersonID)
	assert.Equal(t, people.SeriesCreditCast, got.Kind)
	require.NotNil(t, got.CharacterName)
	assert.Equal(t, "Cassian Andor", *got.CharacterName)
}

func TestSeriesPeopleRepository_Get_NotFound(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
	repo := NewSeriesPeopleRepository(db)
	_, err := repo.Get(context.Background(), 9999)
	assert.True(t, errors.Is(err, ports.ErrNotFound))
}

// TestSeriesPeopleRepository_BatchUpsert_Idempotent covers the
// 50-row acceptance criterion: ONE INSERT, ids round-trip on
// re-batch, no duplicate rows.
func TestSeriesPeopleRepository_BatchUpsert_Idempotent(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
	ctx := context.Background()
	seriesID, err := NewSeriesRepository(db).Upsert(ctx, sampleCanon("Foundation"))
	require.NoError(t, err)
	peopleRepo := NewPeopleRepository(db)
	repo := NewSeriesPeopleRepository(db)

	const n = 50
	credits := make([]people.SeriesCredit, n)
	for i := 0; i < n; i++ {
		p := samplePerson(fmt.Sprintf("Cast %02d", i))
		p.TMDBID = ptrInt(8000 + i)
		personID, err := peopleRepo.Upsert(ctx, p)
		require.NoError(t, err)
		credits[i] = people.SeriesCredit{
			SeriesID:     seriesID,
			PersonID:     personID,
			Kind:         people.SeriesCreditCast,
			TMDBCreditID: fmt.Sprintf("credit-%02d", i),
			CreditOrder:  ptrInt(i),
		}
	}

	ids, err := repo.BatchUpsert(ctx, credits)
	require.NoError(t, err)
	require.Len(t, ids, n)

	// Re-batch with the same payload — same ids, no new rows.
	ids2, err := repo.BatchUpsert(ctx, credits)
	require.NoError(t, err)
	require.Equal(t, ids, ids2,
		"second batch must resolve to the same ids by natural key")

	rows, err := repo.ListBySeries(ctx, seriesID, "")
	require.NoError(t, err)
	assert.Len(t, rows, n,
		"idempotent re-batch must NOT produce duplicate rows")
}

func TestSeriesPeopleRepository_ListBySeries_KindFilter(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
	ctx := context.Background()
	seriesID, err := NewSeriesRepository(db).Upsert(ctx, sampleCanon("Severance"))
	require.NoError(t, err)
	personID, err := NewPeopleRepository(db).Upsert(ctx, samplePerson("Adam Scott"))
	require.NoError(t, err)
	repo := NewSeriesPeopleRepository(db)

	_, err = repo.Upsert(ctx, people.SeriesCredit{
		SeriesID: seriesID, PersonID: personID,
		Kind: people.SeriesCreditCast, TMDBCreditID: "c1", CreditOrder: ptrInt(0),
	})
	require.NoError(t, err)
	_, err = repo.Upsert(ctx, people.SeriesCredit{
		SeriesID:     seriesID,
		PersonID:     personID,
		Kind:         people.SeriesCreditCrew,
		TMDBCreditID: "c2",
		Department:   ptrString("Production"),
		Job:          ptrString("Executive Producer"),
	})
	require.NoError(t, err)

	cast, err := repo.ListBySeries(ctx, seriesID, people.SeriesCreditCast)
	require.NoError(t, err)
	assert.Len(t, cast, 1)
	assert.Equal(t, people.SeriesCreditCast, cast[0].Kind)

	crew, err := repo.ListBySeries(ctx, seriesID, people.SeriesCreditCrew)
	require.NoError(t, err)
	assert.Len(t, crew, 1)

	both, err := repo.ListBySeries(ctx, seriesID, "")
	require.NoError(t, err)
	assert.Len(t, both, 2)
}

// TestSeriesPeopleRepository_ListByPerson_ReverseLookup exercises
// the H-2 "Also in your library" reverse lookup against the
// dedicated series_people_person index.
func TestSeriesPeopleRepository_ListByPerson_ReverseLookup(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
	ctx := context.Background()
	seriesRepo := NewSeriesRepository(db)
	peopleRepo := NewPeopleRepository(db)
	repo := NewSeriesPeopleRepository(db)

	personID, err := peopleRepo.Upsert(ctx, samplePerson("Pedro Pascal"))
	require.NoError(t, err)

	titles := []string{"The Last of Us", "The Mandalorian", "Narcos"}
	expectedSeriesIDs := make([]int64, 0, len(titles))
	for i, title := range titles {
		c := sampleCanon(title)
		c.TMDBID = ptrInt(20000 + i)
		sid, err := seriesRepo.Upsert(ctx, c)
		require.NoError(t, err)
		expectedSeriesIDs = append(expectedSeriesIDs, sid)
		_, err = repo.Upsert(ctx, people.SeriesCredit{
			SeriesID:     sid,
			PersonID:     personID,
			Kind:         people.SeriesCreditCast,
			TMDBCreditID: fmt.Sprintf("credit-%d", i),
			CreditOrder:  ptrInt(0),
		})
		require.NoError(t, err)
	}

	// An unrelated person on one of the series — must NOT appear in
	// the reverse lookup.
	otherPerson := samplePerson("Other")
	otherPerson.TMDBID = ptrInt(30000)
	otherID, err := peopleRepo.Upsert(ctx, otherPerson)
	require.NoError(t, err)
	_, err = repo.Upsert(ctx, people.SeriesCredit{
		SeriesID:     expectedSeriesIDs[0],
		PersonID:     otherID,
		Kind:         people.SeriesCreditCast,
		TMDBCreditID: "credit-other",
	})
	require.NoError(t, err)

	rows, err := repo.ListByPerson(ctx, personID)
	require.NoError(t, err)
	require.Len(t, rows, 3,
		"reverse lookup must return one row per series with this person")
	for _, row := range rows {
		assert.Equal(t, personID, row.PersonID)
	}
}
