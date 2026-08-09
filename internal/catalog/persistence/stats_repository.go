package persistence

import (
	"context"
	"fmt"

	"gorm.io/gorm"

	grab "github.com/alexmorbo/seasonfill/internal/grab/domain"
	ports "github.com/alexmorbo/seasonfill/internal/shared/dataports"
)

// StatsRepository answers the read-only library-statistics aggregations
// backing GET /api/v1/insights/stats. Source of truth for totals/genre/
// network is series_cache — it carries, per (instance, sonarr_series_id),
// both the internal series_id (for the genre/network joins) and the
// series-level size_on_disk_bytes/episode_file_count. grab_success reads
// grab_records; torrent_totals reads qbit_torrents. Every query is a
// bounded COUNT/SUM/AVG or top-N GROUP BY over EXISTING tables; no writes,
// no migration. SQL is dialect-portable (CASE/COALESCE/LIMIT only — no
// NOW()/INTERVAL/FILTER/cast) so the SQLite test lane and the Postgres
// prod target agree.
type StatsRepository struct {
	db *gorm.DB
}

func NewStatsRepository(db *gorm.DB) *StatsRepository {
	return &StatsRepository{db: db}
}

// liveCacheWhere is the shared live-cache predicate. Bind: instance.
const liveCacheWhere = `sc.instance_name = ? AND sc.deleted_at IS NULL`

// genreNameExpr resolves ONE localized name per genre without fanning the
// aggregation out over genres_i18n rows (which would inflate SUM). It is a
// correlated scalar subquery keyed on the grouping column sg.genre_id:
// prefer en-US, then en, then any language alphabetically; skip empty
// names; ” when the genre has no genres_i18n row. Language literals are
// inlined (constant, not user input) so no extra binds shift the order.
const genreNameExpr = `COALESCE((
    SELECT gi.name
      FROM genres_i18n gi
     WHERE gi.genre_id = sg.genre_id
       AND gi.name <> ''
     ORDER BY CASE
                WHEN gi.language = 'en-US' THEN 0
                WHEN gi.language = 'en'    THEN 1
                ELSE 2
              END, gi.language ASC
     LIMIT 1), '')`

type statsKindRow struct {
	Name        string `gorm:"column:name"`
	SeriesCount int    `gorm:"column:series_count"`
	SizeBytes   int64  `gorm:"column:size_bytes"`
}

// DistinctInstances — instance_name values with at least one live
// series_cache row, ordered ascending.
func (r *StatsRepository) DistinctInstances(ctx context.Context) ([]string, error) {
	var names []string
	if err := dbFromContext(ctx, r.db).WithContext(ctx).
		Raw(`SELECT DISTINCT instance_name
		       FROM series_cache
		      WHERE deleted_at IS NULL
		      ORDER BY instance_name ASC`).
		Scan(&names).Error; err != nil {
		return nil, fmt.Errorf("stats distinct_instances: %w", err)
	}
	return names, nil
}

// Totals — series_count / episodes_on_disk / total_size_bytes over live
// series_cache rows for the instance. COALESCE guards the SUM NULL that an
// empty rowset would otherwise scan.
func (r *StatsRepository) Totals(ctx context.Context, instance string) (ports.StatsTotals, error) {
	var row struct {
		SeriesCount    int   `gorm:"column:series_count"`
		EpisodesOnDisk int   `gorm:"column:episodes_on_disk"`
		TotalSizeBytes int64 `gorm:"column:total_size_bytes"`
	}
	if err := dbFromContext(ctx, r.db).WithContext(ctx).
		Raw(`SELECT COUNT(*) AS series_count,
		            COALESCE(SUM(episode_file_count), 0) AS episodes_on_disk,
		            COALESCE(SUM(size_on_disk_bytes), 0) AS total_size_bytes
		       FROM series_cache sc
		      WHERE `+liveCacheWhere, instance).
		Scan(&row).Error; err != nil {
		return ports.StatsTotals{}, fmt.Errorf("stats totals: %w", err)
	}
	return ports.StatsTotals{
		SeriesCount:    row.SeriesCount,
		EpisodesOnDisk: row.EpisodesOnDisk,
		TotalSizeBytes: row.TotalSizeBytes,
	}, nil
}

// ByGenre — top-N genres by summed size on disk. GROUP BY sg.genre_id keeps
// the name subquery a function of the grouping column (valid strict-SQL on
// Postgres, accepted by SQLite). COUNT(DISTINCT sc.series_id) is the series
// count (a series with two genre rows would otherwise double-count).
// Bind order: instance, limit.
func (r *StatsRepository) ByGenre(ctx context.Context, instance string, limit int) ([]ports.StatsKindBucket, error) {
	sqlText := `
		SELECT ` + genreNameExpr + ` AS name,
		       COUNT(DISTINCT sc.series_id) AS series_count,
		       COALESCE(SUM(sc.size_on_disk_bytes), 0) AS size_bytes
		  FROM series_cache sc
		  JOIN series_genres sg ON sg.series_id = sc.series_id
		 WHERE ` + liveCacheWhere + `
		 GROUP BY sg.genre_id
		 ORDER BY COALESCE(SUM(sc.size_on_disk_bytes), 0) DESC, sg.genre_id ASC
		 LIMIT ?`

	var rows []statsKindRow
	if err := dbFromContext(ctx, r.db).WithContext(ctx).
		Raw(sqlText, instance, limit).
		Scan(&rows).Error; err != nil {
		return nil, fmt.Errorf("stats by_genre: %w", err)
	}
	return toKindBuckets(rows), nil
}

// ByNetwork — top-N networks by summed size on disk. networks.name is
// inline (no i18n). GROUP BY sn.network_id, n.name mirrors gaps' grouped-
// title pattern so n.name is a grouping column on strict-SQL Postgres.
// Bind order: instance, limit.
func (r *StatsRepository) ByNetwork(ctx context.Context, instance string, limit int) ([]ports.StatsKindBucket, error) {
	const sqlText = `
		SELECT COALESCE(n.name, '') AS name,
		       COUNT(DISTINCT sc.series_id) AS series_count,
		       COALESCE(SUM(sc.size_on_disk_bytes), 0) AS size_bytes
		  FROM series_cache sc
		  JOIN series_networks sn ON sn.series_id = sc.series_id
		  JOIN networks n ON n.id = sn.network_id
		 WHERE ` + liveCacheWhere + `
		 GROUP BY sn.network_id, n.name
		 ORDER BY COALESCE(SUM(sc.size_on_disk_bytes), 0) DESC, sn.network_id ASC
		 LIMIT ?`

	var rows []statsKindRow
	if err := dbFromContext(ctx, r.db).WithContext(ctx).
		Raw(sqlText, instance, limit).
		Scan(&rows).Error; err != nil {
		return nil, fmt.Errorf("stats by_network: %w", err)
	}
	return toKindBuckets(rows), nil
}

// GrabSuccess — terminal-state grab_records breakdown for the instance.
// failed folds grab_failed + import_failed. COALESCE guards empty-rowset
// SUM NULLs. Bind order: grabbed, imported, grab_failed, import_failed,
// instance.
func (r *StatsRepository) GrabSuccess(ctx context.Context, instance string) (ports.StatsGrabCounts, error) {
	var row struct {
		Grabbed  int `gorm:"column:grabbed"`
		Imported int `gorm:"column:imported"`
		Failed   int `gorm:"column:failed"`
	}
	if err := dbFromContext(ctx, r.db).WithContext(ctx).
		Raw(`SELECT COALESCE(SUM(CASE WHEN status = ? THEN 1 ELSE 0 END), 0) AS grabbed,
		            COALESCE(SUM(CASE WHEN status = ? THEN 1 ELSE 0 END), 0) AS imported,
		            COALESCE(SUM(CASE WHEN status IN (?, ?) THEN 1 ELSE 0 END), 0) AS failed
		       FROM grab_records
		      WHERE instance_name = ?`,
			string(grab.StatusGrabbed),
			string(grab.StatusImported),
			string(grab.StatusGrabFailed),
			string(grab.StatusImportFailed),
			instance).
		Scan(&row).Error; err != nil {
		return ports.StatsGrabCounts{}, fmt.Errorf("stats grab_success: %w", err)
	}
	return ports.StatsGrabCounts{Grabbed: row.Grabbed, Imported: row.Imported, Failed: row.Failed}, nil
}

// TorrentTotals — present=true qbit_torrents rollup for the instance.
// avg_ratio = COALESCE(AVG(ratio),0) (per-torrent mean). Bind order:
// instance, present(true).
func (r *StatsRepository) TorrentTotals(ctx context.Context, instance string) (ports.StatsTorrentTotals, error) {
	var row struct {
		TorrentCount         int     `gorm:"column:torrent_count"`
		TotalUploadedBytes   int64   `gorm:"column:total_uploaded_bytes"`
		TotalDownloadedBytes int64   `gorm:"column:total_downloaded_bytes"`
		AvgRatio             float64 `gorm:"column:avg_ratio"`
	}
	if err := dbFromContext(ctx, r.db).WithContext(ctx).
		Raw(`SELECT COUNT(*) AS torrent_count,
		            COALESCE(SUM(uploaded), 0) AS total_uploaded_bytes,
		            COALESCE(SUM(downloaded), 0) AS total_downloaded_bytes,
		            COALESCE(AVG(ratio), 0) AS avg_ratio
		       FROM qbit_torrents
		      WHERE instance_name = ? AND present = ?`, instance, true).
		Scan(&row).Error; err != nil {
		return ports.StatsTorrentTotals{}, fmt.Errorf("stats torrent_totals: %w", err)
	}
	return ports.StatsTorrentTotals{
		TorrentCount:         row.TorrentCount,
		TotalUploadedBytes:   row.TotalUploadedBytes,
		TotalDownloadedBytes: row.TotalDownloadedBytes,
		AvgRatio:             row.AvgRatio,
	}, nil
}

func toKindBuckets(rows []statsKindRow) []ports.StatsKindBucket {
	out := make([]ports.StatsKindBucket, 0, len(rows))
	for _, row := range rows {
		out = append(out, ports.StatsKindBucket{
			Name:        row.Name,
			SeriesCount: row.SeriesCount,
			SizeBytes:   row.SizeBytes,
		})
	}
	return out
}

// Ensure interface compliance at compile time.
var _ ports.StatsRepository = (*StatsRepository)(nil)
