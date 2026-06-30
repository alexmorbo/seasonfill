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

// fallbackLanguage is the PRD-mandated default — used both as the
// secondary read target by the §5.6 helper and as the contract the
// future TMDB worker writes alongside the requested language row.
const fallbackLanguage = "en-US"

// pickLanguageFallback applies the §5.6 fallback: prefer the requested
// `lang`, else fall back to en-US, else return the first row by
// language ascending. Returns ports.ErrNotFound when no row matches
// either the requested language or the fallback (the composer's
// degraded path picks that up later).
//
// Implemented as a single SELECT — both pg and sqlite treat
// `CASE WHEN language = ? THEN 1 ELSE 0 END DESC, language ASC` as a
// stable, deterministic ORDER BY. The dialect-portable CASE expression
// avoids the `(bool) DESC` form which sorts identically on pg (true>
// false) and sqlite (1>0) but is harder to verify across drivers.
//
// `entityCol` is the FK column name ("series_id" or "episode_id");
// `table` is the unquoted table name. Both are caller-supplied
// constants — never user input — so this small string-builder is safe.
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
	// Three-tier preference encoded in a deterministic ORDER BY: the
	// requested language wins first (CASE = 2), en-US second (CASE = 1),
	// and any other row third (CASE = 0). The language ASC tiebreaker
	// makes the "first available" branch deterministic across dialects.
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

// SeriesTextsRepository persists the localised text rows for a series.
type SeriesTextsRepository struct {
	db *gorm.DB
}

func NewSeriesTextsRepository(db *gorm.DB) *SeriesTextsRepository {
	return &SeriesTextsRepository{db: db}
}

// Get fetches the row for (series_id, language) exactly. Returns
// ports.ErrNotFound when no row matches.
func (r *SeriesTextsRepository) Get(ctx context.Context, seriesID domain.SeriesID, language string) (series.SeriesText, error) {
	var m database.SeriesTextModel
	err := dbFromContext(ctx, r.db).WithContext(ctx).
		Where("series_id = ? AND language = ?", seriesID, language).
		First(&m).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return series.SeriesText{}, ports.ErrNotFound
		}
		return series.SeriesText{}, fmt.Errorf("get series_texts: %w", err)
	}
	return toSeriesText(m), nil
}

// GetWithFallback returns the row for the requested language, or the
// en-US fallback, or the first available row by language ascending.
// ports.ErrNotFound is the only NotFound sentinel.
func (r *SeriesTextsRepository) GetWithFallback(ctx context.Context, seriesID domain.SeriesID, language string) (series.SeriesText, error) {
	var m database.SeriesTextModel
	if err := pickLanguageFallback(ctx, r.db, "series_texts", "series_id", int64(seriesID), language, &m); err != nil {
		return series.SeriesText{}, err
	}
	if m.SeriesID == 0 {
		return series.SeriesText{}, ports.ErrNotFound
	}
	return toSeriesText(m), nil
}

// Upsert writes a text row by composite PK. Idempotent.
func (r *SeriesTextsRepository) Upsert(ctx context.Context, t series.SeriesText) error {
	if t.SeriesID == 0 {
		return fmt.Errorf("upsert series_texts: series_id must be non-zero")
	}
	if t.Language == "" {
		return fmt.Errorf("upsert series_texts: language must be non-empty")
	}
	t.UpdatedAt = time.Now().UTC()
	m := fromSeriesText(t)
	// COALESCE shield on enriched_at: a Sonarr-side write path that
	// leaves EnrichedAt nil MUST NOT blank a previously-stamped TMDB
	// freshness column. Same pattern as series.tmdb_rating / poster_asset
	// guarded by seriesUpsertAssignments.
	err := dbFromContext(ctx, r.db).WithContext(ctx).Clauses(clause.OnConflict{
		Columns: []clause.Column{
			{Name: "series_id"},
			{Name: "language"},
		},
		DoUpdates: clause.Assignments(map[string]any{
			"title":       gorm.Expr("excluded.title"),
			"overview":    gorm.Expr("excluded.overview"),
			"tagline":     gorm.Expr("excluded.tagline"),
			"enriched_at": gorm.Expr("COALESCE(excluded.enriched_at, series_texts.enriched_at)"),
			"updated_at":  gorm.Expr("excluded.updated_at"),
		}),
	}).Create(&m).Error
	if err != nil {
		return fmt.Errorf("upsert series_texts: %w", err)
	}
	return nil
}

func toSeriesText(m database.SeriesTextModel) series.SeriesText {
	return series.SeriesText{
		SeriesID:   m.SeriesID,
		Language:   m.Language,
		Title:      m.Title,
		Overview:   m.Overview,
		Tagline:    m.Tagline,
		EnrichedAt: m.EnrichedAt,
		UpdatedAt:  m.UpdatedAt,
	}
}

func fromSeriesText(t series.SeriesText) database.SeriesTextModel {
	return database.SeriesTextModel{
		SeriesID:   t.SeriesID,
		Language:   t.Language,
		Title:      t.Title,
		Overview:   t.Overview,
		Tagline:    t.Tagline,
		EnrichedAt: t.EnrichedAt,
		UpdatedAt:  t.UpdatedAt,
	}
}

// EpisodeTextsRepository persists the localised text rows for an
// episode. Mirrors SeriesTextsRepository — same PK shape, same
// fallback semantics.
type EpisodeTextsRepository struct {
	db *gorm.DB
}

func NewEpisodeTextsRepository(db *gorm.DB) *EpisodeTextsRepository {
	return &EpisodeTextsRepository{db: db}
}

func (r *EpisodeTextsRepository) Get(ctx context.Context, episodeID domain.EpisodeID, language string) (series.EpisodeText, error) {
	var m database.EpisodeTextModel
	err := dbFromContext(ctx, r.db).WithContext(ctx).
		Where("episode_id = ? AND language = ?", episodeID, language).
		First(&m).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return series.EpisodeText{}, ports.ErrNotFound
		}
		return series.EpisodeText{}, fmt.Errorf("get episode_texts: %w", err)
	}
	return toEpisodeText(m), nil
}

func (r *EpisodeTextsRepository) GetWithFallback(ctx context.Context, episodeID domain.EpisodeID, language string) (series.EpisodeText, error) {
	var m database.EpisodeTextModel
	if err := pickLanguageFallback(ctx, r.db, "episode_texts", "episode_id", int64(episodeID), language, &m); err != nil {
		return series.EpisodeText{}, err
	}
	if m.EpisodeID == 0 {
		return series.EpisodeText{}, ports.ErrNotFound
	}
	return toEpisodeText(m), nil
}

// ListByEpisodeIDsWithFallback returns one EpisodeText per requested
// episode_id, applying the §5.6 two-tier fallback (requested language
// first, en-US second) in a single round-trip. Story 550 (E-1 Z1) —
// replaces the per-episode GetWithFallback loop in the seriesdetail
// composer (composer.go branch b + GetCanonicalSeasons).
//
// Semantics:
//   - Empty episodeIDs slice → empty map, nil error (no SQL issued).
//   - Episode with a row in `lang`            → entry uses that row.
//   - Episode with no `lang` row but en-US    → entry uses en-US row.
//   - Episode with neither row                → key absent from map
//     (caller mirrors the existing ErrNotFound branch by leaving
//     EpisodeDetail.Text nil).
//
// The third §5.6 tier ("first available row by language ASC") is NOT
// applied on this batch path — see story-550 design notes. The composer
// treats "no row" the same way today; degrading further would require a
// per-row LATERAL/window subquery that does not survive sqlite parity.
//
// SQL: at most two index-driven SELECTs against episode_texts on the
// (episode_id, language) PK — one for the requested language, one for
// the en-US fallback restricted to the ids the first query did not
// satisfy. Single SELECT when lang == en-US. Merge happens in Go to
// avoid a COALESCE projection that sqlite cannot scan back into
// *time.Time (no datetime affinity through COALESCE).
func (r *EpisodeTextsRepository) ListByEpisodeIDsWithFallback(
	ctx context.Context,
	episodeIDs []domain.EpisodeID,
	lang string,
) (map[domain.EpisodeID]series.EpisodeText, error) {
	if len(episodeIDs) == 0 {
		return map[domain.EpisodeID]series.EpisodeText{}, nil
	}
	if lang == "" {
		lang = fallbackLanguage
	}

	// Cast to int64 because GORM's `IN ?` expander walks `any` slices
	// element-wise; using the typed primitive directly works on
	// Postgres but trips sqlite's bind-conversion in older driver
	// builds. The conversion is free at runtime — same kind underlying.
	ids := make([]int64, len(episodeIDs))
	for i, id := range episodeIDs {
		ids[i] = int64(id)
	}

	// First pass: rows in the requested language. Scans the
	// (episode_id, language) PK index directly — no `episodes` table
	// touched, query stays cheap even when episode_texts is sparse.
	var primary []database.EpisodeTextModel
	if err := dbFromContext(ctx, r.db).WithContext(ctx).
		Where("episode_id IN ? AND language = ?", ids, lang).
		Find(&primary).Error; err != nil {
		return nil, fmt.Errorf("list episode_texts by ids (lang=%s): %w", lang, err)
	}

	out := make(map[domain.EpisodeID]series.EpisodeText, len(episodeIDs))
	for _, m := range primary {
		out[m.EpisodeID] = toEpisodeText(m)
	}

	// Second pass — only when lang != en-US: pick up episodes that
	// have no row in `lang` but DO have one in en-US. Skipped when
	// lang IS en-US (the first query already covered that case).
	if lang != fallbackLanguage {
		remaining := make([]int64, 0, len(ids))
		for _, id := range ids {
			if _, ok := out[domain.EpisodeID(id)]; !ok {
				remaining = append(remaining, id)
			}
		}
		if len(remaining) > 0 {
			var fallback []database.EpisodeTextModel
			if err := dbFromContext(ctx, r.db).WithContext(ctx).
				Where("episode_id IN ? AND language = ?", remaining, fallbackLanguage).
				Find(&fallback).Error; err != nil {
				return nil, fmt.Errorf("list episode_texts en-US fallback: %w", err)
			}
			for _, m := range fallback {
				out[m.EpisodeID] = toEpisodeText(m)
			}
		}
	}

	return out, nil
}

// CoverageBySeries reports how many of the series's episodes have an
// `episode_texts` row for the given language vs the total episode count.
// Story 548: probe uses this to detect "episodes en-US fine but ru-RU
// missing" — Story 547's async followup committed partial coverage and
// stamped enrichment_tmdb_synced_at, so the canon TTL no longer flags
// the gap and the per-lang invariant must catch it.
//
// Returns (0, 0, nil) when the series has no episodes — caller skips the
// coverage check in that case (cold-boot / FAM / brand-new series).
func (r *EpisodeTextsRepository) CoverageBySeries(ctx context.Context, seriesID domain.SeriesID, language string) (covered, total int, err error) {
	var totalCnt int64
	if e := dbFromContext(ctx, r.db).WithContext(ctx).
		Model(&database.EpisodeModel{}).
		Where("series_id = ?", seriesID).
		Count(&totalCnt).Error; e != nil {
		return 0, 0, fmt.Errorf("count episodes: %w", e)
	}
	if totalCnt == 0 {
		return 0, 0, nil
	}
	var coveredCnt int64
	if e := dbFromContext(ctx, r.db).WithContext(ctx).
		Table("episode_texts AS et").
		Joins("JOIN episodes e ON e.id = et.episode_id").
		Where("e.series_id = ? AND et.language = ?", seriesID, language).
		Count(&coveredCnt).Error; e != nil {
		return 0, 0, fmt.Errorf("count episode_texts: %w", e)
	}
	return int(coveredCnt), int(totalCnt), nil
}

func (r *EpisodeTextsRepository) Upsert(ctx context.Context, t series.EpisodeText) error {
	if t.EpisodeID == 0 {
		return fmt.Errorf("upsert episode_texts: episode_id must be non-zero")
	}
	if t.Language == "" {
		return fmt.Errorf("upsert episode_texts: language must be non-empty")
	}
	t.UpdatedAt = time.Now().UTC()
	m := fromEpisodeText(t)
	err := dbFromContext(ctx, r.db).WithContext(ctx).Clauses(clause.OnConflict{
		Columns: []clause.Column{
			{Name: "episode_id"},
			{Name: "language"},
		},
		DoUpdates: clause.Assignments(map[string]any{
			"title":       gorm.Expr("excluded.title"),
			"overview":    gorm.Expr("excluded.overview"),
			"enriched_at": gorm.Expr("COALESCE(excluded.enriched_at, episode_texts.enriched_at)"),
			"updated_at":  gorm.Expr("excluded.updated_at"),
		}),
	}).Create(&m).Error
	if err != nil {
		return fmt.Errorf("upsert episode_texts: %w", err)
	}
	return nil
}

func toEpisodeText(m database.EpisodeTextModel) series.EpisodeText {
	return series.EpisodeText{
		EpisodeID:  m.EpisodeID,
		Language:   m.Language,
		Title:      m.Title,
		Overview:   m.Overview,
		EnrichedAt: m.EnrichedAt,
		UpdatedAt:  m.UpdatedAt,
	}
}

func fromEpisodeText(t series.EpisodeText) database.EpisodeTextModel {
	return database.EpisodeTextModel{
		EpisodeID:  t.EpisodeID,
		Language:   t.Language,
		Title:      t.Title,
		Overview:   t.Overview,
		EnrichedAt: t.EnrichedAt,
		UpdatedAt:  t.UpdatedAt,
	}
}
