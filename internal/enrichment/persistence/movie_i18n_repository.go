package persistence

import (
	"context"
	"fmt"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	database "github.com/alexmorbo/seasonfill/internal/shared/db"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
)

// MovieI18nSeeder writes the per-language localized movie side-table
// (movie_i18n). The discovery stub path seeds a (movie_id, lang) row
// only-if-absent so a discovery stub can NEVER overwrite an enrichment-written
// localized title — enrichment OWNS the row once present. Mirror of the
// series_texts InsertBaseLangIfAbsent seed used by the series stub adapter.
type MovieI18nSeeder struct {
	db *gorm.DB
}

func NewMovieI18nSeeder(db *gorm.DB) *MovieI18nSeeder { return &MovieI18nSeeder{db: db} }

// SeedStub inserts a (movie_id, lang) row only when absent — DO NOTHING on
// conflict. The title/poster/backdrop come from the discovery TMDB list row
// (raw TMDB asset paths, the same format the enrichment worker later
// overwrites with resolved hashes). An empty title seeds SQL NULL so the
// enrichment worker's never-empty fallback stays intact.
func (r *MovieI18nSeeder) SeedStub(ctx context.Context, movieID domain.MovieID, lang, title string, poster, backdrop *string) error {
	if movieID == 0 {
		return fmt.Errorf("seed movie i18n: movie_id must be non-zero")
	}
	if lang == "" {
		return fmt.Errorf("seed movie i18n: lang required")
	}
	m := database.MovieI18nModel{
		MovieID:       movieID,
		Language:      lang,
		Title:         nilIfEmptyMovieText(title),
		PosterAsset:   poster,
		BackdropAsset: backdrop,
		UpdatedAt:     time.Now().UTC(),
	}
	err := dbFromContext(ctx, r.db).WithContext(ctx).
		Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "movie_id"}, {Name: "language"}},
			DoNothing: true,
		}).
		Create(&m).Error
	if err != nil {
		return fmt.Errorf("seed movie i18n: %w", err)
	}
	return nil
}

// nilIfEmptyMovieText returns a pointer to s, or nil when s == "".
func nilIfEmptyMovieText(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
