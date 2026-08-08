package persistence

import (
	"context"
	"fmt"
	"time"

	"gorm.io/gorm"

	ports "github.com/alexmorbo/seasonfill/internal/shared/dataports"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
)

// GapRepository answers the read-only library-gap queries backing
// GET /api/v1/insights/gaps. A "gap" is a monitored, already-aired,
// fileless canonical episode (specials — season 0 — excluded). Every
// query is a bounded COUNT / top-N drill-down over EXISTING tables; no
// writes, no migration. SQL is dialect-portable — the "already aired"
// boundary is a bind param (never NOW()/INTERVAL), booleans bind as Go
// bools, and only EXISTS / IS NULL / COALESCE / CASE / GROUP BY / COUNT
// appear — so the SQLite test lane and the Postgres prod target agree.
type GapRepository struct {
	db *gorm.DB
}

func NewGapRepository(db *gorm.DB) *GapRepository {
	return &GapRepository{db: db}
}

// gapWhere is the shared AIRED-only gap predicate. Bind order:
// instance, monitored(true), has_file(false), now.
//
//   - deleted_at IS NULL       — SeriesDelete cascade soft-deletes.
//   - monitored = ?            — Sonarr is tracking the episode.
//   - has_file = ?             — no file on disk.
//   - season_number > 0        — specials (season 0) are never gaps.
//   - air_date IS NOT NULL     — a NULL air_date can't prove it aired.
//   - air_date <= ?            — a not-yet-aired episode is NOT a gap.
const gapWhere = `es.instance_name = ?
   AND es.deleted_at IS NULL
   AND es.monitored = ?
   AND es.has_file = ?
   AND e.season_number > 0
   AND e.air_date IS NOT NULL
   AND e.air_date <= ?`

type gapSeriesRankRow struct {
	SeriesID domain.SeriesID `gorm:"column:series_id"`
	Title    string          `gorm:"column:title"`
	GapCount int             `gorm:"column:gap_count"`
}

type gapEpisodeRow struct {
	SeriesID             domain.SeriesID  `gorm:"column:series_id"`
	Title                string           `gorm:"column:title"`
	SeasonNumber         int              `gorm:"column:season_number"`
	EpisodeNumber        int              `gorm:"column:episode_number"`
	EpisodeID            domain.EpisodeID `gorm:"column:episode_id"`
	AirDate              *time.Time       `gorm:"column:air_date"`
	SeasonAiredMonitored int              `gorm:"column:season_aired_monitored"`
	SeasonMissing        int              `gorm:"column:season_missing"`
}

// DistinctInstances — instance_name values with at least one live
// episode_states row, ordered ascending.
func (r *GapRepository) DistinctInstances(ctx context.Context) ([]string, error) {
	var names []string
	if err := dbFromContext(ctx, r.db).WithContext(ctx).
		Raw(`SELECT DISTINCT instance_name
		       FROM episode_states
		      WHERE deleted_at IS NULL
		      ORDER BY instance_name ASC`).
		Scan(&names).Error; err != nil {
		return nil, fmt.Errorf("gaps distinct_instances: %w", err)
	}
	return names, nil
}

// MissingEpisodeCount — total aired monitored fileless episodes
// (season > 0) for the instance.
func (r *GapRepository) MissingEpisodeCount(ctx context.Context, instance string, now time.Time) (int, error) {
	var count int64
	if err := dbFromContext(ctx, r.db).WithContext(ctx).
		Raw(`SELECT COUNT(*)
		       FROM episode_states es
		       JOIN episodes e ON e.id = es.episode_id
		      WHERE `+gapWhere,
			instance, true, false, now).
		Scan(&count).Error; err != nil {
		return 0, fmt.Errorf("gaps missing_episode_count: %w", err)
	}
	return int(count), nil
}

// WholeSeasonMissingCount — number of (series, season) pairs whose every
// aired monitored episode (season > 0) lacks a file. Grouped over the
// aired-monitored set; a season counts when ALL of it is fileless. Bind
// order: instance, monitored(true), now, has_file(false).
func (r *GapRepository) WholeSeasonMissingCount(ctx context.Context, instance string, now time.Time) (int, error) {
	var count int64
	if err := dbFromContext(ctx, r.db).WithContext(ctx).
		Raw(`SELECT COUNT(*) FROM (
		         SELECT e.series_id, e.season_number
		           FROM episode_states es
		           JOIN episodes e ON e.id = es.episode_id
		          WHERE es.instance_name = ?
		            AND es.deleted_at IS NULL
		            AND es.monitored = ?
		            AND e.season_number > 0
		            AND e.air_date IS NOT NULL
		            AND e.air_date <= ?
		          GROUP BY e.series_id, e.season_number
		         HAVING COUNT(*) > 0
		            AND SUM(CASE WHEN es.has_file = ? THEN 1 ELSE 0 END) = COUNT(*)
		       ) t`,
			instance, true, now, false).
		Scan(&count).Error; err != nil {
		return 0, fmt.Errorf("gaps whole_season_missing_count: %w", err)
	}
	return int(count), nil
}

// GapSeriesRanked — the AUTHORITATIVE top-N series drill-down list: every
// series with at least one gap, its EXACT instance-wide gap total, ordered
// biggest-gap-first (series_id ASC tiebreak so the order is deterministic).
// GROUP BY e.series_id, s.original_title keeps both the grouped id and the
// title in the grouping key, so COALESCE(s.original_title,”) is a function
// of a grouping column — valid strict-SQL on Postgres AND accepted by
// SQLite (one series = one series row, so the extra key never splits it).
// Bind order: instance, monitored(true), has_file(false), now, limitSeries.
func (r *GapRepository) GapSeriesRanked(ctx context.Context, instance string, now time.Time, limitSeries int) ([]ports.GapSeriesRank, error) {
	const sqlText = `
		SELECT e.series_id AS series_id,
		       COALESCE(s.original_title, '') AS title,
		       COUNT(*) AS gap_count
		  FROM episode_states es
		  JOIN episodes e ON e.id = es.episode_id
		  JOIN series s ON s.id = e.series_id
		 WHERE ` + gapWhere + `
		 GROUP BY e.series_id, s.original_title
		 ORDER BY COUNT(*) DESC, e.series_id ASC
		 LIMIT ?`

	var rows []gapSeriesRankRow
	if err := dbFromContext(ctx, r.db).WithContext(ctx).
		Raw(sqlText, instance, true, false, now, limitSeries).
		Scan(&rows).Error; err != nil {
		return nil, fmt.Errorf("gaps series_ranked: %w", err)
	}

	items := make([]ports.GapSeriesRank, 0, len(rows))
	for _, row := range rows {
		items = append(items, ports.GapSeriesRank{
			SeriesID: row.SeriesID,
			Title:    row.Title,
			GapCount: row.GapCount,
		})
	}
	return items, nil
}

// GapEpisodesForSeries — the gap episodes for a fixed set of series (the
// ids returned by GapSeriesRanked). Each row carries the per-(series,
// season) aired-monitored + missing totals via correlated scalar
// subqueries (dialect-portable, no N+1). limitEpisodes is a generous
// SAFETY cap on the flat row count: because the series set/order/title/
// badge come from GapSeriesRanked, this LIMIT can only clip a tail
// series' episode list — it can NEVER drop a series from the report.
// An empty seriesIDs slice short-circuits (an empty SQL IN () is invalid);
// the slice is bound as []int64 so GORM's positional IN (?) expansion is
// unambiguous across drivers. Bind order:
//   - sub1 (aired-monitored): instance, monitored(true), now
//   - sub2 (missing):         instance, monitored(true), has_file(false), now
//   - outer gapWhere:         instance, monitored(true), has_file(false), now
//   - series ids (IN list)
//   - limit
func (r *GapRepository) GapEpisodesForSeries(ctx context.Context, instance string, now time.Time, seriesIDs []domain.SeriesID, limitEpisodes int) ([]ports.GapEpisodeRow, error) {
	if len(seriesIDs) == 0 {
		return []ports.GapEpisodeRow{}, nil
	}

	ids := make([]int64, 0, len(seriesIDs))
	for _, id := range seriesIDs {
		ids = append(ids, int64(id))
	}

	const sqlText = `
		SELECT e.series_id AS series_id,
		       COALESCE(s.original_title, '') AS title,
		       e.season_number AS season_number,
		       e.episode_number AS episode_number,
		       e.id AS episode_id,
		       e.air_date AS air_date,
		       (SELECT COUNT(*)
		          FROM episodes e2
		          JOIN episode_states es2 ON es2.episode_id = e2.id
		         WHERE e2.series_id = e.series_id
		           AND e2.season_number = e.season_number
		           AND es2.instance_name = ?
		           AND es2.deleted_at IS NULL
		           AND es2.monitored = ?
		           AND e2.season_number > 0
		           AND e2.air_date IS NOT NULL
		           AND e2.air_date <= ?) AS season_aired_monitored,
		       (SELECT COUNT(*)
		          FROM episodes e3
		          JOIN episode_states es3 ON es3.episode_id = e3.id
		         WHERE e3.series_id = e.series_id
		           AND e3.season_number = e.season_number
		           AND es3.instance_name = ?
		           AND es3.deleted_at IS NULL
		           AND es3.monitored = ?
		           AND es3.has_file = ?
		           AND e3.season_number > 0
		           AND e3.air_date IS NOT NULL
		           AND e3.air_date <= ?) AS season_missing
		  FROM episode_states es
		  JOIN episodes e ON e.id = es.episode_id
		  JOIN series s ON s.id = e.series_id
		 WHERE ` + gapWhere + `
		   AND e.series_id IN (?)
		 ORDER BY e.series_id ASC, e.season_number ASC, e.episode_number ASC
		 LIMIT ?`

	var rows []gapEpisodeRow
	if err := dbFromContext(ctx, r.db).WithContext(ctx).
		Raw(sqlText,
			instance, true, now, // sub1 aired-monitored
			instance, true, false, now, // sub2 missing
			instance, true, false, now, // outer gapWhere
			ids,           // e.series_id IN (?)
			limitEpisodes, // safety cap
		).
		Scan(&rows).Error; err != nil {
		return nil, fmt.Errorf("gaps gap_episodes_for_series: %w", err)
	}

	items := make([]ports.GapEpisodeRow, 0, len(rows))
	for _, row := range rows {
		items = append(items, ports.GapEpisodeRow{
			SeriesID:             row.SeriesID,
			Title:                row.Title,
			SeasonNumber:         row.SeasonNumber,
			EpisodeNumber:        row.EpisodeNumber,
			EpisodeID:            row.EpisodeID,
			AirDate:              row.AirDate,
			SeasonAiredMonitored: row.SeasonAiredMonitored,
			SeasonMissing:        row.SeasonMissing,
		})
	}
	return items, nil
}

// Ensure interface compliance at compile time.
var _ ports.GapRepository = (*GapRepository)(nil)
