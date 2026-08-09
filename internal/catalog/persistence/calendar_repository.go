package persistence

import (
	"context"
	"fmt"
	"time"

	"gorm.io/gorm"

	ports "github.com/alexmorbo/seasonfill/internal/shared/dataports"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
)

// CalendarRepository answers the read-only release-calendar query backing
// GET /api/v1/calendar. It emits one flat row per (episode × matching live
// episode_states instance); a followed-not-in-library episode emits one row
// with a NULL instance. Milestone FLAGS (premiere/finale) + prev_air_date
// (for the Go return-gap test) come from correlated subqueries; the title
// and poster use the any-lang side-table fallback (en-US→en→any, requested
// lang first) so a non-en-only series never resolves to a hole (W18-15).
// SQL is dialect-portable — the window bound is a Go-bound param, and only
// EXISTS / COALESCE / CASE / correlated MAX() / LIMIT appear, so the SQLite
// test lane and the Postgres prod target agree.
type CalendarRepository struct {
	db *gorm.DB
}

func NewCalendarRepository(db *gorm.DB) *CalendarRepository {
	return &CalendarRepository{db: db}
}

// calendarTitleExpr — one localized title (requested lang → en-US → en → any
// alphabetical) with a canon original_title backstop. Mirrors stats.genreNameExpr.
// ONE bind: the requested lang (empty "" matches nothing → pure any-lang).
const calendarTitleExpr = `COALESCE((
    SELECT st.title FROM series_texts st
     WHERE st.series_id = e.series_id
       AND st.title IS NOT NULL AND st.title <> ''
     ORDER BY CASE
                WHEN st.language = ?       THEN 0
                WHEN st.language = 'en-US' THEN 1
                WHEN st.language = 'en'    THEN 2
                ELSE 3
              END, st.language ASC
     LIMIT 1), s.original_title, '')`

// calendarPosterExpr — one localized poster hash, same fallback order. ONE
// bind: the requested lang. NULL when no language carries a non-empty poster.
const calendarPosterExpr = `(
    SELECT smt.poster_asset FROM series_media_texts smt
     WHERE smt.series_id = e.series_id
       AND smt.poster_asset IS NOT NULL AND smt.poster_asset <> ''
     ORDER BY CASE
                WHEN smt.language = ?       THEN 0
                WHEN smt.language = 'en-US' THEN 1
                WHEN smt.language = 'en'    THEN 2
                ELSE 3
              END, smt.language ASC
     LIMIT 1)`

type calendarEventRow struct {
	EpisodeID     domain.EpisodeID `gorm:"column:episode_id"`
	SeriesID      domain.SeriesID  `gorm:"column:series_id"`
	TMDBID        *int64           `gorm:"column:tmdb_id"`
	SeasonNumber  int              `gorm:"column:season_number"`
	EpisodeNumber int              `gorm:"column:episode_number"`
	AirDate       time.Time        `gorm:"column:air_date"`
	Title         string           `gorm:"column:title"`
	Poster        *string          `gorm:"column:poster"`
	IsPremiere    int              `gorm:"column:is_premiere"`
	IsFinale      int              `gorm:"column:is_finale"`
	PrevAirDate   aggTime          `gorm:"column:prev_air_date"`
	Followed      int              `gorm:"column:followed"`
	InstanceName  *string          `gorm:"column:instance_name"`
	HasFile       *bool            `gorm:"column:has_file"`
	Monitored     *bool            `gorm:"column:monitored"`
}

// Events runs the windowed calendar read. Bind order (positional, in SQL
// appearance order):
//  1. title lang  (SELECT calendarTitleExpr)
//  2. poster lang (SELECT calendarPosterExpr)
//  3. from        (WHERE air_date >= ?)
//  4. to          (WHERE air_date <= ?)
//  5. instance    (scope EXISTS, ONLY when q.Instance != "" and scope != "followed")
//  6. limit       (LIMIT ?)
func (r *CalendarRepository) Events(ctx context.Context, q ports.CalendarQuery) ([]ports.CalendarEventRow, error) {
	args := []any{q.Lang, q.Lang, q.From, q.To}

	var scopeSQL string
	switch q.Scope {
	case "library":
		if q.Instance != "" {
			scopeSQL = `EXISTS (SELECT 1 FROM series_cache sc
			              WHERE sc.series_id = e.series_id AND sc.deleted_at IS NULL
			                AND sc.instance_name = ?)`
			args = append(args, q.Instance)
		} else {
			scopeSQL = `EXISTS (SELECT 1 FROM series_cache sc
			              WHERE sc.series_id = e.series_id AND sc.deleted_at IS NULL)`
		}
	case "followed":
		scopeSQL = `EXISTS (SELECT 1 FROM followed_series fs WHERE fs.series_id = e.series_id)`
	default: // "all"
		if q.Instance != "" {
			scopeSQL = `(EXISTS (SELECT 1 FROM series_cache sc
			               WHERE sc.series_id = e.series_id AND sc.deleted_at IS NULL
			                 AND sc.instance_name = ?)
			         OR EXISTS (SELECT 1 FROM followed_series fs WHERE fs.series_id = e.series_id))`
			args = append(args, q.Instance)
		} else {
			scopeSQL = `(EXISTS (SELECT 1 FROM series_cache sc
			               WHERE sc.series_id = e.series_id AND sc.deleted_at IS NULL)
			         OR EXISTS (SELECT 1 FROM followed_series fs WHERE fs.series_id = e.series_id))`
		}
	}
	args = append(args, q.Limit)

	sqlText := `
		SELECT e.id            AS episode_id,
		       e.series_id      AS series_id,
		       s.tmdb_id        AS tmdb_id,
		       e.season_number  AS season_number,
		       e.episode_number AS episode_number,
		       e.air_date       AS air_date,
		       ` + calendarTitleExpr + `  AS title,
		       ` + calendarPosterExpr + ` AS poster,
		       CASE WHEN e.episode_number = 1 THEN 1 ELSE 0 END AS is_premiere,
		       CASE WHEN e.episode_number = (
		              SELECT MAX(e2.episode_number) FROM episodes e2
		               WHERE e2.series_id = e.series_id
		                 AND e2.season_number = e.season_number
		            ) THEN 1 ELSE 0 END AS is_finale,
		       (SELECT MAX(e3.air_date) FROM episodes e3
		          WHERE e3.series_id = e.series_id
		            AND e3.season_number > 0
		            AND e3.air_date IS NOT NULL
		            AND e3.air_date < e.air_date) AS prev_air_date,
		       CASE WHEN EXISTS (SELECT 1 FROM followed_series fs
		                          WHERE fs.series_id = e.series_id)
		            THEN 1 ELSE 0 END AS followed,
		       es.instance_name AS instance_name,
		       es.has_file      AS has_file,
		       es.monitored     AS monitored
		  FROM episodes e
		  JOIN series s ON s.id = e.series_id
		  LEFT JOIN episode_states es
		    ON es.episode_id = e.id AND es.deleted_at IS NULL
		 WHERE e.season_number > 0
		   AND e.air_date IS NOT NULL
		   AND e.air_date >= ?
		   AND e.air_date <= ?
		   AND (` + scopeSQL + `)
		 ORDER BY e.air_date ASC, e.series_id ASC, e.season_number ASC,
		          e.episode_number ASC, es.instance_name ASC
		 LIMIT ?`

	var rows []calendarEventRow
	if err := dbFromContext(ctx, r.db).WithContext(ctx).
		Raw(sqlText, args...).Scan(&rows).Error; err != nil {
		return nil, fmt.Errorf("calendar events: %w", err)
	}

	out := make([]ports.CalendarEventRow, 0, len(rows))
	for _, row := range rows {
		// prev_air_date is a MAX() aggregate — untyped on SQLite (arrives as
		// text), a real timestamp on Postgres; aggTime absorbs both.
		var prev *time.Time
		if row.PrevAirDate.Valid {
			t := row.PrevAirDate.Time.UTC()
			prev = &t
		}
		out = append(out, ports.CalendarEventRow{
			EpisodeID:     row.EpisodeID,
			SeriesID:      row.SeriesID,
			TMDBID:        row.TMDBID,
			SeasonNumber:  row.SeasonNumber,
			EpisodeNumber: row.EpisodeNumber,
			AirDate:       row.AirDate,
			Title:         row.Title,
			Poster:        row.Poster,
			IsPremiere:    row.IsPremiere == 1,
			IsFinale:      row.IsFinale == 1,
			PrevAirDate:   prev,
			Followed:      row.Followed == 1,
			InstanceName:  row.InstanceName,
			HasFile:       row.HasFile,
			Monitored:     row.Monitored,
		})
	}
	return out, nil
}

// Ensure interface compliance at compile time.
var _ ports.CalendarRepository = (*CalendarRepository)(nil)
