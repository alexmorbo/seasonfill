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

// SeriesMediaTextsRepository persists the per-language poster/backdrop
// rows (series_media_texts, PK (series_id, language)). Variant A
// (Story 584). Mirrors SeriesTextsRepository but — like
// SeasonTextsRepository — COALESCE-protects EVERY content column
// (poster_asset, poster_hash, backdrop_asset, backdrop_hash) plus
// enriched_at, so a partial write (e.g. poster-only) never blanks a
// previously-fetched backdrop or freshness stamp.
type SeriesMediaTextsRepository struct {
	db *gorm.DB
}

func NewSeriesMediaTextsRepository(db *gorm.DB) *SeriesMediaTextsRepository {
	return &SeriesMediaTextsRepository{db: db}
}

// Get fetches the row for (series_id, language) exactly. Returns
// ports.ErrNotFound when no row matches.
func (r *SeriesMediaTextsRepository) Get(ctx context.Context, seriesID domain.SeriesID, language string) (series.SeriesMediaText, error) {
	var m database.SeriesMediaTextModel
	err := dbFromContext(ctx, r.db).WithContext(ctx).
		Where("series_id = ? AND language = ?", seriesID, language).
		First(&m).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return series.SeriesMediaText{}, ports.ErrNotFound
		}
		return series.SeriesMediaText{}, fmt.Errorf("get series_media_texts: %w", err)
	}
	return toSeriesMediaText(m), nil
}

// GetWithFallback returns the row for the requested language, or the
// en-US fallback, or the first available row by language ascending
// (three-tier §5.6 CASE ORDER BY via pickLanguageFallback). ErrNotFound
// is the only NotFound sentinel — the composer's canon fallback (raw
// series.poster_asset) is the final, non-persistence tier.
func (r *SeriesMediaTextsRepository) GetWithFallback(ctx context.Context, seriesID domain.SeriesID, language string) (series.SeriesMediaText, error) {
	var m database.SeriesMediaTextModel
	if err := pickLanguageFallback(ctx, r.db, "series_media_texts", "series_id", int64(seriesID), language, &m); err != nil {
		return series.SeriesMediaText{}, err
	}
	if m.SeriesID == 0 {
		return series.SeriesMediaText{}, ports.ErrNotFound
	}
	return toSeriesMediaText(m), nil
}

// ListByIDsWithFallback returns one SeriesMediaText per requested
// series_id, applying the two-tier fallback (requested language first,
// en-US second) in at most two round-trips. Mirrors
// SeriesTextsRepository.ListByIDsWithFallback exactly — used by the
// recommendations read path (584b) and the grid localizer (584b) to
// localize posters without N+1 SELECTs.
//
// Semantics:
//   - Empty seriesIDs slice → empty map, nil error (no SQL).
//   - Series with a row in `lang`          → entry uses that row.
//   - Series with no `lang` row but en-US  → entry uses en-US row.
//   - Series with neither row              → key absent (caller keeps canon).
//   - lang == "" normalises to en-US (single pass).
func (r *SeriesMediaTextsRepository) ListByIDsWithFallback(
	ctx context.Context,
	seriesIDs []domain.SeriesID,
	lang string,
) (map[domain.SeriesID]series.SeriesMediaText, error) {
	if len(seriesIDs) == 0 {
		return map[domain.SeriesID]series.SeriesMediaText{}, nil
	}
	if lang == "" {
		lang = fallbackLanguage
	}
	ids := make([]int64, len(seriesIDs))
	for i, id := range seriesIDs {
		ids[i] = int64(id)
	}

	var primary []database.SeriesMediaTextModel
	if err := dbFromContext(ctx, r.db).WithContext(ctx).
		Where("series_id IN ? AND language = ?", ids, lang).
		Find(&primary).Error; err != nil {
		return nil, fmt.Errorf("list series_media_texts by ids (lang=%s): %w", lang, err)
	}
	out := make(map[domain.SeriesID]series.SeriesMediaText, len(seriesIDs))
	for _, m := range primary {
		out[m.SeriesID] = toSeriesMediaText(m)
	}

	if lang != fallbackLanguage {
		remaining := make([]int64, 0, len(ids))
		for _, id := range ids {
			if _, ok := out[domain.SeriesID(id)]; !ok {
				remaining = append(remaining, id)
			}
		}
		if len(remaining) > 0 {
			var fallback []database.SeriesMediaTextModel
			if err := dbFromContext(ctx, r.db).WithContext(ctx).
				Where("series_id IN ? AND language = ?", remaining, fallbackLanguage).
				Find(&fallback).Error; err != nil {
				return nil, fmt.Errorf("list series_media_texts en-US fallback: %w", err)
			}
			for _, m := range fallback {
				out[m.SeriesID] = toSeriesMediaText(m)
			}
		}
	}
	return out, nil
}

// Upsert writes a per-language media row by composite PK. Idempotent.
// Rejects a zero series_id or empty language. COALESCE shields all four
// media columns + enriched_at so a partial write never blanks a
// previously stored value (memory seasonfill-upsert-coalesce-pattern:
// bare excluded orphan branches trip SQLSTATE 42601 on Postgres — every
// DO UPDATE column is explicitly COALESCE-wrapped). updated_at always
// takes the new value.
func (r *SeriesMediaTextsRepository) Upsert(ctx context.Context, t series.SeriesMediaText) error {
	if t.SeriesID == 0 {
		return fmt.Errorf("upsert series_media_texts: series_id must be non-zero")
	}
	if t.Language == "" {
		return fmt.Errorf("upsert series_media_texts: language must be non-empty")
	}
	t.UpdatedAt = time.Now().UTC()
	m := fromSeriesMediaText(t)
	err := dbFromContext(ctx, r.db).WithContext(ctx).Clauses(clause.OnConflict{
		Columns: []clause.Column{
			{Name: "series_id"},
			{Name: "language"},
		},
		DoUpdates: clause.Assignments(map[string]any{
			"poster_asset":   gorm.Expr("COALESCE(excluded.poster_asset, series_media_texts.poster_asset)"),
			"poster_hash":    gorm.Expr("COALESCE(excluded.poster_hash, series_media_texts.poster_hash)"),
			"backdrop_asset": gorm.Expr("COALESCE(excluded.backdrop_asset, series_media_texts.backdrop_asset)"),
			"backdrop_hash":  gorm.Expr("COALESCE(excluded.backdrop_hash, series_media_texts.backdrop_hash)"),
			"enriched_at":    gorm.Expr("COALESCE(excluded.enriched_at, series_media_texts.enriched_at)"),
			"updated_at":     gorm.Expr("excluded.updated_at"),
		}),
	}).Create(&m).Error
	if err != nil {
		return fmt.Errorf("upsert series_media_texts: %w", err)
	}
	return nil
}

func toSeriesMediaText(m database.SeriesMediaTextModel) series.SeriesMediaText {
	return series.SeriesMediaText{
		SeriesID:      m.SeriesID,
		Language:      m.Language,
		PosterAsset:   m.PosterAsset,
		PosterHash:    m.PosterHash,
		BackdropAsset: m.BackdropAsset,
		BackdropHash:  m.BackdropHash,
		EnrichedAt:    m.EnrichedAt,
		UpdatedAt:     m.UpdatedAt,
	}
}

func fromSeriesMediaText(t series.SeriesMediaText) database.SeriesMediaTextModel {
	return database.SeriesMediaTextModel{
		SeriesID:      t.SeriesID,
		Language:      t.Language,
		PosterAsset:   t.PosterAsset,
		PosterHash:    t.PosterHash,
		BackdropAsset: t.BackdropAsset,
		BackdropHash:  t.BackdropHash,
		EnrichedAt:    t.EnrichedAt,
		UpdatedAt:     t.UpdatedAt,
	}
}
