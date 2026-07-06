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

// GetBackdropAnyLang returns the best per-COLUMN backdrop path for a series
// across ALL languages, preferring preferLang, then en-US, then any other
// language (deterministic language ASC). Unlike GetWithFallback — which picks
// ONE best-language ROW and then reads whatever backdrop_asset that row happens
// to carry (NULL for a poster-only row) — this scans only rows whose
// backdrop_asset is non-NULL, so a poster-only requested-language row never
// shadows another language's row that DOES have a backdrop (W18-15). Mirrors the
// per-column any-lang subselect the discovery list/search projections already
// use (list_repository.go / search.go). Returns (nil, nil) when NO language row
// carries a backdrop — the composer then renders a placeholder.
func (r *SeriesMediaTextsRepository) GetBackdropAnyLang(ctx context.Context, seriesID domain.SeriesID, preferLang string) (*string, error) {
	if preferLang == "" {
		preferLang = fallbackLanguage
	}
	const q = "SELECT smt.backdrop_asset AS backdrop_asset " +
		"FROM series_media_texts smt " +
		"WHERE smt.series_id = ? AND smt.backdrop_asset IS NOT NULL AND smt.backdrop_asset <> '' " +
		"ORDER BY CASE WHEN smt.language = ? THEN 2 WHEN smt.language = ? THEN 1 ELSE 0 END DESC, smt.language ASC " +
		"LIMIT 1"
	var row struct {
		BackdropAsset *string `gorm:"column:backdrop_asset"`
	}
	if err := dbFromContext(ctx, r.db).WithContext(ctx).
		Raw(q, int64(seriesID), preferLang, fallbackLanguage).
		Scan(&row).Error; err != nil {
		return nil, fmt.Errorf("get backdrop any-lang (series_media_texts): %w", err)
	}
	return row.BackdropAsset, nil
}

// ListByIDsWithFallback returns one SeriesMediaText per requested
// series_id, applying the never-empty poster ladder (requested language →
// en-US → any-available-language) in at most three round-trips. Mirrors
// SeriesTextsRepository.ListByIDsWithFallback exactly — used by the
// recommendations read path (584b) and the grid localizer (584b) to
// localize posters without N+1 SELECTs.
//
// Semantics:
//   - Empty seriesIDs slice → empty map, nil error (no SQL).
//   - Series with a row in `lang`          → entry uses that row.
//   - Series with no `lang` row but en-US  → entry uses en-US row.
//   - Series with neither, but SOME row    → entry uses the lowest-
//     language row (ORDER BY language ASC — deterministic).
//   - Series with zero rows                → key absent (caller keeps canon).
//   - lang == "" normalises to en-US.
//
// W15-2: posters have no original_title analogue, so the any-lang tier is
// the terminal never-empty guarantee. The pass runs unconditionally after
// the en-US pass (even when lang == en-US) because a series may hold only
// a non-en-US poster row.
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

	// Third pass (any-lang tier): ids still absent get the lowest-language
	// row available. ORDER BY language ASC makes the pick deterministic;
	// the first row seen per id wins.
	stillMissing := make([]int64, 0, len(ids))
	for _, id := range ids {
		if _, ok := out[domain.SeriesID(id)]; !ok {
			stillMissing = append(stillMissing, id)
		}
	}
	if len(stillMissing) > 0 {
		var anyLang []database.SeriesMediaTextModel
		if err := dbFromContext(ctx, r.db).WithContext(ctx).
			Where("series_id IN ?", stillMissing).
			Order("language ASC").
			Find(&anyLang).Error; err != nil {
			return nil, fmt.Errorf("list series_media_texts any-lang fallback: %w", err)
		}
		for _, m := range anyLang {
			if _, ok := out[m.SeriesID]; !ok {
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

// InsertIfAbsent inserts a series_media_texts row ONLY when no row exists
// for (series_id, language) — INSERT … ON CONFLICT DO NOTHING. Mirrors
// SeriesTextsRepository.InsertBaseLangIfAbsent. W15-6: the discovery
// stub-upsert seeds the per-language poster/backdrop the TMDB list
// response already carried so cards render art before enrichment runs,
// WITHOUT ever clobbering a later RefreshAllLangs poster (its resolved
// hash) via a re-EnsureStub. Idempotent: re-running against an existing
// row is a no-op (0 rows affected, nil error).
func (r *SeriesMediaTextsRepository) InsertIfAbsent(ctx context.Context, t series.SeriesMediaText) error {
	if t.SeriesID == 0 {
		return fmt.Errorf("insert series_media_texts if absent: series_id must be non-zero")
	}
	if t.Language == "" {
		return fmt.Errorf("insert series_media_texts if absent: language must be non-empty")
	}
	if t.UpdatedAt.IsZero() {
		t.UpdatedAt = time.Now().UTC()
	}
	m := fromSeriesMediaText(t)
	err := dbFromContext(ctx, r.db).WithContext(ctx).Clauses(clause.OnConflict{
		Columns: []clause.Column{
			{Name: "series_id"},
			{Name: "language"},
		},
		DoNothing: true,
	}).Create(&m).Error
	if err != nil {
		return fmt.Errorf("insert series_media_texts if absent: %w", err)
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
