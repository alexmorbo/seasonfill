package persistence

import (
	"context"
	"fmt"
	"sort"

	"gorm.io/gorm"
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
// The "en-US" fallback ensures the worker always refreshes at least the
// English list, even on a freshly-installed instance with no users.
type ActiveLanguagesRepository struct {
	db *gorm.DB
}

// NewActiveLanguagesRepository binds the repo to db.
func NewActiveLanguagesRepository(db *gorm.DB) *ActiveLanguagesRepository {
	return &ActiveLanguagesRepository{db: db}
}

// ActiveLanguages returns the distinct set of users.preferred_language
// values (excluding NULL + empty string) UNIONed with "en-US",
// sorted ascending for deterministic output.
//
// The UNION expresses "en-US" as a literal so the fallback is folded
// into the result set in a single round trip — separate query + slice
// append would race against a concurrent DELETE FROM users in a way
// the worker doesn't need to think about.
func (r *ActiveLanguagesRepository) ActiveLanguages(ctx context.Context) ([]string, error) {
	const q = `SELECT DISTINCT preferred_language AS lang
	             FROM users
	            WHERE preferred_language IS NOT NULL
	              AND preferred_language <> ''
	            UNION
	           SELECT 'en-US' AS lang`

	var langs []string
	if err := r.db.WithContext(ctx).Raw(q).Scan(&langs).Error; err != nil {
		return nil, fmt.Errorf("active languages: %w", err)
	}
	sort.Strings(langs)
	return langs, nil
}
