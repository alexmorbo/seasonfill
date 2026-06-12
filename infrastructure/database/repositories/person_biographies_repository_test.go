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

// TestPersonBiographiesRepository_FallbackThreeScenarios covers the
// §5.6 pattern on person_biographies — proves the shared
// pickLanguageFallback helper (introduced in story 203) works
// unchanged against a new table by parameterising table + entityCol.
func TestPersonBiographiesRepository_FallbackThreeScenarios(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name      string
		seed      []people.PersonBiography
		requested string
		wantLang  string
		wantText  string
	}{
		{
			name: "requested language present",
			seed: []people.PersonBiography{
				{Language: "ru-RU", Biography: ptrString("Чилийско-американский актёр.")},
				{Language: "en-US", Biography: ptrString("Chilean-American actor.")},
			},
			requested: "ru-RU",
			wantLang:  "ru-RU",
			wantText:  "Чилийско-американский актёр.",
		},
		{
			name: "requested missing, en-US fallback",
			seed: []people.PersonBiography{
				{Language: "en-US", Biography: ptrString("Chilean-American actor.")},
			},
			requested: "ru-RU",
			wantLang:  "en-US",
			wantText:  "Chilean-American actor.",
		},
		{
			name: "requested and en-US missing, first available wins",
			seed: []people.PersonBiography{
				{Language: "fr-FR", Biography: ptrString("Acteur chilo-américain.")},
				{Language: "de-DE", Biography: ptrString("Chilenisch-amerikanischer Schauspieler.")},
			},
			requested: "ru-RU",
			wantLang:  "de-DE",
			wantText:  "Chilenisch-amerikanischer Schauspieler.",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			db := setupTestDB(t)
			ctx := context.Background()
			repo := NewPersonBiographiesRepository(db)
			personID, err := NewPeopleRepository(db).Upsert(ctx, samplePerson("Pedro Pascal"))
			require.NoError(t, err)
			for _, row := range tc.seed {
				row.PersonID = personID
				require.NoError(t, repo.Upsert(ctx, row))
			}
			got, err := repo.GetWithFallback(ctx, personID, tc.requested)
			require.NoError(t, err)
			assert.Equal(t, tc.wantLang, got.Language)
			require.NotNil(t, got.Biography)
			assert.Equal(t, tc.wantText, *got.Biography)
		})
	}
}

func TestPersonBiographiesRepository_Fallback_NoRows(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
	ctx := context.Background()
	personID, err := NewPeopleRepository(db).Upsert(ctx, samplePerson("Pedro Pascal"))
	require.NoError(t, err)
	repo := NewPersonBiographiesRepository(db)
	_, err = repo.GetWithFallback(ctx, personID, "ru-RU")
	assert.True(t, errors.Is(err, ports.ErrNotFound))
}

func TestPersonBiographiesRepository_Upsert_Idempotent(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
	ctx := context.Background()
	personID, err := NewPeopleRepository(db).Upsert(ctx, samplePerson("Florence Pugh"))
	require.NoError(t, err)
	repo := NewPersonBiographiesRepository(db)

	bio := people.PersonBiography{
		PersonID:  personID,
		Language:  "en-US",
		Biography: ptrString("English actress."),
	}
	require.NoError(t, repo.Upsert(ctx, bio))
	require.NoError(t, repo.Upsert(ctx, bio))

	got, err := repo.Get(ctx, personID, "en-US")
	require.NoError(t, err)
	require.NotNil(t, got.Biography)
	assert.Equal(t, "English actress.", *got.Biography)
}
