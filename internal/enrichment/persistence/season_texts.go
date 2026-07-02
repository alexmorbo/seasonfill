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

// SeasonTextsRepository persists the localised text rows for a season
// (season_texts, PK (series_id, season_number, language)). Mirrors
// SeriesTextsRepository / EpisodeTextsRepository but on the 3-column
// composite key, and — unlike series_texts — COALESCE-protects the
// content columns (name, overview) as well as enriched_at, so the B3b
// narrow worker's multi-pass writes never blank a previously-fetched
// value (see story 580 IN-5).
type SeasonTextsRepository struct {
	db *gorm.DB
}

func NewSeasonTextsRepository(db *gorm.DB) *SeasonTextsRepository {
	return &SeasonTextsRepository{db: db}
}

// Get fetches the row for (series_id, season_number, language) exactly.
// Returns ports.ErrNotFound when no row matches.
func (r *SeasonTextsRepository) Get(
	ctx context.Context,
	seriesID domain.SeriesID,
	seasonNumber int,
	language string,
) (series.SeasonText, error) {
	var m database.SeasonTextModel
	err := dbFromContext(ctx, r.db).WithContext(ctx).
		Where("series_id = ? AND season_number = ? AND language = ?", seriesID, seasonNumber, language).
		First(&m).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return series.SeasonText{}, ports.ErrNotFound
		}
		return series.SeasonText{}, fmt.Errorf("get season_texts: %w", err)
	}
	return toSeasonText(m), nil
}

// ListBySeriesWithFallback returns one SeasonText per season_number for the
// given series, applying the §5.6 two-tier fallback (requested language
// first, en-US second) in at most two round-trips, keyed by season_number.
// This is the read shape B3c's SeasonsComposer consumes: it iterates the
// canon `seasons` rows and looks up each season_number in this map, falling
// back to canon seasons.name (the third, non-persistence tier) when a
// key is absent.
//
// Semantics (mirrors SeriesTextsRepository.ListByIDsWithFallback):
//   - Season with a row in `lang`            → entry uses that row.
//   - Season with no `lang` row but en-US    → entry uses en-US row.
//   - Season with neither row                → key absent (caller uses canon).
//   - lang == "" normalises to en-US (single pass).
//   - Empty series (no rows) → empty map, nil error.
//
// The §5.6 third tier ("first available row by language ASC") is NOT applied
// here — same posture as the sibling batch reads; the composer's canon
// fallback covers the "no localized row at all" case.
func (r *SeasonTextsRepository) ListBySeriesWithFallback(
	ctx context.Context,
	seriesID domain.SeriesID,
	lang string,
) (map[int]series.SeasonText, error) {
	if lang == "" {
		lang = fallbackLanguage
	}

	// First pass: rows in the requested language. Scans the composite PK
	// prefix (series_id) directly — cheap even when season_texts is sparse.
	var primary []database.SeasonTextModel
	if err := dbFromContext(ctx, r.db).WithContext(ctx).
		Where("series_id = ? AND language = ?", seriesID, lang).
		Find(&primary).Error; err != nil {
		return nil, fmt.Errorf("list season_texts by series (lang=%s): %w", lang, err)
	}

	out := make(map[int]series.SeasonText, len(primary))
	for _, m := range primary {
		out[m.SeasonNumber] = toSeasonText(m)
	}

	// Second pass — only when lang != en-US: pick up seasons that have no
	// row in `lang` but DO have one in en-US.
	if lang != fallbackLanguage {
		var fallback []database.SeasonTextModel
		if err := dbFromContext(ctx, r.db).WithContext(ctx).
			Where("series_id = ? AND language = ?", seriesID, fallbackLanguage).
			Find(&fallback).Error; err != nil {
			return nil, fmt.Errorf("list season_texts en-US fallback: %w", err)
		}
		for _, m := range fallback {
			if _, ok := out[m.SeasonNumber]; !ok {
				out[m.SeasonNumber] = toSeasonText(m)
			}
		}
	}

	return out, nil
}

// Upsert writes a season text row by composite PK. Idempotent. Rejects a
// zero series_id or empty language (a zero/negative season_number IS valid —
// season 0 is TMDB "Specials"). COALESCE shields name, overview, and
// enriched_at so a partial (name-only) write never blanks a previously
// stored overview or freshness stamp (story 580 IN-5).
func (r *SeasonTextsRepository) Upsert(ctx context.Context, t series.SeasonText) error {
	if t.SeriesID == 0 {
		return fmt.Errorf("upsert season_texts: series_id must be non-zero")
	}
	if t.Language == "" {
		return fmt.Errorf("upsert season_texts: language must be non-empty")
	}
	t.UpdatedAt = time.Now().UTC()
	m := fromSeasonText(t)
	err := dbFromContext(ctx, r.db).WithContext(ctx).Clauses(clause.OnConflict{
		Columns: []clause.Column{
			{Name: "series_id"},
			{Name: "season_number"},
			{Name: "language"},
		},
		DoUpdates: clause.Assignments(map[string]any{
			"name":        gorm.Expr("COALESCE(excluded.name, season_texts.name)"),
			"overview":    gorm.Expr("COALESCE(excluded.overview, season_texts.overview)"),
			"enriched_at": gorm.Expr("COALESCE(excluded.enriched_at, season_texts.enriched_at)"),
			"updated_at":  gorm.Expr("excluded.updated_at"),
		}),
	}).Create(&m).Error
	if err != nil {
		return fmt.Errorf("upsert season_texts: %w", err)
	}
	return nil
}

func toSeasonText(m database.SeasonTextModel) series.SeasonText {
	return series.SeasonText{
		SeriesID:     m.SeriesID,
		SeasonNumber: m.SeasonNumber,
		Language:     m.Language,
		Name:         m.Name,
		Overview:     m.Overview,
		EnrichedAt:   m.EnrichedAt,
		UpdatedAt:    m.UpdatedAt,
	}
}

func fromSeasonText(t series.SeasonText) database.SeasonTextModel {
	return database.SeasonTextModel{
		SeriesID:     t.SeriesID,
		SeasonNumber: t.SeasonNumber,
		Language:     t.Language,
		Name:         t.Name,
		Overview:     t.Overview,
		EnrichedAt:   t.EnrichedAt,
		UpdatedAt:    t.UpdatedAt,
	}
}
