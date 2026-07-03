package persistence

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	"gorm.io/gorm"

	database "github.com/alexmorbo/seasonfill/internal/shared/db"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
	"github.com/alexmorbo/seasonfill/internal/watchdog/domain/cooldown"
	"github.com/alexmorbo/seasonfill/internal/watchdog/domain/regrab"
)

// WatchdogSeasonRow is the read-only join projection driving the
// `/watchdog/seasons` aggregate page. Every field is populated for the
// origin_releases row that produced it; the optional pointer fields
// (Cooldown / WatchdogState / Blacklist) are filled in by the repository
// from sibling tables. D-1 / 467b: replaced legacy NoBetterCounter
// pointer with WatchdogState (which folds NoBetterCounter semantics).
type WatchdogSeasonRow struct {
	InstanceName domain.InstanceName
	SeriesID     domain.SonarrSeriesID
	// CanonSeriesID is the resolved canon series.id (from the
	// series_cache → series JOIN). Story E-1-B7: keys the series_texts
	// localization lookup. Zero only on a broken row with no canon JOIN
	// (the handler then keeps the canon title).
	CanonSeriesID     domain.SeriesID
	SeasonNumber      int
	SeriesTitle       string
	Monitored         bool
	MissingAiredCount int
	LastAiredAt       *time.Time

	OriginGUID        string
	OriginIndexerName string
	OriginFirstSeenAt time.Time
	OriginLastSeenAt  time.Time
	OriginLastUsedAt  *time.Time

	Cooldown      *cooldown.Cooldown
	WatchdogState *regrab.WatchdogState
	Blacklist     *regrab.BlacklistEntry
}

// WatchdogSeasonsFilter is the optional filter set the
// /watchdog/seasons handler applies before paging.
type WatchdogSeasonsFilter struct {
	Instance        domain.InstanceName
	Q               string
	CooldownOnly    bool
	BlacklistedOnly bool
}

// WatchdogSeasonsCursor is the keyset cursor for the seasons page.
// Pages descend on (instance_name, series_id, season_number) so a
// stable order is preserved across snapshots even when instance names
// share series ids (which they routinely do).
type WatchdogSeasonsCursor struct {
	InstanceName domain.InstanceName
	SeriesID     domain.SonarrSeriesID
	SeasonNumber int
}

// WatchdogSeasonsRepository serves the season-aggregate read view
// behind the `/watchdog/seasons` and `/watchdog/series/:instance/:id`
// endpoints. Read-only — the underlying tables are written by the
// scan / grab / regrab / watchdog use cases.
type WatchdogSeasonsRepository struct {
	db *gorm.DB
}

// watchdogSeriesTextsTitleExpr resolves a series' base display title from
// series_texts at the en-US tier. S-E3a — canon series.title was dropped from
// the domain (column now dead), so the watchdog base title comes from
// series_texts; the handler layer still overrides it with the request-language
// row via CanonSeriesID. `alias` is the series-table alias in the surrounding
// query. NULL (no en-US row) scans as "" — same "no title" signal the old
// canon read produced for cold rows.
func watchdogSeriesTextsTitleExpr(alias string) string {
	return "(SELECT st.title FROM series_texts st WHERE st.series_id = " + alias + ".id " +
		"ORDER BY CASE WHEN st.language = 'en-US' THEN 1 ELSE 0 END DESC, st.language ASC LIMIT 1)"
}

func NewWatchdogSeasonsRepository(db *gorm.DB) *WatchdogSeasonsRepository {
	return &WatchdogSeasonsRepository{db: db}
}

// ListSeasons returns the requested page of WatchdogSeasonRow rows. The
// primary driver is `origin_releases` — every row there is a
// (instance, series, season) triple the watchdog has touched. The
// sibling tables (series_cache, cooldowns, watchdog_state,
// watchdog_blacklist) are batch enrichment so a row with only an
// origin row still appears in the result.
//
// limit must be > 0. The handler caps it at 500; the repository does
// not enforce that ceiling. The returned slice has at most `limit`
// rows.
func (r *WatchdogSeasonsRepository) ListSeasons(
	ctx context.Context, f WatchdogSeasonsFilter, limit int, cur *WatchdogSeasonsCursor, now time.Time,
) ([]WatchdogSeasonRow, *WatchdogSeasonsCursor, error) {
	if limit <= 0 {
		return nil, nil, errors.New("watchdog_seasons: limit must be positive")
	}

	type joined struct {
		InstanceName  domain.InstanceName   `gorm:"column:instance_name"`
		SeriesID      domain.SonarrSeriesID `gorm:"column:series_id"`
		CanonSeriesID domain.SeriesID       `gorm:"column:canon_series_id"`
		SeasonNumber  int                   `gorm:"column:season_number"`
		GUID          string                `gorm:"column:guid"`
		IndexerName   string                `gorm:"column:indexer_name"`
		FirstSeenAt   time.Time             `gorm:"column:first_seen_at"`
		LastSeenAt    time.Time             `gorm:"column:last_seen_at"`
		LastUsedAt    *time.Time            `gorm:"column:last_used_at"`
		Title         *string               `gorm:"column:title"`
		Monitored     *bool                 `gorm:"column:monitored"`
		MissingCount  *int                  `gorm:"column:missing_count"`
		LastAiredAt   *time.Time            `gorm:"column:last_aired_at"`
	}

	// origin_releases is append-only. Two INNER JOINs filter ghosts:
	//   * sonarr_instance — drops rows whose instance_name no longer
	//     matches a configured instance.
	//   * series_cache    — drops rows for series with no cache row.
	q := dbFromContext(ctx, r.db).WithContext(ctx).
		Table("origin_releases o").
		Select(`o.instance_name AS instance_name,
			o.series_id AS series_id,
			s.id AS canon_series_id,
			o.season_number AS season_number,
			o.guid AS guid,
			o.indexer_name AS indexer_name,
			o.first_seen_at AS first_seen_at,
			o.last_seen_at AS last_seen_at,
			o.last_used_at AS last_used_at,
			` + watchdogSeriesTextsTitleExpr("s") + ` AS title,
			sc.monitored AS monitored,
			sc.missing_count AS missing_count,
			s.last_air_date AS last_aired_at`).
		Joins("JOIN series_cache sc ON sc.instance_name = o.instance_name AND sc.sonarr_series_id = o.series_id AND sc.deleted_at IS NULL").
		// S-E3a — canon series.title is dead; the base display title resolves
		// from series_texts (en-US). The ghost-row filter that was
		// `AND s.title <> ''` becomes an EXISTS over a non-empty en-US text row.
		Joins("JOIN series s ON s.id = sc.series_id AND EXISTS (SELECT 1 FROM series_texts st WHERE st.series_id = s.id AND st.title IS NOT NULL AND st.title <> '')").
		Joins("JOIN sonarr_instance si ON si.name = o.instance_name")

	if f.Instance != "" {
		q = q.Where("o.instance_name = ?", f.Instance)
	}
	if f.Q != "" {
		q = q.Where(watchdogSeriesTextsTitleExpr("s")+" LIKE ?", "%"+f.Q+"%")
	}
	if cur != nil {
		q = q.Where("(o.instance_name, o.series_id, o.season_number) > (?, ?, ?)",
			cur.InstanceName, cur.SeriesID, cur.SeasonNumber)
	}

	// Fetch one extra row to drive the next-cursor decision.
	fetch := limit + 1
	var rows []joined
	if err := q.Order("o.instance_name ASC, o.series_id ASC, o.season_number ASC").
		Limit(fetch).
		Find(&rows).Error; err != nil {
		return nil, nil, fmt.Errorf("list watchdog seasons: %w", err)
	}

	var next *WatchdogSeasonsCursor
	if len(rows) > limit {
		last := rows[limit-1]
		next = &WatchdogSeasonsCursor{
			InstanceName: last.InstanceName,
			SeriesID:     last.SeriesID,
			SeasonNumber: last.SeasonNumber,
		}
		rows = rows[:limit]
	}

	out := make([]WatchdogSeasonRow, 0, len(rows))
	for _, j := range rows {
		row := WatchdogSeasonRow{
			InstanceName:      j.InstanceName,
			SeriesID:          j.SeriesID,
			CanonSeriesID:     j.CanonSeriesID,
			SeasonNumber:      j.SeasonNumber,
			OriginGUID:        j.GUID,
			OriginIndexerName: j.IndexerName,
			OriginFirstSeenAt: j.FirstSeenAt,
			OriginLastSeenAt:  j.LastSeenAt,
			OriginLastUsedAt:  j.LastUsedAt,
			LastAiredAt:       j.LastAiredAt,
		}
		if j.Title != nil {
			row.SeriesTitle = *j.Title
		}
		if j.Monitored != nil {
			row.Monitored = *j.Monitored
		}
		if j.MissingCount != nil {
			row.MissingAiredCount = *j.MissingCount
		}
		out = append(out, row)
	}

	if err := r.enrichSiblings(ctx, out, now); err != nil {
		return nil, nil, fmt.Errorf("enrich watchdog seasons: %w", err)
	}

	// Apply the cooldown_only / blacklisted_only post-filters in Go.
	if f.CooldownOnly || f.BlacklistedOnly {
		filtered := out[:0]
		for _, row := range out {
			if f.CooldownOnly && row.Cooldown == nil {
				continue
			}
			if f.BlacklistedOnly && row.Blacklist == nil {
				continue
			}
			filtered = append(filtered, row)
		}
		out = filtered
	}

	return out, next, nil
}

// enrichSiblings fills the Cooldown / WatchdogState / Blacklist pointer
// fields on every row in rows. Performs at most three batch SELECTs
// against the sibling tables — one per table — and is a no-op when
// rows is empty.
func (r *WatchdogSeasonsRepository) enrichSiblings(ctx context.Context, rows []WatchdogSeasonRow, now time.Time) error {
	if len(rows) == 0 {
		return nil
	}

	db := dbFromContext(ctx, r.db).WithContext(ctx)

	// Build cooldown lookup keyed on (scope=series, key=instance:series_id:season).
	cooldownKeys := make([]string, 0, len(rows))
	cooldownIdx := make(map[string]int, len(rows))
	for i, row := range rows {
		k := cooldown.SeriesKey(row.InstanceName, row.SeriesID, row.SeasonNumber)
		cooldownKeys = append(cooldownKeys, k)
		cooldownIdx[k] = i
	}
	var cdModels []database.CooldownModel
	if err := db.Where("scope = ? AND key IN ? AND expires_at > ?",
		string(cooldown.ScopeSeries), cooldownKeys, now).
		Find(&cdModels).Error; err != nil {
		return fmt.Errorf("load cooldowns: %w", err)
	}
	for _, m := range cdModels {
		idx, ok := cooldownIdx[m.Key]
		if !ok {
			continue
		}
		cd := cooldown.Cooldown{
			Scope:     cooldown.Scope(m.Scope),
			Key:       m.Key,
			ExpiresAt: m.ExpiresAt,
			Reason:    m.Reason,
			CreatedAt: m.CreatedAt,
		}
		rows[idx].Cooldown = &cd
	}

	// watchdog_state + watchdog_blacklist both key on (instance_name,
	// sonarr_series_id, season_number). Pull the per-row triple set
	// and run one IN-clause query per table.
	type triple struct {
		instance domain.InstanceName
		seriesID domain.SonarrSeriesID
		season   int
	}
	tripleIdx := make(map[triple]int, len(rows))
	instances := make(map[domain.InstanceName]struct{})
	for i, row := range rows {
		tripleIdx[triple{row.InstanceName, row.SeriesID, row.SeasonNumber}] = i
		instances[row.InstanceName] = struct{}{}
	}
	if len(tripleIdx) == 0 {
		return nil
	}
	instList := make([]domain.InstanceName, 0, len(instances))
	for n := range instances {
		instList = append(instList, n)
	}

	var stateModels []database.WatchdogStateModel
	if err := db.Where("instance_name IN ?", instList).
		Find(&stateModels).Error; err != nil {
		return fmt.Errorf("load watchdog_state: %w", err)
	}
	for _, m := range stateModels {
		idx, ok := tripleIdx[triple{m.InstanceName, m.SonarrSeriesID, m.SeasonNumber}]
		if !ok {
			continue
		}
		ws := toWatchdogState(m)
		rows[idx].WatchdogState = &ws
	}

	var blModels []database.WatchdogBlacklistModel
	if err := db.Where("instance_name IN ?", instList).
		Where("ttl_until IS NULL OR ttl_until > ?", now).
		Find(&blModels).Error; err != nil {
		return fmt.Errorf("load blacklist: %w", err)
	}
	for _, m := range blModels {
		idx, ok := tripleIdx[triple{m.InstanceName, m.SonarrSeriesID, m.SeasonNumber}]
		if !ok {
			continue
		}
		bl := toBlacklistEntry(m)
		rows[idx].Blacklist = &bl
	}

	return nil
}

// SeasonsForSeries returns every (instance, series, season) row for a
// single series. Powers GET /watchdog/series/:instance/:id. Rows come
// back sorted ascending by SeasonNumber so the handler can render the
// list in airing order without a follow-up sort. The instance does NOT
// have to have an origin_releases row for the season to appear — we
// also fold in seasons we have decision rows for, so a season that
// the watchdog skipped (no grab → no origin row) still surfaces.
func (r *WatchdogSeasonsRepository) SeasonsForSeries(
	ctx context.Context, instance domain.InstanceName, seriesID domain.SonarrSeriesID, now time.Time,
) ([]WatchdogSeasonRow, error) {
	if instance == "" || seriesID <= 0 {
		return nil, errors.New("watchdog_seasons: instance and series_id required")
	}

	db := dbFromContext(ctx, r.db).WithContext(ctx)

	// Origin rows for this series.
	var origins []database.OriginReleaseModel
	if err := db.Where("instance_name = ? AND series_id = ?", instance, seriesID).
		Find(&origins).Error; err != nil {
		return nil, fmt.Errorf("load origin rows: %w", err)
	}

	// Distinct season numbers we've ever decided on for this series.
	type seasonRow struct {
		SeasonNumber int `gorm:"column:season_number"`
	}
	var decisionSeasons []seasonRow
	if err := db.Table("decisions").
		Select("DISTINCT season_number").
		Where("instance_name = ? AND series_id = ?", instance, seriesID).
		Find(&decisionSeasons).Error; err != nil {
		return nil, fmt.Errorf("load decision seasons: %w", err)
	}

	// Series cache + canon row — title / monitored / aired metadata.
	var sc struct {
		CanonSeriesID domain.SeriesID `gorm:"column:canon_series_id"`
		Title         string          `gorm:"column:title"`
		Monitored     bool            `gorm:"column:monitored"`
		MissingCount  int             `gorm:"column:missing_count"`
		LastAiredAt   *time.Time      `gorm:"column:last_aired_at"`
	}
	scFound := true
	err := db.Table("series_cache").
		Select(`s.id AS canon_series_id,
			`+watchdogSeriesTextsTitleExpr("s")+` AS title,
			series_cache.monitored AS monitored,
			series_cache.missing_count AS missing_count,
			s.last_air_date AS last_aired_at`).
		Joins("INNER JOIN series s ON s.id = series_cache.series_id").
		Where("series_cache.instance_name = ? AND series_cache.sonarr_series_id = ?", instance, seriesID).
		Limit(1).
		Scan(&sc).Error
	if err != nil {
		return nil, fmt.Errorf("load series_cache: %w", err)
	}
	if sc.Title == "" {
		scFound = false
	}

	seasonSet := make(map[int]WatchdogSeasonRow, len(origins)+len(decisionSeasons))
	for _, o := range origins {
		row := WatchdogSeasonRow{
			InstanceName:      instance,
			SeriesID:          seriesID,
			SeasonNumber:      o.SeasonNumber,
			OriginGUID:        o.GUID,
			OriginIndexerName: o.IndexerName,
			OriginFirstSeenAt: o.FirstSeenAt,
			OriginLastSeenAt:  o.LastSeenAt,
			OriginLastUsedAt:  o.LastUsedAt,
		}
		seasonSet[o.SeasonNumber] = row
	}
	for _, ds := range decisionSeasons {
		if _, ok := seasonSet[ds.SeasonNumber]; ok {
			continue
		}
		seasonSet[ds.SeasonNumber] = WatchdogSeasonRow{
			InstanceName: instance,
			SeriesID:     seriesID,
			SeasonNumber: ds.SeasonNumber,
		}
	}

	for k, row := range seasonSet {
		if scFound {
			row.CanonSeriesID = sc.CanonSeriesID
			row.SeriesTitle = sc.Title
			row.Monitored = sc.Monitored
			row.MissingAiredCount = sc.MissingCount
			row.LastAiredAt = sc.LastAiredAt
		}
		seasonSet[k] = row
	}

	out := make([]WatchdogSeasonRow, 0, len(seasonSet))
	for _, row := range seasonSet {
		out = append(out, row)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].SeasonNumber < out[j].SeasonNumber })

	if err := r.enrichSiblings(ctx, out, now); err != nil {
		return nil, fmt.Errorf("enrich watchdog seasons for series: %w", err)
	}

	return out, nil
}

// SeasonStatsFromDecisions returns the most-recent (TotalEpisodes,
// AiredEpisodes, ExistingEpisodes) snapshot per (instance, series,
// season). One row per season; seasons with no decisions row return
// zero. The handler maps the result into
// dto.WatchdogSeriesSeasonStats per season.
func (r *WatchdogSeasonsRepository) SeasonStatsFromDecisions(
	ctx context.Context, instance domain.InstanceName, seriesID domain.SonarrSeriesID,
) (map[int]WatchdogSeasonStats, error) {
	db := dbFromContext(ctx, r.db).WithContext(ctx)

	var rows []struct {
		SeasonNumber     int       `gorm:"column:season_number"`
		AiredEpisodes    int       `gorm:"column:aired_episodes"`
		ExistingEpisodes int       `gorm:"column:existing_episodes"`
		CreatedAt        time.Time `gorm:"column:created_at"`
	}
	if err := db.Table("decisions").
		Select("season_number, aired_episodes, existing_episodes, created_at").
		Where("instance_name = ? AND series_id = ?", instance, seriesID).
		Order("created_at DESC, id DESC").
		Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("load season stats: %w", err)
	}
	out := make(map[int]WatchdogSeasonStats, len(rows))
	for _, r := range rows {
		if _, ok := out[r.SeasonNumber]; ok {
			continue
		}
		out[r.SeasonNumber] = WatchdogSeasonStats{
			AiredEpisodes:    r.AiredEpisodes,
			ExistingEpisodes: r.ExistingEpisodes,
		}
	}
	return out, nil
}

// WatchdogSeasonStats — repository-side projection of the
// `decisions.aired_episodes` + `decisions.existing_episodes` pair for
// one season.
type WatchdogSeasonStats struct {
	AiredEpisodes    int
	ExistingEpisodes int
}

// RecentDecisionRow is the read-only projection driving the per-season
// recent_decisions trailer. Capped at 20 most-recent-first by the
// repo.
type RecentDecisionRow struct {
	ID        string
	ScanRunID string
	Decision  string
	Reason    string
	CreatedAt time.Time
}

// RecentGrabRow is the read-only projection driving the per-season
// recent_grabs trailer. Capped at 20 most-recent-first by the repo.
type RecentGrabRow struct {
	ID           string
	ReleaseTitle string
	Status       string
	ReplayOfID   *string
	TorrentHash  *domain.QbitHash
	CreatedAt    time.Time
}

// RecentDecisionsBySeason returns the per-season decisions slice,
// capped at perSeason most-recent per season. Empty seasons return an
// empty (non-nil) slice in the map.
func (r *WatchdogSeasonsRepository) RecentDecisionsBySeason(
	ctx context.Context, instance domain.InstanceName, seriesID domain.SonarrSeriesID, perSeason int,
) (map[int][]RecentDecisionRow, error) {
	if perSeason <= 0 {
		return map[int][]RecentDecisionRow{}, nil
	}
	type row struct {
		ID           string    `gorm:"column:id"`
		ScanRunID    string    `gorm:"column:scan_run_id"`
		SeasonNumber int       `gorm:"column:season_number"`
		Decision     string    `gorm:"column:decision"`
		Reason       string    `gorm:"column:reason"`
		CreatedAt    time.Time `gorm:"column:created_at"`
	}
	var rows []row
	db := dbFromContext(ctx, r.db).WithContext(ctx)
	if err := db.Table("decisions").
		Select("id, scan_run_id, season_number, decision, reason, created_at").
		Where("instance_name = ? AND series_id = ?", instance, seriesID).
		Order("created_at DESC, id DESC").
		Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("recent decisions: %w", err)
	}
	out := make(map[int][]RecentDecisionRow)
	for _, r := range rows {
		bucket := out[r.SeasonNumber]
		if len(bucket) >= perSeason {
			continue
		}
		out[r.SeasonNumber] = append(bucket, RecentDecisionRow{
			ID:        r.ID,
			ScanRunID: r.ScanRunID,
			Decision:  r.Decision,
			Reason:    r.Reason,
			CreatedAt: r.CreatedAt,
		})
	}
	return out, nil
}

// RecentGrabsBySeason returns the per-season grabs slice, capped at
// perSeason most-recent per season.
func (r *WatchdogSeasonsRepository) RecentGrabsBySeason(
	ctx context.Context, instance domain.InstanceName, seriesID domain.SonarrSeriesID, perSeason int,
) (map[int][]RecentGrabRow, error) {
	if perSeason <= 0 {
		return map[int][]RecentGrabRow{}, nil
	}
	type row struct {
		ID           string           `gorm:"column:id"`
		SeasonNumber int              `gorm:"column:season_number"`
		ReleaseTitle string           `gorm:"column:release_title"`
		Status       string           `gorm:"column:status"`
		ReplayOfID   *string          `gorm:"column:replay_of_id"`
		TorrentHash  *domain.QbitHash `gorm:"column:torrent_hash"`
		CreatedAt    time.Time        `gorm:"column:created_at"`
	}
	var rows []row
	db := dbFromContext(ctx, r.db).WithContext(ctx)
	if err := db.Table("grab_records").
		Select("id, season_number, release_title, status, replay_of_id, torrent_hash, created_at").
		Where("instance_name = ? AND series_id = ?", instance, seriesID).
		Order("created_at DESC, id DESC").
		Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("recent grabs: %w", err)
	}
	out := make(map[int][]RecentGrabRow)
	for _, r := range rows {
		bucket := out[r.SeasonNumber]
		if len(bucket) >= perSeason {
			continue
		}
		out[r.SeasonNumber] = append(bucket, RecentGrabRow{
			ID:           r.ID,
			ReleaseTitle: r.ReleaseTitle,
			Status:       r.Status,
			ReplayOfID:   r.ReplayOfID,
			TorrentHash:  r.TorrentHash,
			CreatedAt:    r.CreatedAt,
		})
	}
	return out, nil
}
