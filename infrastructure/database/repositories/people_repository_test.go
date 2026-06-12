package repositories

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/application/ports"
	"github.com/alexmorbo/seasonfill/domain/people"
)

func samplePerson(name string) people.Person {
	return people.Person{
		Name:               name,
		Hydration:          people.HydrationStub,
		TMDBID:             ptrInt(7001),
		IMDBID:             ptrString("nm0000001"),
		OriginalName:       ptrString("orig: " + name),
		Gender:             ptrInt(2),
		KnownForDepartment: ptrString("Acting"),
		Popularity:         ptrFloat64(12.5),
	}
}

func TestPeopleRepository_UpsertInsertAndGet(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
	repo := NewPeopleRepository(db)
	ctx := context.Background()

	id, err := repo.Upsert(ctx, samplePerson("Pedro Pascal"))
	require.NoError(t, err)
	require.NotZero(t, id)

	got, err := repo.Get(ctx, id, "en-US")
	require.NoError(t, err)
	assert.Equal(t, "Pedro Pascal", got.Name)
	assert.Equal(t, people.HydrationStub, got.Hydration)
	require.NotNil(t, got.TMDBID)
	assert.Equal(t, 7001, *got.TMDBID)
	assert.Empty(t, got.Biography)
	assert.Empty(t, got.BiographyLanguage)
}

func TestPeopleRepository_Get_NotFound(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
	repo := NewPeopleRepository(db)
	_, err := repo.Get(context.Background(), 9999, "en-US")
	require.True(t, errors.Is(err, ports.ErrNotFound))
}

func TestPeopleRepository_Upsert_Idempotent(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
	repo := NewPeopleRepository(db)
	ctx := context.Background()

	first := samplePerson("Florence Pugh")
	id1, err := repo.Upsert(ctx, first)
	require.NoError(t, err)
	got1, err := repo.Get(ctx, id1, "en-US")
	require.NoError(t, err)

	id2, err := repo.Upsert(ctx, first)
	require.NoError(t, err)
	assert.Equal(t, id1, id2, "natural-key upsert must resolve to the same id")

	got2, err := repo.Get(ctx, id2, "en-US")
	require.NoError(t, err)
	assert.Equal(t, got1.Name, got2.Name)
	assert.Equal(t, got1.CreatedAt.Unix(), got2.CreatedAt.Unix(),
		"created_at must NOT shift on a no-op upsert")
	assert.True(t, !got2.UpdatedAt.Before(got1.UpdatedAt))
}

func TestPeopleRepository_GetByTMDBID(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
	repo := NewPeopleRepository(db)
	ctx := context.Background()

	_, err := repo.Upsert(ctx, samplePerson("Cillian Murphy"))
	require.NoError(t, err)

	got, err := repo.GetByTMDBID(ctx, 7001)
	require.NoError(t, err)
	assert.Equal(t, "Cillian Murphy", got.Name)

	_, err = repo.GetByTMDBID(ctx, 9999)
	assert.True(t, errors.Is(err, ports.ErrNotFound))
}

func TestPeopleRepository_ListByIDs(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
	repo := NewPeopleRepository(db)
	ctx := context.Background()

	a := samplePerson("Actor A")
	a.TMDBID = ptrInt(1001)
	id1, err := repo.Upsert(ctx, a)
	require.NoError(t, err)

	b := samplePerson("Actor B")
	b.TMDBID = ptrInt(1002)
	id2, err := repo.Upsert(ctx, b)
	require.NoError(t, err)

	rows, err := repo.ListByIDs(ctx, []int64{id1, id2, 99999})
	require.NoError(t, err)
	require.Len(t, rows, 2, "missing ids are silently skipped")
	assert.Equal(t, id1, rows[0].ID)
	assert.Equal(t, id2, rows[1].ID)
}

// TestPeopleRepository_Upsert_PreservesFullHydration covers the
// stub-downgrade defence: a series_enrichment_worker stub upsert
// over an existing full row must NOT clobber hydration back to stub.
func TestPeopleRepository_Upsert_PreservesFullHydration(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
	repo := NewPeopleRepository(db)
	ctx := context.Background()

	full := samplePerson("Pascal")
	full.Hydration = people.HydrationFull
	_, err := repo.Upsert(ctx, full)
	require.NoError(t, err)

	stub := samplePerson("Pascal Updated")
	stub.Hydration = people.HydrationStub
	id, err := repo.Upsert(ctx, stub)
	require.NoError(t, err)

	got, err := repo.Get(ctx, id, "en-US")
	require.NoError(t, err)
	assert.Equal(t, people.HydrationFull, got.Hydration,
		"stub upsert MUST NOT downgrade a full-hydrated row")
	assert.Equal(t, "Pascal Updated", got.Name,
		"non-hydration fields still merge")
}

func TestPeopleRepository_Upsert_PartialUnique(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
	repo := NewPeopleRepository(db)
	ctx := context.Background()

	a := samplePerson("Orphan A")
	a.TMDBID = nil
	a.IMDBID = ptrString("nm9000001")
	id1, err := repo.Upsert(ctx, a)
	require.NoError(t, err)

	b := samplePerson("Orphan B")
	b.TMDBID = nil
	b.IMDBID = ptrString("nm9000002")
	id2, err := repo.Upsert(ctx, b)
	require.NoError(t, err)
	assert.NotEqual(t, id1, id2,
		"two NULL-tmdb rows must coexist — partial index excludes them")
}

// TestPeopleRepository_Get_ResolvesBiographyViaFallback proves the
// people.Get path JOINs through the shared §5.6 helper.
func TestPeopleRepository_Get_ResolvesBiographyViaFallback(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
	ctx := context.Background()
	repo := NewPeopleRepository(db)
	bioRepo := NewPersonBiographiesRepository(db)

	id, err := repo.Upsert(ctx, samplePerson("Pedro Pascal"))
	require.NoError(t, err)
	require.NoError(t, bioRepo.Upsert(ctx, people.PersonBiography{
		PersonID:  id,
		Language:  "en-US",
		Biography: ptrString("Chilean-American actor."),
	}))

	// Request ru-RU — only en-US row exists, helper returns en-US.
	got, err := repo.Get(ctx, id, "ru-RU")
	require.NoError(t, err)
	assert.Equal(t, "en-US", got.BiographyLanguage)
	assert.Equal(t, "Chilean-American actor.", got.Biography)
}

func ptrFloat64(v float64) *float64 { return &v }
