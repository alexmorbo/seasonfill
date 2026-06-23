// top_kinds.go reads the "top-10 genres / top-10 networks BY local
// catalog occurrence" projection consumed by the discovery worker
// (story 506). PRD §5.1.1 line 645 specifies "top-10 genres /
// networks" without naming a ranking source; the implementation pins
// the source to local catalog COUNT(*) so a freshly-installed instance
// with an empty catalog returns 0 rows → the worker emits NO
// by_genre / by_network refreshes (avoids the cold-start chicken-and-
// egg of "show Drama-tagged TV when we have nothing tagged yet").
//
// Schema reference (internal/shared/db/models.go):
//   - GenreModel:        genres.id (PK), genres.tmdb_id (nullable)
//   - NetworkModel:      networks.id (PK), networks.tmdb_id (nullable)
//   - SeriesGenreModel:  series_genres(series_id, genre_id, position)
//   - SeriesNetworkModel: series_networks(series_id, network_id, position)
//
// Ranking key: tmdb_id (NOT the local PK) — the worker passes the
// returned ids verbatim to tmdb.DiscoverTV(with_genres=…) /
// (with_networks=…) which is TMDB-id-keyed. Rows whose tmdb_id is
// NULL (Sonarr-string fallback per PRD §5.4) are filtered out at
// SQL level so the worker never ships a NULL genre id to TMDB.
package persistence

import (
	"context"
	"fmt"

	"gorm.io/gorm"
)

// TopKindsReader exposes the two ranking projections needed by the
// discovery worker. Both reads project across the join + dictionary
// tables under a single GORM Raw().Scan() — the queries are
// covered by the partial-unique-on-tmdb_id index on
// genres/networks plus the composite PK on the join tables, so plan
// stays index-only on Postgres.
type TopKindsReader struct {
	db *gorm.DB
}

// NewTopKindsReader binds the reader to db.
func NewTopKindsReader(db *gorm.DB) *TopKindsReader {
	return &TopKindsReader{db: db}
}

// TopGenres returns up to `limit` TMDB genre ids sorted descending by
// occurrence count in series_genres. Genres with NULL tmdb_id are
// skipped (Sonarr-string fallback rows must not leak into TMDB queries).
// limit <= 0 returns []int{} without issuing a query.
//
// Cold-catalog: an empty join table returns []int{} (no rows) — the
// worker treats that as "skip by_genre refresh this tick".
func (r *TopKindsReader) TopGenres(ctx context.Context, limit int) ([]int, error) {
	if limit <= 0 {
		return []int{}, nil
	}
	const q = `
		SELECT g.tmdb_id
		  FROM series_genres sg
		  JOIN genres g ON g.id = sg.genre_id
		 WHERE g.tmdb_id IS NOT NULL
		 GROUP BY g.tmdb_id
		 ORDER BY COUNT(*) DESC, g.tmdb_id ASC
		 LIMIT ?`
	var ids []int
	if err := r.db.WithContext(ctx).Raw(q, limit).Scan(&ids).Error; err != nil {
		return nil, fmt.Errorf("top genres: %w", err)
	}
	if ids == nil {
		ids = []int{}
	}
	return ids, nil
}

// TopNetworks returns up to `limit` TMDB network ids sorted descending
// by occurrence count in series_networks. Same NULL-tmdb_id filter as
// TopGenres. limit <= 0 → []int{}; empty catalog → []int{}.
func (r *TopKindsReader) TopNetworks(ctx context.Context, limit int) ([]int, error) {
	if limit <= 0 {
		return []int{}, nil
	}
	const q = `
		SELECT n.tmdb_id
		  FROM series_networks sn
		  JOIN networks n ON n.id = sn.network_id
		 WHERE n.tmdb_id IS NOT NULL
		 GROUP BY n.tmdb_id
		 ORDER BY COUNT(*) DESC, n.tmdb_id ASC
		 LIMIT ?`
	var ids []int
	if err := r.db.WithContext(ctx).Raw(q, limit).Scan(&ids).Error; err != nil {
		return nil, fmt.Errorf("top networks: %w", err)
	}
	if ids == nil {
		ids = []int{}
	}
	return ids, nil
}
