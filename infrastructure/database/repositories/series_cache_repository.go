package repositories

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/alexmorbo/seasonfill/application/ports"
	"github.com/alexmorbo/seasonfill/domain/series"
	"github.com/alexmorbo/seasonfill/infrastructure/database"
)

// SeriesCacheRepository persists per-instance Sonarr series metadata
// (D66). Upsert resurrects soft-deleted rows by clearing deleted_at —
// the scan and queue handlers see Sonarr "ground truth" again whenever
// the series re-appears in /api/v3/series, while the row's identity
// (and any grab_records FK references) is preserved.
type SeriesCacheRepository struct {
	db *gorm.DB
}

func NewSeriesCacheRepository(db *gorm.DB) *SeriesCacheRepository {
	return &SeriesCacheRepository{db: db}
}

func (r *SeriesCacheRepository) Get(ctx context.Context, instanceName string, sonarrSeriesID int) (series.CacheEntry, error) {
	var m database.SeriesCacheModel
	err := dbFromContext(ctx, r.db).WithContext(ctx).
		Where("instance_name = ? AND sonarr_series_id = ?", instanceName, sonarrSeriesID).
		First(&m).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return series.CacheEntry{}, ports.ErrNotFound
		}
		return series.CacheEntry{}, fmt.Errorf("get series_cache: %w", err)
	}
	entry, cErr := toCacheEntry(m)
	if cErr != nil {
		return series.CacheEntry{}, fmt.Errorf("decode series_cache: %w", cErr)
	}
	return entry, nil
}

// Upsert writes/replaces the row keyed on composite PK. The conflict
// path always sets deleted_at = NULL. Callers wanting soft-delete use
// SoftDelete, not Upsert with DeletedAt set.
func (r *SeriesCacheRepository) Upsert(ctx context.Context, entry series.CacheEntry) error {
	if entry.InstanceName == "" {
		return fmt.Errorf("upsert series_cache: instance_name must be non-empty")
	}
	if entry.SonarrSeriesID == 0 {
		return fmt.Errorf("upsert series_cache: sonarr_series_id must be non-zero")
	}
	now := time.Now().UTC()
	entry.UpdatedAt = now
	entry.DeletedAt = nil

	m, mErr := cacheEntryToModel(entry)
	if mErr != nil {
		return fmt.Errorf("encode series_cache: %w", mErr)
	}

	res := dbFromContext(ctx, r.db).WithContext(ctx).Clauses(clause.OnConflict{
		Columns: []clause.Column{
			{Name: "instance_name"},
			{Name: "sonarr_series_id"},
		},
		DoUpdates: clause.AssignmentColumns([]string{
			"title", "title_slug", "year",
			"tvdb_id", "imdb_id", "tmdb_id",
			"status", "network", "genres",
			"runtime_minutes", "monitored", "overview",
			"poster_path", "fanart_path", "banner_path",
			"missing_count", "last_aired_at",
			"updated_at", "deleted_at",
		}),
	}).Create(&m)
	if res.Error != nil {
		return fmt.Errorf("upsert series_cache: %w", res.Error)
	}
	return nil
}

// SoftDelete stamps deleted_at = now. Idempotent — missing row OR
// already-deleted row both return nil. The 041f webhook fires
// SeriesDelete for IDs that may never have been cached; surfacing
// ErrNotFound would just spam logs without driving any action.
func (r *SeriesCacheRepository) SoftDelete(ctx context.Context, instanceName string, sonarrSeriesID int) error {
	now := time.Now().UTC()
	res := dbFromContext(ctx, r.db).WithContext(ctx).
		Model(&database.SeriesCacheModel{}).
		Where("instance_name = ? AND sonarr_series_id = ?", instanceName, sonarrSeriesID).
		Updates(map[string]interface{}{
			"deleted_at": now,
			"updated_at": now,
		})
	if res.Error != nil {
		return fmt.Errorf("soft delete series_cache: %w", res.Error)
	}
	return nil
}

func (r *SeriesCacheRepository) ListActiveByInstance(ctx context.Context, instanceName string) ([]series.CacheEntry, error) {
	var models []database.SeriesCacheModel
	err := dbFromContext(ctx, r.db).WithContext(ctx).
		Where("instance_name = ? AND deleted_at IS NULL", instanceName).
		Find(&models).Error
	if err != nil {
		return nil, fmt.Errorf("list active series_cache: %w", err)
	}
	out := make([]series.CacheEntry, 0, len(models))
	for _, m := range models {
		entry, cErr := toCacheEntry(m)
		if cErr != nil {
			return nil, fmt.Errorf("decode series_cache: %w", cErr)
		}
		out = append(out, entry)
	}
	return out, nil
}

// ListByFilter implements the B3 list endpoint backing query. The
// `imported` state filter is an EXISTS subquery against grab_records.
// The `missing` state filter is missing_count > 0. The `all` state is
// the unnarrowed active set. Story 120: `filter.Search`, when set,
// adds a case-insensitive substring predicate over (title, title_slug).
// Keyset pagination over the chosen sort key.
func (r *SeriesCacheRepository) ListByFilter(
	ctx context.Context,
	instanceName string,
	filter ports.SeriesCacheFilter,
	sort ports.SeriesCacheSort,
	page ports.Pagination,
) ([]series.CacheEntry, int, bool, *ports.Cursor, error) {
	if instanceName == "" {
		return nil, 0, false, nil, fmt.Errorf("list series_cache: instance_name must be non-empty")
	}
	if page.Limit <= 0 || page.Limit > ports.MaxListLimit {
		return nil, 0, false, nil, fmt.Errorf("list series_cache: %w", ports.ErrInvalidLimit)
	}
	if !sort.IsValid() {
		sort = ports.SeriesCacheSortUpdatedDesc
	}
	if filter.State == "" {
		filter.State = ports.SeriesCacheStateAll
	}
	if !filter.State.IsValid() {
		return nil, 0, false, nil, fmt.Errorf("list series_cache: invalid state %q", filter.State)
	}

	db := dbFromContext(ctx, r.db).WithContext(ctx)
	base := db.Model(&database.SeriesCacheModel{}).
		Where("instance_name = ? AND deleted_at IS NULL", instanceName)
	base = applyStateFilter(base, filter.State)
	base = applySearchFilter(base, filter.Search)
	base = applyMonitoredFilter(base, filter.MonitoredOnly)
	base = applyNetworksFilter(base, filter.Networks)

	var total int64
	if err := base.Session(&gorm.Session{}).Count(&total).Error; err != nil {
		return nil, 0, false, nil, fmt.Errorf("count series_cache: %w", err)
	}

	q := base.Session(&gorm.Session{})
	q = applyCursor(q, sort, page.Cursor)
	q = applyOrder(q, sort)

	var models []database.SeriesCacheModel
	if err := q.Limit(page.Limit + 1).Find(&models).Error; err != nil {
		return nil, 0, false, nil, fmt.Errorf("list series_cache: %w", err)
	}

	hasMore := false
	var next *ports.Cursor
	if len(models) > page.Limit {
		hasMore = true
		last := models[page.Limit-1]
		next = cursorFromModel(last, sort)
		models = models[:page.Limit]
	}

	out := make([]series.CacheEntry, 0, len(models))
	for _, m := range models {
		entry, cErr := toCacheEntry(m)
		if cErr != nil {
			return nil, 0, false, nil, fmt.Errorf("decode series_cache: %w", cErr)
		}
		out = append(out, entry)
	}
	return out, int(total), hasMore, next, nil
}

// applyStateFilter narrows the query for the imported / missing /
// all enum. imported uses an EXISTS subquery against grab_records
// keyed on (instance_name, series_id) — Phase 4 indexes cover this.
// 7d cutoff is computed Go-side so SQLite (tests) and Postgres (prod)
// run the same plan.
func applyStateFilter(q *gorm.DB, state ports.SeriesCacheState) *gorm.DB {
	switch state {
	case ports.SeriesCacheStateImported:
		cutoff := time.Now().UTC().Add(-7 * 24 * time.Hour)
		return q.Where(
			"EXISTS (SELECT 1 FROM grab_records gr "+
				"WHERE gr.instance_name = series_cache.instance_name "+
				"AND gr.series_id = series_cache.sonarr_series_id "+
				"AND gr.status = ? "+
				"AND gr.created_at >= ?)",
			"imported", cutoff,
		)
	case ports.SeriesCacheStateMissing:
		return q.Where("missing_count > 0")
	default:
		return q
	}
}

// applySearchFilter adds a case-insensitive substring predicate over
// (title, title_slug) when q is non-empty. Story 120: uses
// `LOWER(col) LIKE LOWER(?)` rather than Postgres ILIKE so the same
// expression runs on SQLite (tests) and Postgres (prod) without a
// dialect branch. The pattern is wrapped in `%…%` after wildcard
// escaping so user input cannot smuggle SQL wildcards. An empty
// trimmed q ⇒ no-op (returns the unmodified query).
func applySearchFilter(q *gorm.DB, search string) *gorm.DB {
	trimmed := strings.TrimSpace(search)
	if trimmed == "" {
		return q
	}
	pat := "%" + escapeLikePattern(trimmed) + "%"
	return q.Where(
		"(LOWER(title) LIKE LOWER(?) ESCAPE '\\' "+
			"OR LOWER(title_slug) LIKE LOWER(?) ESCAPE '\\')",
		pat, pat,
	)
}

// applyMonitoredFilter narrows the query to monitored=true or
// monitored=false rows when the tri-state pointer is set. nil ⇒
// no-op. Story 121a: needed for keyset-paginated /series filtering
// because client-side `monitoredOnly` couldn't see rows past page 1.
func applyMonitoredFilter(q *gorm.DB, only *bool) *gorm.DB {
	if only == nil {
		return q
	}
	return q.Where("monitored = ?", *only)
}

// applyNetworksFilter narrows the query to rows whose `network`
// column matches any of the supplied names. Empty slice ⇒ no-op
// (the repo edge MUST refuse to emit `IN ()` which Postgres rejects).
// Story 121a: the /series facet panel needs server-side narrowing
// because the panel itself reads from a separate distinct endpoint.
func applyNetworksFilter(q *gorm.DB, networks []string) *gorm.DB {
	if len(networks) == 0 {
		return q
	}
	return q.Where("network IN ?", networks)
}

// escapeLikePattern doubles every LIKE meta-character (`%`, `_`, `\`)
// so the value matches literally inside a wrapped `%…%` pattern. The
// ESCAPE '\' clause is paired in the query so the escape works on both
// SQLite and Postgres. Order matters: `\` must be replaced first to
// avoid double-escaping the `\` introduced by the other two.
func escapeLikePattern(in string) string {
	r := strings.NewReplacer(
		`\`, `\\`,
		`%`, `\%`,
		`_`, `\_`,
	)
	return r.Replace(in)
}

// applyCursor adds the keyset predicate for the chosen sort key.
// updated_desc:    WHERE (updated_at, sonarr_series_id) < (ts, id)
// title_asc:       WHERE (LOWER(title), sonarr_series_id) > (title, id)
// air_date_desc:   nil-aware:
//
//	WHEN cursor row had non-NULL last_aired_at:
//	  WHERE (last_aired_at, sonarr_series_id) < (ts, id)
//	  OR last_aired_at IS NULL
//	WHEN cursor row had NULL last_aired_at:
//	  WHERE last_aired_at IS NULL AND sonarr_series_id < id
//
// nil cursor = first page (no predicate).
func applyCursor(q *gorm.DB, sort ports.SeriesCacheSort, cur *ports.Cursor) *gorm.DB {
	if cur == nil {
		return q
	}
	switch sort {
	case ports.SeriesCacheSortTitleAsc:
		title, sid := splitTitleCursorID(cur.ID)
		return q.Where("(LOWER(title), sonarr_series_id) > (?, ?)", title, sid)
	case ports.SeriesCacheSortAirDateDesc:
		sid, _ := strconv.Atoi(cur.ID)
		if cur.Timestamp.IsZero() {
			// Previous page ended on a NULL-air row: stay in the NULL
			// tail and walk down by id only.
			return q.Where("last_aired_at IS NULL AND sonarr_series_id < ?", sid)
		}
		return q.Where(
			"(last_aired_at IS NOT NULL AND (last_aired_at, sonarr_series_id) < (?, ?)) OR last_aired_at IS NULL",
			cur.Timestamp, sid,
		)
	default:
		sid, _ := strconv.Atoi(cur.ID)
		return q.Where("(updated_at, sonarr_series_id) < (?, ?)", cur.Timestamp, sid)
	}
}

// applyOrder is the ORDER BY half of the sort. For air_date_desc we
// emit `last_aired_at IS NULL ASC` as the first key so non-NULL rows
// sort BEFORE NULL rows (NULLS LAST semantics) on both Postgres and
// SQLite — both engines treat `IS NULL` as a 0/1 expression and
// accept ASC ordering on it.
func applyOrder(q *gorm.DB, sort ports.SeriesCacheSort) *gorm.DB {
	switch sort {
	case ports.SeriesCacheSortTitleAsc:
		return q.Order("LOWER(title) ASC, sonarr_series_id ASC")
	case ports.SeriesCacheSortAirDateDesc:
		return q.Order("last_aired_at IS NULL ASC, last_aired_at DESC, sonarr_series_id DESC")
	default:
		return q.Order("updated_at DESC, sonarr_series_id DESC")
	}
}

// cursorFromModel produces the next-page cursor. For updated_desc:
// Timestamp=UpdatedAt + ID=decimal(series_id). For title_asc:
// Timestamp=zero + ID="lower(title)|series_id" packed string. For
// air_date_desc: Timestamp=LastAiredAt (zero when nil) + ID=decimal
// series_id; applyCursor reads the zero-Timestamp signal as "we are
// in the NULL tail".
func cursorFromModel(m database.SeriesCacheModel, sort ports.SeriesCacheSort) *ports.Cursor {
	switch sort {
	case ports.SeriesCacheSortTitleAsc:
		return &ports.Cursor{
			Timestamp: time.Time{},
			ID:        strings.ToLower(m.Title) + "|" + strconv.Itoa(m.SonarrSeriesID),
		}
	case ports.SeriesCacheSortAirDateDesc:
		var ts time.Time
		if m.LastAiredAt != nil {
			ts = m.LastAiredAt.UTC()
		}
		return &ports.Cursor{
			Timestamp: ts,
			ID:        strconv.Itoa(m.SonarrSeriesID),
		}
	default:
		return &ports.Cursor{
			Timestamp: m.UpdatedAt.UTC(),
			ID:        strconv.Itoa(m.SonarrSeriesID),
		}
	}
}

// splitTitleCursorID parses "title|id" cursor IDs. Malformed input
// degrades safely to ("", 0).
func splitTitleCursorID(raw string) (string, int) {
	i := strings.LastIndex(raw, "|")
	if i < 0 {
		return raw, 0
	}
	sid, _ := strconv.Atoi(raw[i+1:])
	return raw[:i], sid
}

// FetchLastGrabInfo aggregates the latest imported grab_records per
// series id in ONE query (defence against N+1). LastImportedEpisode is
// derived as "S{NN}" from the latest imported grab's season_number —
// grab_records does not store the episode list. F11 can later upgrade
// this by joining a future episodes table.
func (r *SeriesCacheRepository) FetchLastGrabInfo(
	ctx context.Context, instanceName string, seriesIDs []int,
) (map[int]ports.LastGrabInfo, error) {
	out := make(map[int]ports.LastGrabInfo, len(seriesIDs))
	if len(seriesIDs) == 0 {
		return out, nil
	}
	type row struct {
		SeriesID     int       `gorm:"column:series_id"`
		MaxCreatedAt time.Time `gorm:"column:max_created_at"`
		SeasonNumber int       `gorm:"column:season_number"`
	}
	var rows []row
	sub := dbFromContext(ctx, r.db).WithContext(ctx).
		Table("grab_records").
		Select("series_id, MAX(created_at) AS max_created_at").
		Where("instance_name = ? AND series_id IN ? AND status = ?",
			instanceName, seriesIDs, "imported").
		Group("series_id")
	err := dbFromContext(ctx, r.db).WithContext(ctx).
		Table("grab_records AS g").
		Select("g.series_id, g.created_at AS max_created_at, g.season_number").
		Joins("JOIN (?) AS agg ON g.series_id = agg.series_id AND g.created_at = agg.max_created_at",
			sub).
		Where("g.instance_name = ? AND g.status = ?", instanceName, "imported").
		Scan(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("fetch last_grab_info: %w", err)
	}
	for _, r := range rows {
		out[r.SeriesID] = ports.LastGrabInfo{
			LastGrabAt:          r.MaxCreatedAt.UTC(),
			LastImportedEpisode: formatSeasonTag(r.SeasonNumber),
		}
	}
	return out, nil
}

// ListDistinctNetworks returns the sorted, distinct, non-empty
// network strings for the instance's active rows. Story 121a §A.
func (r *SeriesCacheRepository) ListDistinctNetworks(
	ctx context.Context,
	instanceName string,
) ([]string, error) {
	if instanceName == "" {
		return nil, fmt.Errorf("list distinct networks: instance_name must be non-empty")
	}
	db := dbFromContext(ctx, r.db).WithContext(ctx)
	var rows []string
	err := db.Model(&database.SeriesCacheModel{}).
		Where("instance_name = ? AND deleted_at IS NULL", instanceName).
		Where("network IS NOT NULL AND network != ''").
		Distinct("network").
		Order("network ASC").
		Limit(ports.MaxDistinctNetworks).
		Pluck("network", &rows).Error
	if err != nil {
		return nil, fmt.Errorf("list distinct networks: %w", err)
	}
	return rows, nil
}

func formatSeasonTag(season int) string {
	if season <= 0 {
		return ""
	}
	if season < 10 {
		return "S0" + strconv.Itoa(season)
	}
	return "S" + strconv.Itoa(season)
}

// toCacheEntry maps DB model → domain. Genres JSON-decoded.
func toCacheEntry(m database.SeriesCacheModel) (series.CacheEntry, error) {
	var genres []string
	if m.Genres != nil && *m.Genres != "" {
		if err := json.Unmarshal([]byte(*m.Genres), &genres); err != nil {
			return series.CacheEntry{}, fmt.Errorf("unmarshal genres: %w", err)
		}
	}
	return series.CacheEntry{
		InstanceName:   m.InstanceName,
		SonarrSeriesID: m.SonarrSeriesID,
		Title:          m.Title,
		TitleSlug:      m.TitleSlug,
		Year:           m.Year,
		TVDBID:         m.TVDBID,
		IMDBID:         m.IMDBID,
		TMDBID:         m.TMDBID,
		Status:         m.Status,
		Network:        m.Network,
		Genres:         genres,
		RuntimeMinutes: m.RuntimeMinutes,
		Monitored:      m.Monitored,
		Overview:       m.Overview,
		PosterPath:     m.PosterPath,
		FanartPath:     m.FanartPath,
		BannerPath:     m.BannerPath,
		MissingCount:   m.MissingCount,
		LastAiredAt:    m.LastAiredAt,
		UpdatedAt:      m.UpdatedAt,
		DeletedAt:      m.DeletedAt,
	}, nil
}

// cacheEntryToModel: inverse of toCacheEntry. Genres JSON-encoded.
func cacheEntryToModel(e series.CacheEntry) (database.SeriesCacheModel, error) {
	var genresPtr *string
	if len(e.Genres) > 0 {
		raw, err := json.Marshal(e.Genres)
		if err != nil {
			return database.SeriesCacheModel{}, fmt.Errorf("marshal genres: %w", err)
		}
		s := string(raw)
		genresPtr = &s
	}
	return database.SeriesCacheModel{
		InstanceName:   e.InstanceName,
		SonarrSeriesID: e.SonarrSeriesID,
		Title:          e.Title,
		TitleSlug:      e.TitleSlug,
		Year:           e.Year,
		TVDBID:         e.TVDBID,
		IMDBID:         e.IMDBID,
		TMDBID:         e.TMDBID,
		Status:         e.Status,
		Network:        e.Network,
		Genres:         genresPtr,
		RuntimeMinutes: e.RuntimeMinutes,
		Monitored:      e.Monitored,
		Overview:       e.Overview,
		PosterPath:     e.PosterPath,
		FanartPath:     e.FanartPath,
		BannerPath:     e.BannerPath,
		MissingCount:   e.MissingCount,
		LastAiredAt:    e.LastAiredAt,
		UpdatedAt:      e.UpdatedAt,
		DeletedAt:      e.DeletedAt,
	}, nil
}

var _ ports.SeriesCacheRepository = (*SeriesCacheRepository)(nil)
