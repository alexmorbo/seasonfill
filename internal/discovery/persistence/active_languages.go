package persistence

import (
	"context"
	"fmt"
	"sort"

	"gorm.io/gorm"

	"github.com/alexmorbo/seasonfill/internal/shared/locale"
)

// ActiveLanguagesRepository implements app.ActiveLanguagesProvider against
// the D-1 `users` table.
//
// PRD §5.1.1 originally specified
//
//	SELECT DISTINCT value FROM user_settings WHERE key='preferred_language'
//
// but D-1-7a collapsed user_settings into the `users` table (see
// internal/shared/db/models.go:218 godoc) — the equivalent live query
// reads users.preferred_language directly.
//
// locale.SupportedUserLanguages is the FLOOR: every supported language is
// always warmed regardless of user prefs, so a freshly-installed instance
// whose only admin prefers en-US still refreshes ru-RU. Exotic user prefs
// (e.g. ja-JP) are additive on top of that floor.
type ActiveLanguagesRepository struct {
	db *gorm.DB
}

// NewActiveLanguagesRepository binds the repo to db.
func NewActiveLanguagesRepository(db *gorm.DB) *ActiveLanguagesRepository {
	return &ActiveLanguagesRepository{db: db}
}

// ActiveLanguages returns the distinct set of users.preferred_language
// values (excluding NULL + empty string) merged with the supported-language
// floor locale.SupportedUserLanguages, deduped and sorted ascending for
// deterministic output.
//
// The DB query returns only the distinct user prefs; the supported-language
// floor is merged in Go so that every supported language is warmed even on a
// freshly-installed instance with no matching user pref.
func (r *ActiveLanguagesRepository) ActiveLanguages(ctx context.Context) ([]string, error) {
	const q = `SELECT DISTINCT preferred_language AS lang
		             FROM users
		            WHERE preferred_language IS NOT NULL
		              AND preferred_language <> ''`

	var langs []string
	if err := r.db.WithContext(ctx).Raw(q).Scan(&langs).Error; err != nil {
		return nil, fmt.Errorf("active languages: %w", err)
	}

	set := make(map[string]struct{}, len(langs)+len(locale.SupportedUserLanguages))
	for _, lang := range langs {
		set[lang] = struct{}{}
	}
	for _, lang := range locale.SupportedUserLanguages {
		set[lang] = struct{}{}
	}

	merged := make([]string, 0, len(set))
	for lang := range set {
		merged = append(merged, lang)
	}
	sort.Strings(merged)
	return merged, nil
}
