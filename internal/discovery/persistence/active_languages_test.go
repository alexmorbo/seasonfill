package persistence

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	database "github.com/alexmorbo/seasonfill/internal/shared/db"
	"github.com/alexmorbo/seasonfill/internal/shared/locale"
	"github.com/alexmorbo/seasonfill/internal/shared/testhelpers"
)

// uniqueUsername returns a UUID-derived username for fixture isolation —
// the users.username column has a UNIQUE index so naive duplicates
// fail across parallel subtests.
func uniqueUsername(prefix string) string {
	return prefix + "-" + uuid.NewString()[:8]
}

func TestActiveLanguages_EmptyUsersReturnsAllSupported(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			repo := NewActiveLanguagesRepository(db)

			got, err := repo.ActiveLanguages(context.Background())
			require.NoError(t, err)
			assert.ElementsMatch(t, locale.SupportedUserLanguages, got,
				"empty users → supported-language floor {en-US, ru-RU}")
		})
	}
}

func TestActiveLanguages_DistinctUserLanguagesAreReturned(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			repo := NewActiveLanguagesRepository(db)
			ctx := context.Background()

			ruRU := "ru-RU"
			jaJP := "ja-JP"
			enUS := "en-US"
			for _, lang := range []string{ruRU, jaJP, enUS} {
				m := database.UserModel{
					Username:          uniqueUsername("u"),
					Role:              "admin",
					AvatarMode:        "auto",
					PreferredLanguage: &lang,
				}
				require.NoError(t, db.Create(&m).Error)
			}

			got, err := repo.ActiveLanguages(ctx)
			require.NoError(t, err)
			// ja-JP exotic pref + ru-RU/en-US (which coincide with the
			// supported floor) → 3 distinct langs, deduped.
			assert.ElementsMatch(t,
				[]string{"en-US", "ja-JP", "ru-RU"}, got,
				"3 distinct user prefs deduped against supported floor")
		})
	}
}

func TestActiveLanguages_NullPreferredLanguageExcluded(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			repo := NewActiveLanguagesRepository(db)
			ctx := context.Background()

			// NULL preferred_language must NOT appear in the set.
			nullUser := database.UserModel{
				Username:   uniqueUsername("null"),
				Role:       "admin",
				AvatarMode: "auto",
				// PreferredLanguage left nil.
			}
			require.NoError(t, db.Create(&nullUser).Error)

			// Empty-string preferred_language also excluded (the WHERE
			// clause filters '' alongside NULL).
			emptyLang := ""
			emptyUser := database.UserModel{
				Username:          uniqueUsername("empty"),
				Role:              "admin",
				AvatarMode:        "auto",
				PreferredLanguage: &emptyLang,
			}
			require.NoError(t, db.Create(&emptyUser).Error)

			got, err := repo.ActiveLanguages(ctx)
			require.NoError(t, err)
			assert.ElementsMatch(t, locale.SupportedUserLanguages, got,
				"NULL + empty preferred_language excluded; supported floor remains")
		})
	}
}

func TestActiveLanguages_SupportedFloorIncludedForExoticPrefOnly(t *testing.T) {
	t.Parallel()
	for _, backend := range testhelpers.AllBackends(t) {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			db := backend.NewDB(t)
			repo := NewActiveLanguagesRepository(db)
			ctx := context.Background()

			// A single user whose ONLY pref is exotic (ja-JP) — no
			// ru-RU/en-US user exists. The supported floor must still be
			// warmed, and the exotic pref is additive on top.
			jaJP := "ja-JP"
			m := database.UserModel{
				Username:          uniqueUsername("exotic"),
				Role:              "admin",
				AvatarMode:        "auto",
				PreferredLanguage: &jaJP,
			}
			require.NoError(t, db.Create(&m).Error)

			got, err := repo.ActiveLanguages(ctx)
			require.NoError(t, err)
			assert.ElementsMatch(t,
				[]string{"en-US", "ja-JP", "ru-RU"}, got,
				"supported floor {en-US, ru-RU} always warmed; exotic ja-JP additive")
		})
	}
}
