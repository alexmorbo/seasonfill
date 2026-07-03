package persistence

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/internal/enrichment/domain/people"
	"github.com/alexmorbo/seasonfill/internal/shared/testhelpers"
)

// TestPersonCreditsRepository_ListByMediaWithTextFallback_D0 — S-G reader
// suite. Exercises the requested-lang → en-US → base character_name fallback
// against a real DB (SQLite + testcontainers Postgres) via two LEFT JOINs +
// COALESCE.
func TestPersonCreditsRepository_ListByMediaWithTextFallback_D0(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()

			// seed installs one tv credit (tmdb_media_id=100) whose base
			// character_name is baseName, plus any texts rows in seedTexts.
			seed := func(t *testing.T, baseName string, seedTexts []people.PersonCreditText) *PersonCreditsRepository {
				t.Helper()
				db := backend.NewDB(t)
				personID, err := NewPeopleRepository(db).Upsert(ctx, samplePerson("Reader Actor"))
				require.NoError(t, err)
				credit := samplePersonCredit(personID, "credit-i18n-1", "R&M", 100)
				credit.CharacterName = new(baseName)
				pcRepo := NewPersonCreditsRepository(db)
				creditID, err := pcRepo.Upsert(ctx, credit)
				require.NoError(t, err)
				if len(seedTexts) > 0 {
					for i := range seedTexts {
						seedTexts[i].PersonCreditID = creditID
					}
					require.NoError(t, NewPersonCreditsTextsRepository(db).BatchUpsert(ctx, seedTexts))
				}
				return pcRepo
			}

			// Case A — ru exists → returns ru.
			t.Run("ru_present_returns_ru", func(t *testing.T) {
				repo := seed(t, "Morty", []people.PersonCreditText{
					{Language: "en-US", CharacterName: new("Rick")},
					{Language: "ru-RU", CharacterName: new("Рик")},
				})
				rows, err := repo.ListByMediaWithTextFallback(ctx, "tv", 100, "ru-RU")
				require.NoError(t, err)
				require.Len(t, rows, 1)
				require.NotNil(t, rows[0].CharacterName)
				assert.Equal(t, "Рик", *rows[0].CharacterName)
			})

			// Case B — only en-US exists → ru request falls back to en-US.
			t.Run("only_en_returns_en_fallback", func(t *testing.T) {
				repo := seed(t, "Morty", []people.PersonCreditText{
					{Language: "en-US", CharacterName: new("Rick")},
				})
				rows, err := repo.ListByMediaWithTextFallback(ctx, "tv", 100, "ru-RU")
				require.NoError(t, err)
				require.Len(t, rows, 1)
				require.NotNil(t, rows[0].CharacterName)
				assert.Equal(t, "Rick", *rows[0].CharacterName)
			})

			// Case C — no texts rows → base person_credits.character_name.
			t.Run("no_texts_returns_base", func(t *testing.T) {
				repo := seed(t, "Morty", nil)
				rows, err := repo.ListByMediaWithTextFallback(ctx, "tv", 100, "ru-RU")
				require.NoError(t, err)
				require.Len(t, rows, 1)
				require.NotNil(t, rows[0].CharacterName)
				assert.Equal(t, "Morty", *rows[0].CharacterName)
			})

			// Case D — lang=="" defaults to en-US tier.
			t.Run("empty_lang_defaults_to_en", func(t *testing.T) {
				repo := seed(t, "Morty", []people.PersonCreditText{
					{Language: "en-US", CharacterName: new("Rick")},
					{Language: "ru-RU", CharacterName: new("Рик")},
				})
				rows, err := repo.ListByMediaWithTextFallback(ctx, "tv", 100, "")
				require.NoError(t, err)
				require.Len(t, rows, 1)
				require.NotNil(t, rows[0].CharacterName)
				assert.Equal(t, "Rick", *rows[0].CharacterName)
			})

			// NULL/error pair — empty media_type → error.
			t.Run("empty_media_type_errors", func(t *testing.T) {
				repo := seed(t, "Morty", nil)
				_, err := repo.ListByMediaWithTextFallback(ctx, "", 100, "ru-RU")
				require.Error(t, err)
			})
		})
	}
}
