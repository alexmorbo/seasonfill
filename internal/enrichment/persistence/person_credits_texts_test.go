package persistence

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/internal/enrichment/domain/people"
	"github.com/alexmorbo/seasonfill/internal/shared/testhelpers"
)

// TestPersonCreditsTextsRepository_D0 — S-G dual-backend suite. Exercises the
// real ON CONFLICT COALESCE upsert on the (person_credit_id, language)
// composite key across SQLite + testcontainers Postgres. Covers happy insert
// both langs, COALESCE-preserve nil write, validation error pairs, empty-input
// no-op, and the FK error path.
func TestPersonCreditsTextsRepository_D0(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()

			seed := func(t *testing.T) (int64, *PersonCreditsTextsRepository) {
				t.Helper()
				db := backend.NewDB(t)
				personID, err := NewPeopleRepository(db).Upsert(ctx, samplePerson("Cast Localize"))
				require.NoError(t, err)
				creditID, err := NewPersonCreditsRepository(db).
					Upsert(ctx, samplePersonCredit(personID, "credit-sg-001", "R&M", 100))
				require.NoError(t, err)
				require.NotZero(t, creditID)
				return creditID, NewPersonCreditsTextsRepository(db)
			}

			readCharacterName := func(t *testing.T, repo *PersonCreditsTextsRepository, creditID int64, lang string) (string, bool) {
				t.Helper()
				var name *string
				row := repo.db.WithContext(ctx).
					Raw("SELECT character_name FROM person_credits_texts WHERE person_credit_id = ? AND language = ?", creditID, lang).
					Row()
				if err := row.Scan(&name); err != nil {
					return "", false
				}
				if name == nil {
					return "", true
				}
				return *name, true
			}

			t.Run("batch_upsert_both_langs", func(t *testing.T) {
				creditID, repo := seed(t)
				require.NoError(t, repo.BatchUpsert(ctx, []people.PersonCreditText{
					{PersonCreditID: creditID, Language: "en-US", CharacterName: new("Rick")},
					{PersonCreditID: creditID, Language: "ru-RU", CharacterName: new("Рик")},
				}))
				en, ok := readCharacterName(t, repo, creditID, "en-US")
				require.True(t, ok)
				assert.Equal(t, "Rick", en)
				ru, ok := readCharacterName(t, repo, creditID, "ru-RU")
				require.True(t, ok)
				assert.Equal(t, "Рик", ru)
			})

			// COALESCE regression: a later nil write must NOT wipe the stored value.
			t.Run("coalesce_preserve_on_nil_write", func(t *testing.T) {
				creditID, repo := seed(t)
				require.NoError(t, repo.Upsert(ctx, people.PersonCreditText{
					PersonCreditID: creditID, Language: "ru-RU", CharacterName: new("Рик"),
				}))
				require.NoError(t, repo.Upsert(ctx, people.PersonCreditText{
					PersonCreditID: creditID, Language: "ru-RU", CharacterName: nil,
				}))
				ru, ok := readCharacterName(t, repo, creditID, "ru-RU")
				require.True(t, ok)
				assert.Equal(t, "Рик", ru, "nil write must not wipe existing value")
			})

			t.Run("validation_zero_person_credit_id", func(t *testing.T) {
				_, repo := seed(t)
				err := repo.Upsert(ctx, people.PersonCreditText{
					PersonCreditID: 0, Language: "en-US", CharacterName: new("X"),
				})
				require.Error(t, err)
			})

			t.Run("validation_empty_language", func(t *testing.T) {
				creditID, repo := seed(t)
				err := repo.Upsert(ctx, people.PersonCreditText{
					PersonCreditID: creditID, Language: "", CharacterName: new("X"),
				})
				require.Error(t, err)
			})

			t.Run("empty_input_is_noop", func(t *testing.T) {
				_, repo := seed(t)
				require.NoError(t, repo.BatchUpsert(ctx, nil))
			})

			t.Run("fk_orphan_person_credit_id_errors", func(t *testing.T) {
				// The unit-test SQLite harness opens :memory: without
				// foreign_keys enforcement, so an orphan INSERT does not
				// raise there — gate to postgres (native enforcement). The
				// s_g_person_credits_texts_apply integration test covers the
				// sqlite CASCADE/FK path with _pragma=foreign_keys(1) on.
				if backend.Name != "postgres" {
					t.Skip("sqlite unit harness has FK enforcement off")
				}
				_, repo := seed(t)
				err := repo.BatchUpsert(ctx, []people.PersonCreditText{
					{PersonCreditID: 999999, Language: "en-US", CharacterName: new("Orphan")},
				})
				require.Error(t, err)
			})
		})
	}
}
