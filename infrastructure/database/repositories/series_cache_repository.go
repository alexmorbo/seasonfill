package repositories

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/alexmorbo/seasonfill/application/ports"
	"github.com/alexmorbo/seasonfill/infrastructure/database"
	"github.com/alexmorbo/seasonfill/internal/catalog/domain/series"
	"github.com/alexmorbo/seasonfill/internal/enrichment/persistence"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
	sharedErrors "github.com/alexmorbo/seasonfill/internal/shared/errors"
)

// SeriesCacheRepository persists the thin per-instance Sonarr projection
// (PRD v4 §5.11) post the 000032 cutover. The canon attributes
// (title / year / external ids / status / network / runtime / last_air)
// live on `series` and are JOIN-read via `series_cache.series_id`.
// Upsert resolves-or-creates the canon row through SeriesRepository
// before writing the thin row. Soft-deleted via deleted_at to preserve
// grab_records FK references.
type SeriesCacheRepository struct {
	db     *gorm.DB
	series *persistence.SeriesRepository
}

func NewSeriesCacheRepository(db *gorm.DB, series *persistence.SeriesRepository) *SeriesCacheRepository {
	return &SeriesCacheRepository{db: db, series: series}
}

// cacheRow is the internal row shape returned by every read path —
// the cache columns plus the canon columns JOINed from `series`.
// rowToCacheEntry projects it back onto the public series.CacheEntry
// shape so callers see no change.
type cacheRow struct {
	// series_cache columns
	InstanceName      domain.InstanceName   `gorm:"column:instance_name"`
	SonarrSeriesID    domain.SonarrSeriesID `gorm:"column:sonarr_series_id"`
	SeriesID          *domain.SeriesID      `gorm:"column:series_id"`
	TitleSlug         string                `gorm:"column:title_slug"`
	Monitored         bool                  `gorm:"column:monitored"`
	MissingCount      int                   `gorm:"column:missing_count"`
	EpisodeFileCount  int                   `gorm:"column:episode_file_count"`
	SizeOnDiskBytes   int64                 `gorm:"column:size_on_disk_bytes"`
	AiredEpisodeCount int                   `gorm:"column:aired_episode_count"`
	UpdatedAt         time.Time             `gorm:"column:updated_at"`
	DeletedAt         *time.Time            `gorm:"column:deleted_at"`
	// canon columns — JOINed from series (s.*).
	Title  string         `gorm:"column:s_title"`
	Year   *int           `gorm:"column:s_year"`
	TVDBID *domain.TVDBID `gorm:"column:s_tvdb_id"`
	IMDBID *domain.IMDBID `gorm:"column:s_imdb_id"`
	TMDBID *domain.TMDBID `gorm:"column:s_tmdb_id"`
	Status *string        `gorm:"column:s_status"`
	// Network FIELD REMOVED in E-1 — sourced via series_networks
	// subquery for ListDistinctNetworks; per-row network on detail
	// card lands in a future story.
	RuntimeMinutes *int       `gorm:"column:s_runtime_minutes"`
	LastAiredAt    *time.Time `gorm:"column:s_last_air_date"`
	// PosterAsset is the raw canon path read straight from
	// series.poster_asset. The handler layer derives the content-
	// addressed media hash from this path so the catalog tiles can
	// request /media/<hash> deterministically — there is no LEFT JOIN
	// on media_assets in this projection anymore, which means tiles
	// no longer wait for the downloader to write a 'stored' row before
	// they can show a poster. The on-demand fetch path on the media
	// handler is the recovery for "hash known, bytes not yet there".
	PosterAsset *string `gorm:"column:s_poster_asset"`
	// Genres / Overview / FanartPath / BannerPath: post-cutover canon
	// does not store these in the same form as the old series_cache
	// (genres → series_genres join; overview → series_texts; fanart/
	// banner → media_assets with hash). rowToCacheEntry returns nil
	// for them. The DTO already drops these fields per the lean-shape
	// comment in dto.go.
}

// seriesCacheSelect projects every joined column with the s_*
// prefix to avoid name collisions with series_cache.*. Used as the
// SELECT list on every read path.
//
// s_poster_asset projects the raw canon poster path. Handler-side
// helpers derive the content-addressed media hash deterministically
// (sha256 of the synthetic CDN URL) so the wire `poster_hash` is
// available the moment the canon row carries a path, independent of
// whether media_assets has caught up. This replaces the earlier LEFT
// JOIN on media_assets, which made tiles wait for the downloader.
const seriesCacheSelect = `
		series_cache.instance_name      AS instance_name,
		series_cache.sonarr_series_id   AS sonarr_series_id,
		series_cache.series_id          AS series_id,
		series_cache.title_slug         AS title_slug,
		series_cache.monitored          AS monitored,
		series_cache.missing_count      AS missing_count,
		series_cache.episode_file_count AS episode_file_count,
		series_cache.size_on_disk_bytes AS size_on_disk_bytes,
		series_cache.aired_episode_count AS aired_episode_count,
		series_cache.updated_at         AS updated_at,
		series_cache.deleted_at         AS deleted_at,
		s.title                         AS s_title,
		s.year                          AS s_year,
		s.tvdb_id                       AS s_tvdb_id,
		s.imdb_id                       AS s_imdb_id,
		s.tmdb_id                       AS s_tmdb_id,
		s.status                        AS s_status,
		s.runtime_minutes               AS s_runtime_minutes,
		s.last_air_date                 AS s_last_air_date,
		s.poster_asset                  AS s_poster_asset
	`

// seriesCacheJoin is the canon JOIN. INNER — every cache row has a
// canon row post-cutover; INNER catches stale data fast. No LEFT JOIN
// on media_assets: the FE poster_hash is derived from s.poster_asset
// in the handler layer.
const seriesCacheJoin = `INNER JOIN series s ON s.id = series_cache.series_id`

// Get returns the per-instance cache row joined to its canonical
// series. Missing row → typed SeriesCacheNotFoundError; F-2c-3
// dropped the legacy errors.Join(typed, ports.ErrNotFound) shim.
// Consumers (series-detail composer, cast composer, series-refresh
// usecase, series-torrents handler) either dispatch via c.Error
// (typed → 404) or wrap with %w (errors.As still walks the chain).
func (r *SeriesCacheRepository) Get(ctx context.Context, instanceName domain.InstanceName, sonarrSeriesID domain.SonarrSeriesID) (series.CacheEntry, error) {
	var row cacheRow
	err := dbFromContext(ctx, r.db).WithContext(ctx).
		Table("series_cache").
		Select(seriesCacheSelect).
		Joins(seriesCacheJoin).
		Where("series_cache.instance_name = ? AND series_cache.sonarr_series_id = ?",
			instanceName, sonarrSeriesID).
		First(&row).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return series.CacheEntry{}, &sharedErrors.SeriesCacheNotFoundError{
				InstanceName:   instanceName,
				SonarrSeriesID: sonarrSeriesID,
			}
		}
		return series.CacheEntry{}, fmt.Errorf("get series_cache: %w", err)
	}
	return rowToCacheEntry(row), nil
}

// Upsert writes/replaces the per-instance cache row keyed on
// (instance_name, sonarr_series_id). The new responsibility post-B-1b
// is to resolve-or-create the canonical `series` row first via
// SeriesRepository (TMDB > TVDB > IMDB > orphan-by-fingerprint
// priority) and write the resolved series_id onto the cache row.
//
// The canon write is "last-write-wins" by §5.8 (multiple Sonarr
// instances writing identical metadata for the same show is normal;
// E-1 will replace this with the merge-policy writer). The cache
// path always sets deleted_at = NULL — callers wanting soft-delete
// use SoftDelete.
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

	canonID, err := r.resolveOrCreateCanon(ctx, entry)
	if err != nil {
		return fmt.Errorf("upsert series_cache: resolve canon: %w", err)
	}

	m := database.SeriesCacheModel{
		InstanceName:      entry.InstanceName,
		SonarrSeriesID:    entry.SonarrSeriesID,
		SeriesID:          &canonID,
		TitleSlug:         entry.TitleSlug,
		Monitored:         entry.Monitored,
		MissingCount:      entry.MissingCount,
		EpisodeFileCount:  entry.EpisodeFileCount,
		SizeOnDiskBytes:   entry.SizeOnDiskBytes,
		AiredEpisodeCount: entry.AiredEpisodeCount,
		UpdatedAt:         entry.UpdatedAt,
		DeletedAt:         nil,
	}
	res := dbFromContext(ctx, r.db).WithContext(ctx).Clauses(clause.OnConflict{
		Columns: []clause.Column{
			{Name: "instance_name"},
			{Name: "sonarr_series_id"},
		},
		DoUpdates: clause.AssignmentColumns([]string{
			"series_id", "title_slug", "monitored", "missing_count",
			"episode_file_count", "size_on_disk_bytes",
			"aired_episode_count",
			"updated_at", "deleted_at",
		}),
	}).Create(&m)
	if res.Error != nil {
		return fmt.Errorf("upsert series_cache: %w", res.Error)
	}
	return nil
}

// resolveOrCreateCanon picks an existing series row by natural key
// (TMDB > TVDB > IMDB) or creates one for orphans. For B-1b this is
// last-write-wins on every canon column except id/created_at; E-1
// replaces this with merge-policy ordering. Returns the resolved id.
//
// Hydration is always 'stub' here — workers later flip to 'full'.
// PosterAsset stays nil — Sonarr URLs are not canon-shaped (canon
// stores hashes from F-1 media-prewarm).
func (r *SeriesCacheRepository) resolveOrCreateCanon(ctx context.Context, e series.CacheEntry) (domain.SeriesID, error) {
	canon := series.Canon{
		TMDBID:         e.TMDBID,
		TVDBID:         e.TVDBID,
		IMDBID:         e.IMDBID,
		Title:          e.Title,
		Year:           e.Year,
		Status:         e.Status,
		RuntimeMinutes: e.RuntimeMinutes,
		LastAirDate:    e.LastAiredAt,
		Hydration:      series.HydrationStub,
		InProduction:   false,
	}
	if canon.Title == "" {
		// SeriesRepository.Upsert requires a non-empty title; orphan
		// rows that never had a title get a placeholder so the cache
		// row still resolves.
		canon.Title = e.TitleSlug
		if canon.Title == "" {
			canon.Title = fmt.Sprintf("sonarr:%s:%d", e.InstanceName, e.SonarrSeriesID)
		}
	}
	existing, err := r.series.FindByExternalIDs(ctx, e.TMDBID, e.TVDBID, e.IMDBID)
	if err == nil {
		canon.ID = existing.ID
		canon.CreatedAt = existing.CreatedAt
	} else if !errors.Is(err, ports.ErrNotFound) {
		return 0, fmt.Errorf("find canon: %w", err)
	}
	return r.series.Upsert(ctx, canon)
}

// SoftDelete stamps deleted_at = now. Idempotent — missing row OR
// already-deleted row both return nil. The 041f webhook fires
// SeriesDelete for IDs that may never have been cached; surfacing
// ErrNotFound would just spam logs without driving any action.
func (r *SeriesCacheRepository) SoftDelete(ctx context.Context, instanceName domain.InstanceName, sonarrSeriesID domain.SonarrSeriesID) error {
	now := time.Now().UTC()
	res := dbFromContext(ctx, r.db).WithContext(ctx).
		Model(&database.SeriesCacheModel{}).
		Where("instance_name = ? AND sonarr_series_id = ?", instanceName, sonarrSeriesID).
		Updates(map[string]any{
			"deleted_at": now,
			"updated_at": now,
		})
	if res.Error != nil {
		return fmt.Errorf("soft delete series_cache: %w", res.Error)
	}
	return nil
}

// ListBySeriesID returns every active cache row pointing at the
// given canon series.id. Used by the seriesdetail composer's
// recommendations branch to compute the per-recommendation
// in_library flag (PRD §5.6 recommendations bullet). Soft-deleted
// rows excluded — recommendations refer to current library state
// only.
func (r *SeriesCacheRepository) ListBySeriesID(ctx context.Context, seriesID domain.SeriesID) ([]series.CacheEntry, error) {
	var rows []cacheRow
	err := dbFromContext(ctx, r.db).WithContext(ctx).
		Table("series_cache").
		Select(seriesCacheSelect).
		Joins(seriesCacheJoin).
		Where("series_cache.series_id = ? AND series_cache.deleted_at IS NULL", seriesID).
		Find(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("list series_cache by series_id: %w", err)
	}
	out := make([]series.CacheEntry, 0, len(rows))
	for _, row := range rows {
		out = append(out, rowToCacheEntry(row))
	}
	return out, nil
}

func (r *SeriesCacheRepository) ListActiveByInstance(ctx context.Context, instanceName domain.InstanceName) ([]series.CacheEntry, error) {
	var rows []cacheRow
	err := dbFromContext(ctx, r.db).WithContext(ctx).
		Table("series_cache").
		Select(seriesCacheSelect).
		Joins(seriesCacheJoin).
		Where("series_cache.instance_name = ? AND series_cache.deleted_at IS NULL", instanceName).
		Find(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("list active series_cache: %w", err)
	}
	out := make([]series.CacheEntry, 0, len(rows))
	for _, row := range rows {
		out = append(out, rowToCacheEntry(row))
	}
	return out, nil
}

// ListByFilter implements the B3 list endpoint backing query. The
// `imported` state filter is an EXISTS subquery against grab_records.
// The `missing` state filter is missing_count > 0. The `all` state is
// the unnarrowed active set. Story 120: `filter.Search`, when set,
// adds a case-insensitive substring predicate over (s.title, title_slug).
// Keyset pagination over the chosen sort key. Post-cutover every
// predicate that referenced a canon column qualifies with s.* and the
// query runs over the JOIN.
func (r *SeriesCacheRepository) ListByFilter(
	ctx context.Context,
	instanceName domain.InstanceName,
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
	base := db.Table("series_cache").
		Joins(seriesCacheJoin).
		Where("series_cache.instance_name = ? AND series_cache.deleted_at IS NULL", instanceName)
	base = applyStateFilter(base, filter.State)
	base = applySearchFilter(base, filter.Search)
	base = applyMonitoredFilter(base, filter.MonitoredOnly)
	base = applyNetworksFilter(base, filter.Networks)

	var total int64
	if err := base.Session(&gorm.Session{}).Count(&total).Error; err != nil {
		return nil, 0, false, nil, fmt.Errorf("count series_cache: %w", err)
	}

	q := base.Session(&gorm.Session{}).Select(seriesCacheSelect)
	q = applyCursor(q, sort, page.Cursor)
	q = applyOrder(q, sort)

	var rows []cacheRow
	if err := q.Limit(page.Limit + 1).Find(&rows).Error; err != nil {
		return nil, 0, false, nil, fmt.Errorf("list series_cache: %w", err)
	}

	hasMore := false
	var next *ports.Cursor
	if len(rows) > page.Limit {
		hasMore = true
		last := rows[page.Limit-1]
		next = cursorFromRow(last, sort)
		rows = rows[:page.Limit]
	}

	out := make([]series.CacheEntry, 0, len(rows))
	for _, row := range rows {
		out = append(out, rowToCacheEntry(row))
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
		return q.Where("series_cache.missing_count > 0")
	default:
		return q
	}
}

// applySearchFilter adds a case-insensitive substring predicate over
// (s.title, title_slug) when q is non-empty. Story 120: uses
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
		"(LOWER(s.title) LIKE LOWER(?) ESCAPE '\\' "+
			"OR LOWER(series_cache.title_slug) LIKE LOWER(?) ESCAPE '\\')",
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
	return q.Where("series_cache.monitored = ?", *only)
}

// applyNetworksFilter narrows the query to rows whose canon network
// membership (via series_networks join) matches any of the supplied
// names. Empty slice ⇒ no-op. Story 121a /series facet panel needs
// server-side narrowing.
//
// Post-E-1: network membership lives in series_networks; the predicate
// is an EXISTS subquery against series_networks JOIN networks.
func applyNetworksFilter(q *gorm.DB, networks []string) *gorm.DB {
	if len(networks) == 0 {
		return q
	}
	return q.Where(
		"EXISTS (SELECT 1 FROM series_networks sn "+
			"JOIN networks n ON n.id = sn.network_id "+
			"WHERE sn.series_id = series_cache.series_id "+
			"AND n.name IN ?)",
		networks,
	)
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
// title_asc:       WHERE (LOWER(s.title), sonarr_series_id) > (title, id)
// air_date_desc:   nil-aware:
//
//	WHEN cursor row had non-NULL last_air_date:
//	  WHERE (last_air_date, sonarr_series_id) < (ts, id)
//	  OR last_air_date IS NULL
//	WHEN cursor row had NULL last_air_date:
//	  WHERE last_air_date IS NULL AND sonarr_series_id < id
//
// nil cursor = first page (no predicate).
func applyCursor(q *gorm.DB, sort ports.SeriesCacheSort, cur *ports.Cursor) *gorm.DB {
	if cur == nil {
		return q
	}
	switch sort {
	case ports.SeriesCacheSortTitleAsc:
		title, sid := splitTitleCursorID(cur.ID)
		return q.Where("(LOWER(s.title), series_cache.sonarr_series_id) > (?, ?)", title, sid)
	case ports.SeriesCacheSortAirDateDesc:
		sid, _ := strconv.Atoi(cur.ID)
		if cur.Timestamp.IsZero() {
			// Previous page ended on a NULL-air row: stay in the NULL
			// tail and walk down by id only.
			return q.Where("s.last_air_date IS NULL AND series_cache.sonarr_series_id < ?", sid)
		}
		return q.Where(
			"(s.last_air_date IS NOT NULL AND (s.last_air_date, series_cache.sonarr_series_id) < (?, ?)) OR s.last_air_date IS NULL",
			cur.Timestamp, sid,
		)
	default:
		sid, _ := strconv.Atoi(cur.ID)
		return q.Where("(series_cache.updated_at, series_cache.sonarr_series_id) < (?, ?)", cur.Timestamp, sid)
	}
}

// applyOrder is the ORDER BY half of the sort. For air_date_desc we
// emit `last_air_date IS NULL ASC` as the first key so non-NULL rows
// sort BEFORE NULL rows (NULLS LAST semantics) on both Postgres and
// SQLite — both engines treat `IS NULL` as a 0/1 expression and
// accept ASC ordering on it.
func applyOrder(q *gorm.DB, sort ports.SeriesCacheSort) *gorm.DB {
	switch sort {
	case ports.SeriesCacheSortTitleAsc:
		return q.Order("LOWER(s.title) ASC, series_cache.sonarr_series_id ASC")
	case ports.SeriesCacheSortAirDateDesc:
		return q.Order("s.last_air_date IS NULL ASC, s.last_air_date DESC, series_cache.sonarr_series_id DESC")
	default:
		return q.Order("series_cache.updated_at DESC, series_cache.sonarr_series_id DESC")
	}
}

// cursorFromRow produces the next-page cursor. For updated_desc:
// Timestamp=UpdatedAt + ID=decimal(series_id). For title_asc:
// Timestamp=zero + ID="lower(title)|series_id" packed string. For
// air_date_desc: Timestamp=LastAiredAt (zero when nil) + ID=decimal
// series_id; applyCursor reads the zero-Timestamp signal as "we are
// in the NULL tail".
func cursorFromRow(row cacheRow, sort ports.SeriesCacheSort) *ports.Cursor {
	switch sort {
	case ports.SeriesCacheSortTitleAsc:
		return &ports.Cursor{
			Timestamp: time.Time{},
			ID:        strings.ToLower(row.Title) + "|" + strconv.Itoa(int(row.SonarrSeriesID)),
		}
	case ports.SeriesCacheSortAirDateDesc:
		var ts time.Time
		if row.LastAiredAt != nil {
			ts = row.LastAiredAt.UTC()
		}
		return &ports.Cursor{
			Timestamp: ts,
			ID:        strconv.Itoa(int(row.SonarrSeriesID)),
		}
	default:
		return &ports.Cursor{
			Timestamp: row.UpdatedAt.UTC(),
			ID:        strconv.Itoa(int(row.SonarrSeriesID)),
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
	ctx context.Context, instanceName domain.InstanceName, seriesIDs []domain.SonarrSeriesID,
) (map[domain.SonarrSeriesID]ports.LastGrabInfo, error) {
	out := make(map[domain.SonarrSeriesID]ports.LastGrabInfo, len(seriesIDs))
	if len(seriesIDs) == 0 {
		return out, nil
	}
	type row struct {
		SeriesID     domain.SonarrSeriesID `gorm:"column:series_id"`
		MaxCreatedAt time.Time             `gorm:"column:max_created_at"`
		SeasonNumber int                   `gorm:"column:season_number"`
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
// network names for the instance's active rows. Post-E-1 networks
// live in series_networks; we JOIN cache → series → series_networks
// → networks and project networks.name.
func (r *SeriesCacheRepository) ListDistinctNetworks(
	ctx context.Context,
	instanceName domain.InstanceName,
) ([]string, error) {
	if instanceName == "" {
		return nil, fmt.Errorf("list distinct networks: instance_name must be non-empty")
	}
	db := dbFromContext(ctx, r.db).WithContext(ctx)
	var rows []string
	err := db.Table("series_cache").
		Joins(seriesCacheJoin).
		Joins("INNER JOIN series_networks sn ON sn.series_id = s.id").
		Joins("INNER JOIN networks n ON n.id = sn.network_id").
		Where("series_cache.instance_name = ? AND series_cache.deleted_at IS NULL", instanceName).
		Where("n.name IS NOT NULL AND n.name <> ''").
		Distinct("n.name").
		Order("n.name ASC").
		Limit(ports.MaxDistinctNetworks).
		Pluck("n.name", &rows).Error
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

// rowToCacheEntry projects the internal joined row onto the public
// series.CacheEntry shape. Genres / Overview / FanartPath / BannerPath
// stay nil — canon stores those differently (series_genres /
// series_texts / media_assets) and the lean wire DTO already drops
// them. Production reads never hit these fields.
func rowToCacheEntry(r cacheRow) series.CacheEntry {
	return series.CacheEntry{
		InstanceName:      r.InstanceName,
		SonarrSeriesID:    r.SonarrSeriesID,
		SeriesID:          r.SeriesID,
		Title:             r.Title,
		TitleSlug:         r.TitleSlug,
		Year:              r.Year,
		TVDBID:            r.TVDBID,
		IMDBID:            r.IMDBID,
		TMDBID:            r.TMDBID,
		Status:            r.Status,
		Genres:            nil,
		RuntimeMinutes:    r.RuntimeMinutes,
		Monitored:         r.Monitored,
		Overview:          nil,
		PosterAsset:       r.PosterAsset,
		FanartPath:        nil,
		BannerPath:        nil,
		MissingCount:      r.MissingCount,
		EpisodeFileCount:  r.EpisodeFileCount,
		SizeOnDiskBytes:   r.SizeOnDiskBytes,
		AiredEpisodeCount: r.AiredEpisodeCount,
		LastAiredAt:       r.LastAiredAt,
		UpdatedAt:         r.UpdatedAt,
		DeletedAt:         r.DeletedAt,
	}
}

var _ ports.SeriesCacheRepository = (*SeriesCacheRepository)(nil)
