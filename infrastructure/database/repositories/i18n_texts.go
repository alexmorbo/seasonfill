package repositories

import (
	"context"
	"errors"
	"fmt"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/alexmorbo/seasonfill/application/ports"
	"github.com/alexmorbo/seasonfill/domain/series"
	"github.com/alexmorbo/seasonfill/infrastructure/database"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
)

// fallbackLanguage is the PRD-mandated default — used both as the
// secondary read target by the §5.6 helper and as the contract the
// future TMDB worker writes alongside the requested language row.
const fallbackLanguage = "en-US"

// pickLanguageFallback applies the §5.6 fallback: prefer the requested
// `lang`, else fall back to en-US, else return the first row by
// language ascending. Returns ports.ErrNotFound when no row matches
// either the requested language or the fallback (sync_log degraded
// path picks that up later).
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
	err := dbFromContext(ctx, r.db).WithContext(ctx).Clauses(clause.OnConflict{
		Columns: []clause.Column{
			{Name: "series_id"},
			{Name: "language"},
		},
		DoUpdates: clause.AssignmentColumns([]string{
			"title", "overview", "tagline", "updated_at",
		}),
	}).Create(&m).Error
	if err != nil {
		return fmt.Errorf("upsert series_texts: %w", err)
	}
	return nil
}

func toSeriesText(m database.SeriesTextModel) series.SeriesText {
	return series.SeriesText{
		SeriesID:  m.SeriesID,
		Language:  m.Language,
		Title:     m.Title,
		Overview:  m.Overview,
		Tagline:   m.Tagline,
		UpdatedAt: m.UpdatedAt,
	}
}

func fromSeriesText(t series.SeriesText) database.SeriesTextModel {
	return database.SeriesTextModel{
		SeriesID:  t.SeriesID,
		Language:  t.Language,
		Title:     t.Title,
		Overview:  t.Overview,
		Tagline:   t.Tagline,
		UpdatedAt: t.UpdatedAt,
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
		DoUpdates: clause.AssignmentColumns([]string{
			"title", "overview", "updated_at",
		}),
	}).Create(&m).Error
	if err != nil {
		return fmt.Errorf("upsert episode_texts: %w", err)
	}
	return nil
}

func toEpisodeText(m database.EpisodeTextModel) series.EpisodeText {
	return series.EpisodeText{
		EpisodeID: m.EpisodeID,
		Language:  m.Language,
		Title:     m.Title,
		Overview:  m.Overview,
		UpdatedAt: m.UpdatedAt,
	}
}

func fromEpisodeText(t series.EpisodeText) database.EpisodeTextModel {
	return database.EpisodeTextModel{
		EpisodeID: t.EpisodeID,
		Language:  t.Language,
		Title:     t.Title,
		Overview:  t.Overview,
		UpdatedAt: t.UpdatedAt,
	}
}
