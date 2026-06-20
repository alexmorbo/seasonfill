package persistence

import (
	"context"
	"fmt"

	"gorm.io/gorm"
)

// fallbackLanguage is the PRD-mandated default — used both as the
// secondary read target by the §5.6 helper and as the contract the
// TMDB worker writes alongside the requested language row.
const fallbackLanguage = "en-US"

// pickLanguageFallback applies the §5.6 fallback: prefer the requested
// `lang`, else fall back to en-US, else return the first row by
// language ascending. Returns the raw GORM error on miss — callers
// translate to ports.ErrNotFound when appropriate.
//
// Implemented as a single SELECT — both pg and sqlite treat the
// `CASE WHEN language = ? THEN 1 ELSE 0 END DESC, language ASC` form
// as a stable, deterministic ORDER BY. Mirrors the legacy helper in
// infrastructure/database/repositories/i18n_texts.go bit-for-bit so
// the people / person_biography fallback reads (which moved here in
// story 437 commit 2) produce identical rows to the pre-move codepath.
// When i18n_texts.go itself moves in commit 3, the two copies merge
// into one home and this file is deleted.
func pickLanguageFallback(
	ctx context.Context,
	db *gorm.DB,
	table, entityCol string,
	entityID int64,
	lang string,
	dst any,
) error {
	if lang == "" {
		lang = fallbackLanguage
	}
	q := fmt.Sprintf(
		"SELECT * FROM %s "+
			"WHERE %s = ? "+
			"ORDER BY CASE WHEN language = ? THEN 2 WHEN language = ? THEN 1 ELSE 0 END DESC, language ASC "+
			"LIMIT 1",
		table, entityCol,
	)
	err := dbFromContext(ctx, db).WithContext(ctx).
		Raw(q, entityID, lang, fallbackLanguage).Scan(dst).Error
	if err != nil {
		return fmt.Errorf("pick language fallback (%s): %w", table, err)
	}
	return nil
}
