package persistence

import (
	"context"
	"fmt"
	"time"

	"gorm.io/gorm"

	"github.com/alexmorbo/seasonfill/internal/catalog/app/moviecalendar"
)

// MovieCalendarRepository queries movies' three release milestones for the
// calendar (Ф6-R-6a). Dialect-portable (column refs + bind params).
type MovieCalendarRepository struct{ db *gorm.DB }

func NewMovieCalendarRepository(db *gorm.DB) *MovieCalendarRepository {
	return &MovieCalendarRepository{db: db}
}

var _ moviecalendar.Repository = (*MovieCalendarRepository)(nil)

// Events returns theatrical/digital/physical release rows in [from,to]. Only
// movies present in at least one instance library are included (EXISTS
// movie_states) so the calendar shows the operator's movies, mirroring the TV
// calendar's library scope. Ordered by date ASC, then movie id ASC.
func (r *MovieCalendarRepository) Events(ctx context.Context, from, to time.Time) ([]moviecalendar.Row, error) {
	type dbRow struct {
		MovieID   int64
		TMDBID    *int64
		Title     string
		Poster    *string
		Milestone string
		Date      time.Time
	}
	const q = `
SELECT m.id AS movie_id, m.tmdb_id AS tmdb_id, m.title AS title, m.poster_asset AS poster,
       x.milestone AS milestone, x.date AS date
FROM movies m
JOIN (
  SELECT id, 'theatrical' AS milestone, release_date          AS date FROM movies WHERE release_date          IS NOT NULL
  UNION ALL
  SELECT id, 'digital'    AS milestone, digital_release_date  AS date FROM movies WHERE digital_release_date  IS NOT NULL
  UNION ALL
  SELECT id, 'physical'   AS milestone, physical_release_date AS date FROM movies WHERE physical_release_date IS NOT NULL
) x ON x.id = m.id
WHERE x.date >= ? AND x.date <= ?
  AND EXISTS (SELECT 1 FROM movie_states ms WHERE ms.movie_id = m.id AND ms.deleted_at IS NULL)
ORDER BY x.date ASC, m.id ASC`
	var rows []dbRow
	if err := dbFromContext(ctx, r.db).WithContext(ctx).Raw(q, from, to).Scan(&rows).Error; err != nil {
		return nil, fmt.Errorf("movie calendar events: %w", err)
	}
	out := make([]moviecalendar.Row, 0, len(rows))
	for _, d := range rows {
		out = append(out, moviecalendar.Row{MovieID: d.MovieID, TMDBID: d.TMDBID, Title: d.Title, Poster: d.Poster, Milestone: d.Milestone, Date: d.Date})
	}
	return out, nil
}
