// pickers.go reads the canonical genre / network lookup tables that
// back the discovery picker endpoints (story 507 N-2f):
//   - GET /api/v1/discovery/genres?lang=...
//   - GET /api/v1/discovery/networks?lang=...
//
// Genres carry a localised name via genres_i18n (PRD §5.3) — the read
// falls back to genres.name when the per-language row is missing.
// As of D-1 the `genres` table has NO `name` column (only PK + tmdb_id
// + audit columns) — the canonical name lives in genres_i18n. We
// resolve the fallback by reading the en-US row when the requested
// language is missing; if BOTH are missing the row is skipped at SQL
// level (LEFT JOIN with COALESCE on the i18n names).
//
// Networks have their name directly on the entity (NetworkModel.Name)
// — no per-language table. The localisation argument is accepted for
// API uniformity but is currently ignored at SQL level.
//
// Both readers filter rows whose `tmdb_id` is NULL — the URL-safe id
// the picker emits is the TMDB id (consistent with
// /discovery/genre/:id and /discovery/network/:id route shapes).
package persistence

import (
	"context"
	"fmt"

	"gorm.io/gorm"
)

// GenrePickItem is one row of GET /api/v1/discovery/genres. ID is the
// TMDB genre id (URL-safe). Name is the requested-language name when
// present, en-US name when not, never the empty string (rows missing
// both are filtered at SQL level).
type GenrePickItem struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

// GenresPickerRepo lists the TMDB genres known to the local catalog,
// localised. Read-only.
type GenresPickerRepo struct {
	db *gorm.DB
}

// NewGenresPickerRepo binds the reader to db.
func NewGenresPickerRepo(db *gorm.DB) *GenresPickerRepo {
	return &GenresPickerRepo{db: db}
}

// List returns the catalog of TMDB genres, localised to lang with an
// en-US fallback. Genres with NULL tmdb_id are skipped (Sonarr-string
// fallback rows never leak into the discovery URL surface). Empty
// catalog → empty slice (never nil).
//
// Sort: COALESCE(localised, fallback) ASC for a stable picker order.
//
// lang is consumed verbatim. The caller validates BCP-47 at the HTTP
// boundary; the repo does not double-check. Empty lang collapses to
// en-US — the LEFT JOIN turns into a self-join and the fallback row
// is returned as the primary translation.
func (r *GenresPickerRepo) List(ctx context.Context, lang string) ([]GenrePickItem, error) {
	if lang == "" {
		lang = "en-US"
	}
	const q = `
		SELECT g.tmdb_id AS id,
		       COALESCE(gi_pri.name, gi_fb.name) AS name
		  FROM genres g
		  LEFT JOIN genres_i18n gi_pri
		         ON gi_pri.genre_id = g.id AND gi_pri.language = ?
		  LEFT JOIN genres_i18n gi_fb
		         ON gi_fb.genre_id = g.id AND gi_fb.language = ?
		 WHERE g.tmdb_id IS NOT NULL
		   AND COALESCE(gi_pri.name, gi_fb.name) IS NOT NULL
		 ORDER BY COALESCE(gi_pri.name, gi_fb.name) ASC, g.tmdb_id ASC`
	var rows []GenrePickItem
	if err := r.db.WithContext(ctx).Raw(q, lang, "en-US").Scan(&rows).Error; err != nil {
		return nil, fmt.Errorf("genres picker: list: %w", err)
	}
	if rows == nil {
		rows = []GenrePickItem{}
	}
	return rows, nil
}

// NetworkPickItem is one row of GET /api/v1/discovery/networks.
type NetworkPickItem struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

// NetworksPickerRepo lists the TMDB networks known to the local
// catalog. Read-only. Networks are not localised (brand names stay
// canonical) but the lang argument is accepted at the port for
// API uniformity.
type NetworksPickerRepo struct {
	db *gorm.DB
}

// NewNetworksPickerRepo binds the reader to db.
func NewNetworksPickerRepo(db *gorm.DB) *NetworksPickerRepo {
	return &NetworksPickerRepo{db: db}
}

// List returns the catalog of TMDB networks sorted by name. Networks
// with NULL tmdb_id are filtered. lang is accepted but ignored
// (networks have no i18n table).
func (r *NetworksPickerRepo) List(ctx context.Context, _ string) ([]NetworkPickItem, error) {
	const q = `
		SELECT n.tmdb_id AS id, n.name AS name
		  FROM networks n
		 WHERE n.tmdb_id IS NOT NULL
		 ORDER BY n.name ASC, n.tmdb_id ASC`
	var rows []NetworkPickItem
	if err := r.db.WithContext(ctx).Raw(q).Scan(&rows).Error; err != nil {
		return nil, fmt.Errorf("networks picker: list: %w", err)
	}
	if rows == nil {
		rows = []NetworkPickItem{}
	}
	return rows, nil
}
