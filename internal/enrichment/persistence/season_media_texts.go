package persistence

import (
	"context"
	"errors"
	"fmt"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/alexmorbo/seasonfill/internal/catalog/domain/series"
	ports "github.com/alexmorbo/seasonfill/internal/shared/dataports"
	database "github.com/alexmorbo/seasonfill/internal/shared/db"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
)

// SeasonMediaTextsRepository persists the per-language season poster/backdrop
// rows (season_media_texts, PK (series_id, season_number, language)). S-C2.
// Mirrors SeriesMediaTextsRepository on the 3-column composite key (like
// SeasonTextsRepository) and COALESCE-protects EVERY content column so a
// partial (poster-only) write never blanks a previously-fetched value or the
// freshness stamp (memory seasonfill-upsert-coalesce-pattern: bare excluded.*
// orphan branches trip SQLSTATE 42601 on Postgres).
type SeasonMediaTextsRepository struct {
	db *gorm.DB
}

func NewSeasonMediaTextsRepository(db *gorm.DB) *SeasonMediaTextsRepository {
	return &SeasonMediaTextsRepository{db: db}
}

// Get fetches the row for (series_id, season_number, language) exactly.
// Returns ports.ErrNotFound when no row matches.
func (r *SeasonMediaTextsRepository) Get(
	ctx context.Context,
	seriesID domain.SeriesID,
	seasonNumber int,
	language string,
) (series.SeasonMediaText, error) {
	var m database.SeasonMediaTextModel
	err := dbFromContext(ctx, r.db).WithContext(ctx).
		Where("series_id = ? AND season_number = ? AND language = ?", seriesID, seasonNumber, language).
		First(&m).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return series.SeasonMediaText{}, ports.ErrNotFound
		}
		return series.SeasonMediaText{}, fmt.Errorf("get season_media_texts: %w", err)
	}
	return toSeasonMediaText(m), nil
}

// ListBySeriesWithFallback returns one SeasonMediaText per season_number for the
// given series, applying the §5.6 two-tier fallback (requested language first,
// en-US second) in at most two round-trips, keyed by season_number. This is the
// read shape GetSeason / SeasonsComposer consume: they iterate canon seasons and
// look up each season_number, falling back to canon seasons.poster_asset (the
// third, non-persistence tier) when a key is absent. Mirrors
// SeasonTextsRepository.ListBySeriesWithFallback exactly.
func (r *SeasonMediaTextsRepository) ListBySeriesWithFallback(
	ctx context.Context,
	seriesID domain.SeriesID,
	lang string,
) (map[int]series.SeasonMediaText, error) {
	if lang == "" {
		lang = fallbackLanguage
	}

	var primary []database.SeasonMediaTextModel
	if err := dbFromContext(ctx, r.db).WithContext(ctx).
		Where("series_id = ? AND language = ?", seriesID, lang).
		Find(&primary).Error; err != nil {
		return nil, fmt.Errorf("list season_media_texts by series (lang=%s): %w", lang, err)
	}

	out := make(map[int]series.SeasonMediaText, len(primary))
	for _, m := range primary {
		out[m.SeasonNumber] = toSeasonMediaText(m)
	}

	if lang != fallbackLanguage {
		var fallback []database.SeasonMediaTextModel
		if err := dbFromContext(ctx, r.db).WithContext(ctx).
			Where("series_id = ? AND language = ?", seriesID, fallbackLanguage).
			Find(&fallback).Error; err != nil {
			return nil, fmt.Errorf("list season_media_texts en-US fallback: %w", err)
		}
		for _, m := range fallback {
			if _, ok := out[m.SeasonNumber]; !ok {
				out[m.SeasonNumber] = toSeasonMediaText(m)
			}
		}
	}

	return out, nil
}

// Upsert writes a season media row by composite PK. Idempotent. Rejects a zero
// series_id or empty language (a zero/negative season_number IS valid — season 0
// is TMDB "Specials"). COALESCE shields all four media columns + enriched_at so a
// partial write never blanks a previously stored value; updated_at always takes
// the new value.
func (r *SeasonMediaTextsRepository) Upsert(ctx context.Context, t series.SeasonMediaText) error {
	if t.SeriesID == 0 {
		return fmt.Errorf("upsert season_media_texts: series_id must be non-zero")
	}
	if t.Language == "" {
		return fmt.Errorf("upsert season_media_texts: language must be non-empty")
	}
	t.UpdatedAt = time.Now().UTC()
	m := fromSeasonMediaText(t)
	err := dbFromContext(ctx, r.db).WithContext(ctx).Clauses(clause.OnConflict{
		Columns: []clause.Column{
			{Name: "series_id"},
			{Name: "season_number"},
			{Name: "language"},
		},
		DoUpdates: clause.Assignments(map[string]any{
			"poster_asset":   gorm.Expr("COALESCE(excluded.poster_asset, season_media_texts.poster_asset)"),
			"poster_hash":    gorm.Expr("COALESCE(excluded.poster_hash, season_media_texts.poster_hash)"),
			"backdrop_asset": gorm.Expr("COALESCE(excluded.backdrop_asset, season_media_texts.backdrop_asset)"),
			"backdrop_hash":  gorm.Expr("COALESCE(excluded.backdrop_hash, season_media_texts.backdrop_hash)"),
			"enriched_at":    gorm.Expr("COALESCE(excluded.enriched_at, season_media_texts.enriched_at)"),
			"updated_at":     gorm.Expr("excluded.updated_at"),
		}),
	}).Create(&m).Error
	if err != nil {
		return fmt.Errorf("upsert season_media_texts: %w", err)
	}
	return nil
}

func toSeasonMediaText(m database.SeasonMediaTextModel) series.SeasonMediaText {
	return series.SeasonMediaText{
		SeriesID:      m.SeriesID,
		SeasonNumber:  m.SeasonNumber,
		Language:      m.Language,
		PosterAsset:   m.PosterAsset,
		PosterHash:    m.PosterHash,
		BackdropAsset: m.BackdropAsset,
		BackdropHash:  m.BackdropHash,
		EnrichedAt:    m.EnrichedAt,
		UpdatedAt:     m.UpdatedAt,
	}
}

func fromSeasonMediaText(t series.SeasonMediaText) database.SeasonMediaTextModel {
	return database.SeasonMediaTextModel{
		SeriesID:      t.SeriesID,
		SeasonNumber:  t.SeasonNumber,
		Language:      t.Language,
		PosterAsset:   t.PosterAsset,
		PosterHash:    t.PosterHash,
		BackdropAsset: t.BackdropAsset,
		BackdropHash:  t.BackdropHash,
		EnrichedAt:    t.EnrichedAt,
		UpdatedAt:     t.UpdatedAt,
	}
}
