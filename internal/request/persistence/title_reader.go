package persistence

import (
	"context"
	"fmt"

	"gorm.io/gorm"
)

// TitleReader resolves human-readable catalog titles for request-queue rows
// (Ф8-U-6a) from the LOCAL catalog only — no external TMDB call. Movie titles
// come from the `movies` canon keyed by TMDB id; series titles come from
// `series` (keyed by TVDB id) with the display title resolved from
// `series_texts` (en-US → first available language) and a canon
// original_title fallback, mirroring the catalog list's title resolution.
// Read-only.
type TitleReader struct{ db *gorm.DB }

// NewTitleReader constructs a TitleReader bound to db.
func NewTitleReader(db *gorm.DB) *TitleReader {
	return &TitleReader{db: db}
}

// MovieTitlesByTMDB batch-resolves movie titles by TMDB id in a single query.
// Ids with no local `movies` row are absent from the returned map (the caller
// renders a null title). An empty/nil id slice short-circuits to an empty map.
func (r *TitleReader) MovieTitlesByTMDB(ctx context.Context, tmdbIDs []int64) (map[int64]string, error) {
	out := make(map[int64]string, len(tmdbIDs))
	if len(tmdbIDs) == 0 {
		return out, nil
	}
	var rows []struct {
		TMDBID int64  `gorm:"column:tmdb_id"`
		Title  string `gorm:"column:title"`
	}
	err := r.db.WithContext(ctx).
		Table("movies").
		Select("tmdb_id", "title").
		Where("tmdb_id IN ?", tmdbIDs).
		Scan(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("movie titles by tmdb: %w", err)
	}
	for _, row := range rows {
		if row.Title != "" {
			out[row.TMDBID] = row.Title
		}
	}
	return out, nil
}

// seriesTitleExpr resolves a series' display title from series_texts with the
// §5.6 language fallback (en-US preferred, else first language ASC), wrapped in
// COALESCE(..., s.original_title) so a series with no text rows still resolves
// to its canon original_title. `s` is the series alias. The literal 'en-US' is
// a fixed constant (no injection surface); the whole expression is dual-dialect
// (Postgres + SQLite) portable.
const seriesTitleExpr = "COALESCE((SELECT st.title FROM series_texts st WHERE st.series_id = s.id " +
	"ORDER BY CASE WHEN st.language = 'en-US' THEN 1 ELSE 0 END DESC, st.language ASC LIMIT 1), s.original_title)"

// SeriesTitlesByTVDB batch-resolves series display titles by TVDB id in a
// single query. Ids with no local `series` row — or a series with no resolvable
// title — are absent from the returned map. An empty/nil id slice
// short-circuits to an empty map.
func (r *TitleReader) SeriesTitlesByTVDB(ctx context.Context, tvdbIDs []int64) (map[int64]string, error) {
	out := make(map[int64]string, len(tvdbIDs))
	if len(tvdbIDs) == 0 {
		return out, nil
	}
	var rows []struct {
		TVDBID int64   `gorm:"column:tvdb_id"`
		Title  *string `gorm:"column:title"`
	}
	err := r.db.WithContext(ctx).
		Table("series AS s").
		Select("s.tvdb_id AS tvdb_id, "+seriesTitleExpr+" AS title").
		Where("s.tvdb_id IN ?", tvdbIDs).
		Scan(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("series titles by tvdb: %w", err)
	}
	for _, row := range rows {
		if row.Title != nil && *row.Title != "" {
			out[row.TVDBID] = *row.Title
		}
	}
	return out, nil
}
