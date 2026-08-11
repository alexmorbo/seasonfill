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

// UpsertEnriched writes the enrichment-OWNED per-language movie_i18n row
// (Ф6-R-4a L3-2). Unlike SeedStub (DO NOTHING on conflict — a discovery stub
// must never overwrite an enriched row), the enrichment worker OWNS the base-
// language row and refreshes it every hydrate: OnConflict DoUpdates.
//
// The text/media columns are COALESCE(excluded.X, movie_i18n.X)-guarded so a
// partial GetMovie payload (e.g. a movie with no tagline) never blanks a
// previously-written value — mirror of the "два писателя" COALESCE invariant on
// the canon movies row. enriched_at + updated_at are always stamped fresh.
//
// title/overview/tagline are passed as strings; empty → SQL NULL via the
// nil-if-empty helper, and the COALESCE then preserves any prior non-NULL value.
func (r *MovieI18nSeeder) UpsertEnriched(
	ctx context.Context,
	movieID domain.MovieID,
	lang, title, overview, tagline string,
	poster, backdrop *string,
	now time.Time,
) error {
	if movieID == 0 {
		return fmt.Errorf("upsert movie i18n: movie_id must be non-zero")
	}
	if lang == "" {
		return fmt.Errorf("upsert movie i18n: lang required")
	}
	nowUTC := now.UTC()
	m := database.MovieI18nModel{
		MovieID:       movieID,
		Language:      lang,
		Title:         nilIfEmptyMovieText(title),
		Overview:      nilIfEmptyMovieText(overview),
		Tagline:       nilIfEmptyMovieText(tagline),
		PosterAsset:   poster,
		BackdropAsset: backdrop,
		EnrichedAt:    &nowUTC,
		UpdatedAt:     nowUTC,
	}
	err := dbFromContext(ctx, r.db).WithContext(ctx).
		Clauses(clause.OnConflict{
			Columns: []clause.Column{{Name: "movie_id"}, {Name: "language"}},
			DoUpdates: clause.Assignments(map[string]any{
				"title":          gorm.Expr("COALESCE(excluded.title, movie_i18n.title)"),
				"overview":       gorm.Expr("COALESCE(excluded.overview, movie_i18n.overview)"),
				"tagline":        gorm.Expr("COALESCE(excluded.tagline, movie_i18n.tagline)"),
				"poster_asset":   gorm.Expr("COALESCE(excluded.poster_asset, movie_i18n.poster_asset)"),
				"backdrop_asset": gorm.Expr("COALESCE(excluded.backdrop_asset, movie_i18n.backdrop_asset)"),
				"enriched_at":    nowUTC,
				"updated_at":     nowUTC,
			}),
		}).
		Create(&m).Error
	if err != nil {
		return fmt.Errorf("upsert movie i18n: %w", err)
	}
	return nil
}
