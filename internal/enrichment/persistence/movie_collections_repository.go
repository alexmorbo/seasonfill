package persistence

import (
	"context"
	"errors"
	"fmt"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/alexmorbo/seasonfill/internal/catalog/domain/movie"
	ports "github.com/alexmorbo/seasonfill/internal/shared/dataports"
	database "github.com/alexmorbo/seasonfill/internal/shared/db"
	sharedErrors "github.com/alexmorbo/seasonfill/internal/shared/errors"
)

// MovieCollectionsRepository persists the `collections` table (Ф6-R-5) — the
// TMDB-franchise canon plus the Radarr native-monitor flag. Enrichment-path
// UpsertCollection is COALESCE-guarded on the TMDB columns and PRESERVES the
// operator/Radarr flags (monitored, radarr_monitored) by omitting them from the
// conflict-update set. NOT related to the Ф7 insight collections repository.
type MovieCollectionsRepository struct{ db *gorm.DB }

func NewMovieCollectionsRepository(db *gorm.DB) *MovieCollectionsRepository {
	return &MovieCollectionsRepository{db: db}
}

var (
	_ ports.MovieCollectionsReader = (*MovieCollectionsRepository)(nil)
)

// UpsertCollection inserts or COALESCE-updates the collection row keyed by the
// UNIQUE tmdb_collection_id. name/overview/*_asset are COALESCE-guarded so a
// language-poor refetch never blanks a richer value; monitored + radarr_monitored
// are EXCLUDED from the update set so an enrichment write can never reset the
// operator/Radarr flags (they keep their stored value on conflict, default false
// on first insert).
func (r *MovieCollectionsRepository) UpsertCollection(ctx context.Context, c movie.CollectionCanon) error {
	if c.TMDBCollectionID == 0 {
		return fmt.Errorf("upsert collection: tmdb_collection_id must be non-zero")
	}
	now := time.Now().UTC()
	m := database.CollectionModel{
		TMDBCollectionID: c.TMDBCollectionID,
		Name:             c.Name,
		Overview:         c.Overview,
		PosterAsset:      c.PosterAsset,
		BackdropAsset:    c.BackdropAsset,
		Monitored:        false, // ignored on conflict (not in DoUpdates)
		RadarrMonitored:  false, // ignored on conflict (not in DoUpdates)
		CreatedAt:        now,
		UpdatedAt:        now,
	}
	conflict := clause.OnConflict{
		Columns:   []clause.Column{{Name: "tmdb_collection_id"}},
		DoUpdates: clause.Assignments(movieCollectionUpsertAssignments()),
	}
	if err := dbFromContext(ctx, r.db).WithContext(ctx).Clauses(conflict).Create(&m).Error; err != nil {
		return fmt.Errorf("upsert collection: %w", err)
	}
	return nil
}

// movieCollectionUpsertAssignments — COALESCE guard for the TMDB-owned columns.
// name uses NULLIF empty-string so a stub write with an empty name cannot blank a
// stored name (mirror of movieUpsertAssignments' title guard). monitored +
// radarr_monitored are DELIBERATELY ABSENT so they are preserved on conflict.
func movieCollectionUpsertAssignments() map[string]any {
	return map[string]any{
		"name":           gorm.Expr("COALESCE(NULLIF(excluded.name, ''), collections.name)"),
		"overview":       gorm.Expr("COALESCE(excluded.overview, collections.overview)"),
		"poster_asset":   gorm.Expr("COALESCE(excluded.poster_asset, collections.poster_asset)"),
		"backdrop_asset": gorm.Expr("COALESCE(excluded.backdrop_asset, collections.backdrop_asset)"),
		"updated_at":     gorm.Expr("excluded.updated_at"),
	}
}

// GetByTMDBCollectionID reads the collection row (incl. operator/Radarr flags).
// ports.ErrNotFound on miss. Powers R-6 UI + the radarr-monitor usecase's
// idempotency read + repository tests.
func (r *MovieCollectionsRepository) GetByTMDBCollectionID(ctx context.Context, tmdbCollectionID int) (movie.CollectionCanon, error) {
	var m database.CollectionModel
	err := dbFromContext(ctx, r.db).WithContext(ctx).
		Where("tmdb_collection_id = ?", tmdbCollectionID).First(&m).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return movie.CollectionCanon{}, errors.Join(&sharedErrors.MovieNotFoundError{}, ports.ErrNotFound)
		}
		return movie.CollectionCanon{}, fmt.Errorf("get collection: %w", err)
	}
	return movie.CollectionCanon{
		TMDBCollectionID: m.TMDBCollectionID,
		Name:             m.Name,
		Overview:         m.Overview,
		PosterAsset:      m.PosterAsset,
		BackdropAsset:    m.BackdropAsset,
		Monitored:        m.Monitored,
		RadarrMonitored:  m.RadarrMonitored,
	}, nil
}

// SetRadarrMonitored flips collections.radarr_monitored for a collection keyed by
// tmdb_collection_id. ports.ErrNotFound when no row matched (the caller may treat
// it as a no-op / surface it). Only touches radarr_monitored + updated_at.
func (r *MovieCollectionsRepository) SetRadarrMonitored(ctx context.Context, tmdbCollectionID int, monitored bool) error {
	if tmdbCollectionID == 0 {
		return fmt.Errorf("set radarr_monitored: tmdb_collection_id must be non-zero")
	}
	now := time.Now().UTC()
	res := dbFromContext(ctx, r.db).WithContext(ctx).Table("collections").
		Where("tmdb_collection_id = ?", tmdbCollectionID).
		Updates(map[string]any{"radarr_monitored": monitored, "updated_at": now})
	if res.Error != nil {
		return fmt.Errorf("set radarr_monitored: %w", res.Error)
	}
	if res.RowsAffected == 0 {
		return errors.Join(&sharedErrors.MovieNotFoundError{}, ports.ErrNotFound)
	}
	return nil
}

// ListPartsWithMembership projects every canon movie in the collection with its
// per-instance library membership + localized title + raw poster. LEFT JOIN
// movie_i18n on the requested lang yields the localized title
// (COALESCE(NULLIF(mi.title,”), m.title) → canon fallback when no localized row
// or an empty localized title). LEFT JOIN movie_states on the ACTIVE
// (deleted_at IS NULL) row for the given instance; InLibrary = joined row present.
// Poster is the raw movies.poster_asset (handler resolves it to a media hash).
// Column refs only + three bind params → dialect-portable (Postgres + SQLite).
// Ordered by movies.id ASC. Empty → (nil, nil).
func (r *MovieCollectionsRepository) ListPartsWithMembership(ctx context.Context, tmdbCollectionID int, instanceName, lang string) ([]ports.MovieCollectionPart, error) {
	if tmdbCollectionID == 0 {
		return nil, nil
	}
	type partRow struct {
		MovieID   int64
		TMDBID    *int
		Title     string
		Year      *int
		Poster    *string
		InLibrary int
	}
	const q = `
SELECT m.id AS movie_id,
       m.tmdb_id AS tmdb_id,
       COALESCE(NULLIF(mi.title, ''), m.title) AS title,
       m.year AS year,
       m.poster_asset AS poster,
       CASE WHEN ms.movie_id IS NOT NULL THEN 1 ELSE 0 END AS in_library
FROM movies m
LEFT JOIN movie_i18n mi
  ON mi.movie_id = m.id
 AND mi.language = ?
LEFT JOIN movie_states ms
  ON ms.movie_id = m.id
 AND ms.instance_name = ?
 AND ms.deleted_at IS NULL
WHERE m.collection_id = ?
ORDER BY m.id ASC`
	var rows []partRow
	if err := dbFromContext(ctx, r.db).WithContext(ctx).
		Raw(q, lang, instanceName, tmdbCollectionID).Scan(&rows).Error; err != nil {
		return nil, fmt.Errorf("list collection parts: %w", err)
	}
	if len(rows) == 0 {
		return nil, nil
	}
	out := make([]ports.MovieCollectionPart, 0, len(rows))
	for _, row := range rows {
		tmdbID := 0
		if row.TMDBID != nil {
			tmdbID = *row.TMDBID
		}
		out = append(out, ports.MovieCollectionPart{
			MovieID:   row.MovieID,
			TMDBID:    tmdbID,
			Title:     row.Title,
			Year:      row.Year,
			InLibrary: row.InLibrary != 0,
			Poster:    row.Poster,
		})
	}
	return out, nil
}
