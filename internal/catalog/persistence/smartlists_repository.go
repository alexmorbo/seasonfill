package persistence

import (
	"context"
	"database/sql/driver"
	"fmt"
	"time"

	"gorm.io/gorm"

	ports "github.com/alexmorbo/seasonfill/internal/shared/dataports"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
)

// SmartListsRepository answers the read-only "smart lists" shelves backing
// GET /api/v1/insights/lists. Source of instance membership is series_cache
// (deleted_at IS NULL); status/next_air_date come from the joined canonical
// `series` row; the aired/fileless facts come from episodes + episode_states
// exactly as the I-2 gap predicate. Every query is a bounded COUNT / top-N
// over EXISTING tables; no writes, no migration. SQL is dialect-portable —
// the wall-clock boundaries are bind params (never NOW()/INTERVAL), booleans
// bind as Go bools, status literals are inlined constants, and only
// LOWER/COALESCE/IN/GROUP BY/HAVING/LIMIT appear — so the SQLite test lane and
// the Postgres prod target agree.
type SmartListsRepository struct {
	db *gorm.DB
}

func NewSmartListsRepository(db *gorm.DB) *SmartListsRepository {
	return &SmartListsRepository{db: db}
}

type smartListSeriesRow struct {
	SeriesID     domain.SeriesID       `gorm:"column:series_id"`
	SonarrID     domain.SonarrSeriesID `gorm:"column:sonarr_id"`
	Title        string                `gorm:"column:title"`
	MissingCount int                   `gorm:"column:missing_count"`
	NextAirDate  *time.Time            `gorm:"column:next_air_date"`
	LastAiredAt  aggTime               `gorm:"column:last_aired_at"`
}

func (r smartListSeriesRow) toPort() ports.SmartListSeriesRow {
	out := ports.SmartListSeriesRow{
		SeriesID:     r.SeriesID,
		SonarrID:     r.SonarrID,
		Title:        r.Title,
		MissingCount: r.MissingCount,
		NextAirDate:  r.NextAirDate,
	}
	if r.LastAiredAt.Valid {
		t := r.LastAiredAt.Time.UTC()
		out.LastAiredAt = &t
	}
	return out
}

// aggTime scans a MAX(air_date) aggregate result. On Postgres the driver
// returns a real timestamp (time.Time). On SQLite (modernc/glebarez) an
// untyped aggregate expression has no column affinity, so the raw stored
// text arrives as a string/[]byte and must be parsed. Direct column reads
// never need this — only aggregate projections do.
type aggTime struct {
	Time  time.Time
	Valid bool
}

// aggTimeLayouts covers the modernc/glebarez SQLite storage format
// ("2006-01-02 15:04:05-07:00", optionally with fractional seconds) plus the
// RFC3339 variants, in most-specific-first order.
var aggTimeLayouts = []string{
	"2006-01-02 15:04:05.999999999-07:00",
	"2006-01-02 15:04:05-07:00",
	time.RFC3339Nano,
	time.RFC3339,
	"2006-01-02 15:04:05",
}

func (a *aggTime) Scan(src any) error {
	switch v := src.(type) {
	case nil:
		a.Time, a.Valid = time.Time{}, false
		return nil
	case time.Time:
		a.Time, a.Valid = v, true
		return nil
	case []byte:
		return a.parse(string(v))
	case string:
		return a.parse(v)
	default:
		return fmt.Errorf("smartlists: unsupported last_aired_at scan type %T", src)
	}
}

// Value satisfies driver.Valuer so GORM accepts aggTime as a scannable field.
// This column is read-only (aggregate projection); it is never written.
func (a aggTime) Value() (driver.Value, error) {
	if !a.Valid {
		return nil, nil
	}
	return a.Time, nil
}

func (a *aggTime) parse(s string) error {
	if s == "" {
		a.Time, a.Valid = time.Time{}, false
		return nil
	}
	for _, layout := range aggTimeLayouts {
		if t, err := time.Parse(layout, s); err == nil {
			a.Time, a.Valid = t, true
			return nil
		}
	}
	return fmt.Errorf("smartlists: cannot parse last_aired_at %q", s)
}

// DistinctInstances — instance_name values with at least one live series_cache
// row, ordered ascending.
func (r *SmartListsRepository) DistinctInstances(ctx context.Context) ([]string, error) {
	var names []string
	if err := dbFromContext(ctx, r.db).WithContext(ctx).
		Raw(`SELECT DISTINCT instance_name
		       FROM series_cache
		      WHERE deleted_at IS NULL
		      ORDER BY instance_name ASC`).
		Scan(&names).Error; err != nil {
		return nil, fmt.Errorf("smartlists distinct_instances: %w", err)
	}
	return names, nil
}

// endedIncompleteWhere is the shared ended-incomplete predicate. Bind order:
// instance, monitored(true), has_file(false), now.
const endedIncompleteWhere = `sc.instance_name = ?
   AND sc.deleted_at IS NULL
   AND es.deleted_at IS NULL
   AND es.monitored = ?
   AND es.has_file = ?
   AND e.season_number > 0
   AND e.air_date IS NOT NULL
   AND e.air_date <= ?
   AND LOWER(s.status) IN ('ended','canceled','cancelled')`

// EndedIncomplete — terminal-status series with the most aired-monitored-
// fileless episodes. Bind order: instance, monitored(true), has_file(false),
// now, limit.
func (r *SmartListsRepository) EndedIncomplete(ctx context.Context, instance string, now time.Time, limit int) ([]ports.SmartListSeriesRow, error) {
	const sqlText = `
		SELECT sc.series_id AS series_id,
		       sc.sonarr_series_id AS sonarr_id,
		       COALESCE(s.original_title, '') AS title,
		       COUNT(*) AS missing_count
		  FROM series_cache sc
		  JOIN series s ON s.id = sc.series_id
		  JOIN episodes e ON e.series_id = sc.series_id
		  JOIN episode_states es ON es.episode_id = e.id AND es.instance_name = sc.instance_name
		 WHERE ` + endedIncompleteWhere + `
		 GROUP BY sc.series_id, sc.sonarr_series_id, s.original_title
		 ORDER BY COUNT(*) DESC, sc.series_id ASC
		 LIMIT ?`

	var rows []smartListSeriesRow
	if err := dbFromContext(ctx, r.db).WithContext(ctx).
		Raw(sqlText, instance, true, false, now, limit).
		Scan(&rows).Error; err != nil {
		return nil, fmt.Errorf("smartlists ended_incomplete: %w", err)
	}
	return toPortRows(rows), nil
}

// EndedIncompleteCount — exact number of series on the ended_incomplete shelf.
// Bind order: instance, monitored(true), has_file(false), now.
func (r *SmartListsRepository) EndedIncompleteCount(ctx context.Context, instance string, now time.Time) (int, error) {
	const sqlText = `
		SELECT COUNT(*) FROM (
		  SELECT 1
		    FROM series_cache sc
		    JOIN series s ON s.id = sc.series_id
		    JOIN episodes e ON e.series_id = sc.series_id
		    JOIN episode_states es ON es.episode_id = e.id AND es.instance_name = sc.instance_name
		   WHERE ` + endedIncompleteWhere + `
		   GROUP BY sc.series_id, sc.sonarr_series_id
		) t`

	var count int64
	if err := dbFromContext(ctx, r.db).WithContext(ctx).
		Raw(sqlText, instance, true, false, now).
		Scan(&count).Error; err != nil {
		return 0, fmt.Errorf("smartlists ended_incomplete_count: %w", err)
	}
	return int(count), nil
}

// ReturningSoon — series whose next_air_date is in [now, until]. Bind order:
// instance, now, until, limit.
func (r *SmartListsRepository) ReturningSoon(ctx context.Context, instance string, now, until time.Time, limit int) ([]ports.SmartListSeriesRow, error) {
	const sqlText = `
		SELECT sc.series_id AS series_id,
		       sc.sonarr_series_id AS sonarr_id,
		       COALESCE(s.original_title, '') AS title,
		       s.next_air_date AS next_air_date
		  FROM series_cache sc
		  JOIN series s ON s.id = sc.series_id
		 WHERE sc.instance_name = ?
		   AND sc.deleted_at IS NULL
		   AND s.next_air_date IS NOT NULL
		   AND s.next_air_date >= ?
		   AND s.next_air_date <= ?
		 ORDER BY s.next_air_date ASC, sc.series_id ASC
		 LIMIT ?`

	var rows []smartListSeriesRow
	if err := dbFromContext(ctx, r.db).WithContext(ctx).
		Raw(sqlText, instance, now, until, limit).
		Scan(&rows).Error; err != nil {
		return nil, fmt.Errorf("smartlists returning_soon: %w", err)
	}
	return toPortRows(rows), nil
}

// ReturningSoonCount — exact number of series on the returning_soon shelf.
// Bind order: instance, now, until.
func (r *SmartListsRepository) ReturningSoonCount(ctx context.Context, instance string, now, until time.Time) (int, error) {
	const sqlText = `
		SELECT COUNT(*)
		  FROM series_cache sc
		  JOIN series s ON s.id = sc.series_id
		 WHERE sc.instance_name = ?
		   AND sc.deleted_at IS NULL
		   AND s.next_air_date IS NOT NULL
		   AND s.next_air_date >= ?
		   AND s.next_air_date <= ?`

	var count int64
	if err := dbFromContext(ctx, r.db).WithContext(ctx).
		Raw(sqlText, instance, now, until).
		Scan(&count).Error; err != nil {
		return 0, fmt.Errorf("smartlists returning_soon_count: %w", err)
	}
	return int(count), nil
}

// hiatusWhere is the shared hiatus predicate. Bind order: instance, now.
const hiatusWhere = `sc.instance_name = ?
   AND sc.deleted_at IS NULL
   AND es.deleted_at IS NULL
   AND e.season_number > 0
   AND e.air_date IS NOT NULL
   AND e.air_date <= ?
   AND s.next_air_date IS NULL
   AND LOWER(s.status) IN ('returning series','continuing')`

// Hiatus — returning-status series with no scheduled next airing whose last
// aired episode is older than cutoff. Bind order: instance, now, cutoff, limit.
func (r *SmartListsRepository) Hiatus(ctx context.Context, instance string, now, cutoff time.Time, limit int) ([]ports.SmartListSeriesRow, error) {
	const sqlText = `
		SELECT sc.series_id AS series_id,
		       sc.sonarr_series_id AS sonarr_id,
		       COALESCE(s.original_title, '') AS title,
		       MAX(e.air_date) AS last_aired_at
		  FROM series_cache sc
		  JOIN series s ON s.id = sc.series_id
		  JOIN episodes e ON e.series_id = sc.series_id
		  JOIN episode_states es ON es.episode_id = e.id AND es.instance_name = sc.instance_name
		 WHERE ` + hiatusWhere + `
		 GROUP BY sc.series_id, sc.sonarr_series_id, s.original_title
		HAVING MAX(e.air_date) < ?
		 ORDER BY MAX(e.air_date) ASC, sc.series_id ASC
		 LIMIT ?`

	var rows []smartListSeriesRow
	if err := dbFromContext(ctx, r.db).WithContext(ctx).
		Raw(sqlText, instance, now, cutoff, limit).
		Scan(&rows).Error; err != nil {
		return nil, fmt.Errorf("smartlists hiatus: %w", err)
	}
	return toPortRows(rows), nil
}

// HiatusCount — exact number of series on the hiatus shelf. Bind order:
// instance, now, cutoff.
func (r *SmartListsRepository) HiatusCount(ctx context.Context, instance string, now, cutoff time.Time) (int, error) {
	const sqlText = `
		SELECT COUNT(*) FROM (
		  SELECT 1
		    FROM series_cache sc
		    JOIN series s ON s.id = sc.series_id
		    JOIN episodes e ON e.series_id = sc.series_id
		    JOIN episode_states es ON es.episode_id = e.id AND es.instance_name = sc.instance_name
		   WHERE ` + hiatusWhere + `
		   GROUP BY sc.series_id, sc.sonarr_series_id
		  HAVING MAX(e.air_date) < ?
		) t`

	var count int64
	if err := dbFromContext(ctx, r.db).WithContext(ctx).
		Raw(sqlText, instance, now, cutoff).
		Scan(&count).Error; err != nil {
		return 0, fmt.Errorf("smartlists hiatus_count: %w", err)
	}
	return int(count), nil
}

func toPortRows(rows []smartListSeriesRow) []ports.SmartListSeriesRow {
	out := make([]ports.SmartListSeriesRow, 0, len(rows))
	for _, row := range rows {
		out = append(out, row.toPort())
	}
	return out
}

// Ensure interface compliance at compile time.
var _ ports.SmartListsRepository = (*SmartListsRepository)(nil)
