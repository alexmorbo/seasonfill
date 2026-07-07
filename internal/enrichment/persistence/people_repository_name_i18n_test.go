package persistence

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/alexmorbo/seasonfill/internal/enrichment/domain/people"
	"github.com/alexmorbo/seasonfill/internal/shared/testhelpers"
)

// TestPeopleRepository_ListByIDsWithNameFallback_D0 — Story 1083 reader suite.
// Exercises the requested-lang → en-US → original_name → name fallback against a
// real DB (SQLite + testcontainers Postgres) via two LEFT JOINs + COALESCE.
func TestPeopleRepository_ListByIDsWithNameFallback_D0(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()

			// seed installs ONE person with the given base people.name +
			// original_name, plus any people_texts rows in seedTexts. Returns the
			// repo + the assigned person id.
			seed := func(t *testing.T, baseName string, originalName *string, seedTexts []people.PersonText) (*PeopleRepository, int64) {
				t.Helper()
				db := backend.NewDB(t)
				repo := NewPeopleRepository(db)
				p := people.Person{
					Name:         baseName,
					OriginalName: originalName,
					Hydration:    people.HydrationStub,
					TMDBID:       ptrTMDBID(9100),
				}
				pid, err := repo.Upsert(ctx, p)
				require.NoError(t, err)
				require.Greater(t, pid, int64(0))
				if len(seedTexts) > 0 {
					for i := range seedTexts {
						seedTexts[i].PersonID = pid
					}
					require.NoError(t, NewPeopleTextsRepository(db).BatchUpsert(ctx, seedTexts))
				}
				return repo, pid
			}

			// Case A — the exact bug: people.name = Cyrillic (last ru-RU pass),
			// original_name = Latin, en-US texts row present. lang=en-US → Latin.
			t.Run("en_us_resolves_latin", func(t *testing.T) {
				repo, pid := seed(t, "Адам Скотт", new("Adam Scott"), []people.PersonText{
					{Language: "en-US", Name: new("Adam Scott")},
					{Language: "ru-RU", Name: new("Адам Скотт")},
				})
				rows, err := repo.ListByIDsWithNameFallback(ctx, []int64{pid}, "en-US")
				require.NoError(t, err)
				require.Len(t, rows, 1)
				assert.Equal(t, "Adam Scott", rows[0].Name)
			})

			// Case B — same person, lang=ru-RU → Cyrillic.
			t.Run("ru_ru_resolves_cyrillic", func(t *testing.T) {
				repo, pid := seed(t, "Adam Scott", new("Adam Scott"), []people.PersonText{
					{Language: "en-US", Name: new("Adam Scott")},
					{Language: "ru-RU", Name: new("Адам Скотт")},
				})
				rows, err := repo.ListByIDsWithNameFallback(ctx, []int64{pid}, "ru-RU")
				require.NoError(t, err)
				require.Len(t, rows, 1)
				assert.Equal(t, "Адам Скотт", rows[0].Name)
			})

			// Case C — only en-US texts row; ru-RU request falls back to en-US.
			t.Run("only_en_texts_ru_falls_back_to_en", func(t *testing.T) {
				repo, pid := seed(t, "base-name", new("orig-name"), []people.PersonText{
					{Language: "en-US", Name: new("Adam Scott")},
				})
				rows, err := repo.ListByIDsWithNameFallback(ctx, []int64{pid}, "ru-RU")
				require.NoError(t, err)
				require.Len(t, rows, 1)
				assert.Equal(t, "Adam Scott", rows[0].Name)
			})

			// Case D — no texts rows; original_name (Latin) beats the Cyrillic
			// base name for an en-US request. This heals the common western-actor
			// case even before an en-US RefreshCast pass populates people_texts.
			t.Run("no_texts_falls_back_to_original_name", func(t *testing.T) {
				repo, pid := seed(t, "Адам Скотт", new("Adam Scott"), nil)
				rows, err := repo.ListByIDsWithNameFallback(ctx, []int64{pid}, "en-US")
				require.NoError(t, err)
				require.Len(t, rows, 1)
				assert.Equal(t, "Adam Scott", rows[0].Name)
			})

			// Case E — no texts rows AND original_name NULL → people.name (base).
			t.Run("no_texts_null_original_falls_back_to_name", func(t *testing.T) {
				repo, pid := seed(t, "Only Name", nil, nil)
				rows, err := repo.ListByIDsWithNameFallback(ctx, []int64{pid}, "ru-RU")
				require.NoError(t, err)
				require.Len(t, rows, 1)
				assert.Equal(t, "Only Name", rows[0].Name)
			})

			// Case F — lang=="" defaults to the en-US tier.
			t.Run("empty_lang_defaults_to_en", func(t *testing.T) {
				repo, pid := seed(t, "base", new("orig"), []people.PersonText{
					{Language: "en-US", Name: new("Adam Scott")},
					{Language: "ru-RU", Name: new("Адам Скотт")},
				})
				rows, err := repo.ListByIDsWithNameFallback(ctx, []int64{pid}, "")
				require.NoError(t, err)
				require.Len(t, rows, 1)
				assert.Equal(t, "Adam Scott", rows[0].Name)
			})

			// Case G — empty id list is a no-op.
			t.Run("empty_ids_returns_nil", func(t *testing.T) {
				repo, _ := seed(t, "x", nil, nil)
				rows, err := repo.ListByIDsWithNameFallback(ctx, nil, "en-US")
				require.NoError(t, err)
				assert.Empty(t, rows)
			})
		})
	}
}
