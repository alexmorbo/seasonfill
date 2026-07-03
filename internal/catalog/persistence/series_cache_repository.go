package persistence

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/alexmorbo/seasonfill/internal/catalog/domain/series"
	enrichpersistence "github.com/alexmorbo/seasonfill/internal/enrichment/persistence"
	ports "github.com/alexmorbo/seasonfill/internal/shared/dataports"
	database "github.com/alexmorbo/seasonfill/internal/shared/db"
	"github.com/alexmorbo/seasonfill/internal/shared/dbtx"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
	sharedErrors "github.com/alexmorbo/seasonfill/internal/shared/errors"
	"github.com/alexmorbo/seasonfill/internal/shared/locale"
)

// SeriesCacheRepository persists the thin per-instance Sonarr projection
// (PRD v4 §5.11) post the 000032 cutover. The canon attributes
// (title / year / external ids / status / network / runtime / last_air)
// live on `series` and are JOIN-read via `series_cache.series_id`.
// Upsert resolves-or-creates the canon row through SeriesRepository
// before writing the thin row. Soft-deleted via deleted_at to preserve
// grab_records FK references.
type SeriesCacheRepository struct {
	db          *gorm.DB
	series      *enrichpersistence.SeriesRepository
	seriesTexts baseLangTextsWriter
}

// baseLangTextsWriter seeds series_texts{en-US} only-if-absent so a
// Sonarr title survives on the scan path for never-enriched / tmdb-less
// series. Satisfied by *enrichpersistence.SeriesTextsRepository.
type baseLangTextsWriter interface {
	InsertBaseLangIfAbsent(ctx context.Context, t series.SeriesText) error
}

func NewSeriesCacheRepository(db *gorm.DB, series *enrichpersistence.SeriesRepository) *SeriesCacheRepository {
	return &SeriesCacheRepository{db: db, series: series}
}

// WithSeriesTexts wires the base-lang seeder so Upsert can guarantee an
// en-US series_texts row for every cache writer. Nil-OK: Upsert skips the
// seed when unset.
func (r *SeriesCacheRepository) WithSeriesTexts(w baseLangTextsWriter) *SeriesCacheRepository {
	r.seriesTexts = w
	return r
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
// seriesCacheSelectCore projects every column EXCEPT the display title.
// The title is appended per read path: canon s.title for the point reads
// (seriesCacheSelect, no localization), or the series_texts-resolved
// expression for the catalog list (ListByFilter, S-E2). Column ordering
// is irrelevant — cacheRow is scanned by alias.
const seriesCacheSelectCore = `
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
		s.year                          AS s_year,
		s.tvdb_id                       AS s_tvdb_id,
		s.imdb_id                       AS s_imdb_id,
		s.tmdb_id                       AS s_tmdb_id,
		s.status                        AS s_status,
		s.runtime_minutes               AS s_runtime_minutes,
		s.last_air_date                 AS s_last_air_date
	`

// seriesCacheSelect is the point-read projection (Get / ListBySeriesID /
// ListBySeriesIDs / ListActiveByInstance). S-E3a — canon series.title /
// series.poster_asset were dropped from the domain (columns now dead), so
// the point read resolves the display title from series_texts and the poster
// raw path from series_media_texts, both at the en-US base tier (point reads
// carry no request language; S-E1 guarantees an en-US series_texts row). The
// catalog list path (ListByFilter) uses the requested-lang resolvers instead.
var seriesCacheSelect = seriesCacheSelectCore + ", " +
	resolvedTitleExpr("en-US") + " AS s_title, " +
	resolvedPosterExpr("en-US") + " AS s_poster_asset"

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
	err := dbtx.DBFromContext(ctx, r.db).WithContext(ctx).
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
	res := dbtx.DBFromContext(ctx, r.db).WithContext(ctx).Clauses(clause.OnConflict{
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

	// S-E1 base-lang guarantee on EVERY cache writer (scan, webhook,
	// watchdog, seriesdetail): seed series_texts{en-US} from the Sonarr
	// title ONLY IF ABSENT so tmdb-less / never-enriched series carry a
	// display title. Best-effort — a text-write hiccup must NOT regress
	// the Upsert (the webhook caller aborts episode-landing on Upsert
	// error). Re-scan re-Upserts every series, so this doubles as the
	// idempotent catch-up pass.
	if r.seriesTexts != nil && entry.Title != "" {
		title := entry.Title
		st := series.SeriesText{
			SeriesID:  canonID,
			Language:  locale.Default(), // "en-US"
			Title:     &title,
			UpdatedAt: now,
		}
		if terr := r.seriesTexts.InsertBaseLangIfAbsent(ctx, st); terr != nil {
			slog.WarnContext(ctx, "series_cache_base_lang_seed_failed",
				slog.Int64("sonarr_series_id", int64(entry.SonarrSeriesID)),
				slog.String("error", terr.Error()))
		}
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
		Year:           e.Year,
		Status:         e.Status,
		RuntimeMinutes: e.RuntimeMinutes,
		LastAirDate:    e.LastAiredAt,
		Hydration:      series.HydrationStub,
		InProduction:   false,
	}
	// S-E3a — canon.title no longer exists on the domain aggregate. The
	// Sonarr title flows to series_texts{en-US} via InsertBaseLangIfAbsent
	// on the sync path; the (dead) series.title column is left empty.
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
	res := dbtx.DBFromContext(ctx, r.db).WithContext(ctx).
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
	err := dbtx.DBFromContext(ctx, r.db).WithContext(ctx).
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

// ListBySeriesIDs is the batch sibling of ListBySeriesID — returns
// the active cache rows (deleted_at IS NULL) for every series.id in
// one query, bucketed into a map[series_id] → []CacheEntry. Empty
// input returns an empty (non-nil) map. Missing ids map to a nil
// slice in the result so callers can probe O(1) and avoid a
// per-id presence check.
//
// Story 556 (E-1 Z7) — replaces the per-credit
// SeriesCacheLookup.ListBySeriesID loop in
// CastComposer.probeInLibrary (cast.go:344). One round-trip per
// /cast request regardless of M; the `series_id IN (?)` predicate
// rides the `series_cache_series_id` index on both Postgres and
// sqlite. Soft-deleted rows excluded for the same reason
// ListBySeriesID excludes them — in_library reflects current
// library state only.
func (r *SeriesCacheRepository) ListBySeriesIDs(ctx context.Context, seriesIDs []domain.SeriesID) (map[domain.SeriesID][]series.CacheEntry, error) {
	out := make(map[domain.SeriesID][]series.CacheEntry, len(seriesIDs))
	if len(seriesIDs) == 0 {
		return out, nil
	}
	bound := make([]int64, 0, len(seriesIDs))
	for _, id := range seriesIDs {
		if id <= 0 {
			continue
		}
		bound = append(bound, int64(id))
	}
	if len(bound) == 0 {
		return out, nil
	}
	var rows []cacheRow
	err := dbtx.DBFromContext(ctx, r.db).WithContext(ctx).
		Table("series_cache").
		Select(seriesCacheSelect).
		Joins(seriesCacheJoin).
		Where("series_cache.series_id IN ? AND series_cache.deleted_at IS NULL", bound).
		Find(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("list series_cache by series_ids: %w", err)
	}
	for _, row := range rows {
		entry := rowToCacheEntry(row)
		if entry.SeriesID == nil {
			continue
		}
		sid := *entry.SeriesID
		out[sid] = append(out[sid], entry)
	}
	return out, nil
}

// GetInstancesBySeriesID returns the sorted, distinct instance names
// that currently carry this canonical series.id (deleted_at IS NULL).
// Empty result when no active cache row points at the series.
//
// Portable across SQLite + Postgres: DISTINCT + ORDER BY, no array_agg.
// Sorting at the SQL edge means callers get a deterministic preferred-
// instance pick without re-sorting application-side.
//
// Used by GlobalComposerUseCase + GlobalSeriesHandler.Regrab to resolve
// the preferred instance for a canonical series.id (story 491 / N-1a).
// The wider ListBySeriesID method above is the richer projection — this
// method exists for callers that only need the name list.
func (r *SeriesCacheRepository) GetInstancesBySeriesID(ctx context.Context, seriesID domain.SeriesID) ([]domain.InstanceName, error) {
	if seriesID <= 0 {
		return nil, fmt.Errorf("get instances by series_id: invalid id %d", seriesID)
	}
	var rows []domain.InstanceName
	err := dbtx.DBFromContext(ctx, r.db).WithContext(ctx).
		Table("series_cache").
		Where("series_id = ? AND deleted_at IS NULL", seriesID).
		Distinct("instance_name").
		Order("instance_name ASC").
		Pluck("instance_name", &rows).Error
	if err != nil {
		return nil, fmt.Errorf("get instances by series_id: %w", err)
	}
	if rows == nil {
		rows = []domain.InstanceName{}
	}
	return rows, nil
}

// GetInstancesBySeriesIDs is the batch sibling of GetInstancesBySeriesID,
// returning the sorted distinct active instance names for every id in
// one query. Soft-deleted rows excluded.
//
// Empty input slice short-circuits before any SQL and returns an empty
// (non-nil) map. Invalid ids (≤0) are skipped at the SQL level — they
// can never have a series_cache row. Caller-side validation can rely on
// "missing key in returned map" === "no active library instance".
//
// Used by the discovery handler page-projection loop to populate the
// DiscoverySeriesItem.InLibraryInstances slice in one round-trip per
// response, regardless of page size (avoids N+1 across 20-100 items).
//
// Portable across SQLite + Postgres: WHERE IN + ORDER BY without
// array_agg. The (series_id, instance_name) ordering combined with the
// stream-merge below means callers see deterministic instance ordering
// per id.
func (r *SeriesCacheRepository) GetInstancesBySeriesIDs(
	ctx context.Context,
	seriesIDs []domain.SeriesID,
) (map[domain.SeriesID][]domain.InstanceName, error) {
	out := make(map[domain.SeriesID][]domain.InstanceName, len(seriesIDs))
	if len(seriesIDs) == 0 {
		return out, nil
	}
	// Filter out invalid ids before issuing SQL — the single-id sibling
	// returns an error on ≤0, but for the batch path silently dropping
	// invalid ids is the right shape: callers feeding mixed slices
	// shouldn't lose the valid lookups to one malformed entry.
	clean := make([]domain.SeriesID, 0, len(seriesIDs))
	for _, id := range seriesIDs {
		if id > 0 {
			clean = append(clean, id)
		}
	}
	if len(clean) == 0 {
		return out, nil
	}
	type row struct {
		SeriesID     domain.SeriesID     `gorm:"column:series_id"`
		InstanceName domain.InstanceName `gorm:"column:instance_name"`
	}
	var rows []row
	err := dbtx.DBFromContext(ctx, r.db).WithContext(ctx).
		Table("series_cache").
		Select("DISTINCT series_id, instance_name").
		Where("series_id IN ? AND deleted_at IS NULL", clean).
		Order("series_id ASC, instance_name ASC").
		Scan(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("get instances by series_ids: %w", err)
	}
	for _, r := range rows {
		out[r.SeriesID] = append(out[r.SeriesID], r.InstanceName)
	}
	return out, nil
}

func (r *SeriesCacheRepository) ListActiveByInstance(ctx context.Context, instanceName domain.InstanceName) ([]series.CacheEntry, error) {
	var rows []cacheRow
	err := dbtx.DBFromContext(ctx, r.db).WithContext(ctx).
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

	// S-E2: resolve the display title / title_asc sort key from
	// series_texts (requested lang → en-US). lang is clamped to the
	// supported whitelist so it is safe to inline into the correlated
	// subquery (see resolvedTitleExpr).
	lang := normalizeSupportedLang(filter.Lang)
	titleExpr := resolvedTitleExpr(lang)
	posterExpr := resolvedPosterExpr(lang)

	db := dbtx.DBFromContext(ctx, r.db).WithContext(ctx)
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

	q := base.Session(&gorm.Session{}).
		Select(seriesCacheSelectCore + ", " + titleExpr + " AS s_title, " + posterExpr + " AS s_poster_asset")
	q = applyCursor(q, sort, page.Cursor, titleExpr)
	q = applyOrder(q, sort, titleExpr)

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

// applySearchFilter adds a case-insensitive substring predicate. S-E2 /
// 569: the title match runs across ALL languages via series_texts (so a
// Russian query finds the row whose display/en-US title is English),
// plus the Sonarr title_slug. Canon s.title is no longer searched.
// LOWER(...) LIKE LOWER(?) keeps sqlite (tests) and Postgres (prod) on
// one expression; Postgres folds Cyrillic case, sqlite folds ASCII only
// (unchanged from the prior canon-title search). Wildcards escaped;
// empty trimmed q ⇒ no-op.
func applySearchFilter(q *gorm.DB, search string) *gorm.DB {
	trimmed := strings.TrimSpace(search)
	if trimmed == "" {
		return q
	}
	pat := "%" + escapeLikePattern(trimmed) + "%"
	return q.Where(
		"(EXISTS (SELECT 1 FROM series_texts st "+
			"WHERE st.series_id = s.id "+
			"AND LOWER(st.title) LIKE LOWER(?) ESCAPE '\\') "+
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

// normalizeSupportedLang clamps a caller-supplied tag to the closed
// supported set (locale.SupportedUserLanguages). Anything blank or
// unrecognised collapses to the base language (en-US). The return value
// is ALWAYS one of the whitelist constants — which is what makes it safe
// to inline into SQL (resolvedTitleExpr) without a bind parameter.
func normalizeSupportedLang(lang string) string {
	lang = strings.TrimSpace(lang)
	for _, s := range locale.SupportedUserLanguages {
		if lang == s {
			return lang
		}
	}
	return locale.Default()
}

// resolvedTitleExpr builds a correlated scalar subquery that resolves a
// series' display title from series_texts using the §5.6 language
// fallback: requested lang (CASE=2) → en-US (CASE=1) → first row by
// language ASC. S-E2: this replaces the canon series.title read on the
// catalog list (projection + title_asc sort + keyset cursor). Canon is
// no longer a fallback tier (dark-launch Variant A; S-E1 guarantees an
// en-US row per series).
//
// `lang` MUST come from normalizeSupportedLang — it is inlined as a
// literal, NOT bound, because (1) GORM silently drops clause.Expr Vars
// inside .Order(), and (2) the value is provably one of {en-US, ru-RU},
// so there is no injection surface. `s` is the series alias from
// seriesCacheJoin.
func resolvedTitleExpr(lang string) string {
	return "(SELECT st.title FROM series_texts st WHERE st.series_id = s.id " +
		"ORDER BY CASE WHEN st.language = '" + lang + "' THEN 2 " +
		"WHEN st.language = 'en-US' THEN 1 ELSE 0 END DESC, st.language ASC LIMIT 1)"
}

// resolvedPosterExpr mirrors resolvedTitleExpr for the per-language poster raw
// path, sourced from series_media_texts (§5.6 fallback: requested lang → en-US
// → first row by language ASC). S-E3a — this replaces the canon
// series.poster_asset read on the catalog list + point reads (canon art was
// removed from the domain; the column is dead pending the S-E3b drop). `lang`
// MUST be a normalizeSupportedLang whitelist value — inlined as a literal for
// the same reasons as resolvedTitleExpr (no injection surface, GORM drops
// clause.Expr Vars inside .Order()).
func resolvedPosterExpr(lang string) string {
	return "(SELECT smt.poster_asset FROM series_media_texts smt WHERE smt.series_id = s.id " +
		"ORDER BY CASE WHEN smt.language = '" + lang + "' THEN 2 " +
		"WHEN smt.language = 'en-US' THEN 1 ELSE 0 END DESC, smt.language ASC LIMIT 1)"
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
func applyCursor(q *gorm.DB, sort ports.SeriesCacheSort, cur *ports.Cursor, titleExpr string) *gorm.DB {
	if cur == nil {
		return q
	}
	switch sort {
	case ports.SeriesCacheSortTitleAsc:
		title, sid := splitTitleCursorID(cur.ID)
		// S-E2: keyset over the series_texts-resolved title (lang inlined
		// in titleExpr), matching applyOrder + cursorFromRow.
		return q.Where("(LOWER("+titleExpr+"), series_cache.sonarr_series_id) > (?, ?)", title, sid)
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
func applyOrder(q *gorm.DB, sort ports.SeriesCacheSort, titleExpr string) *gorm.DB {
	switch sort {
	case ports.SeriesCacheSortTitleAsc:
		// S-E2: order by the series_texts-resolved title (lang inlined in
		// titleExpr). GORM drops clause.Expr Vars in Order, hence the
		// literal-inlined, bind-free expression.
		return q.Order("LOWER(" + titleExpr + ") ASC, series_cache.sonarr_series_id ASC")
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
	sub := dbtx.DBFromContext(ctx, r.db).WithContext(ctx).
		Table("grab_records").
		Select("series_id, MAX(created_at) AS max_created_at").
		Where("instance_name = ? AND series_id IN ? AND status = ?",
			instanceName, seriesIDs, "imported").
		Group("series_id")
	err := dbtx.DBFromContext(ctx, r.db).WithContext(ctx).
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
	db := dbtx.DBFromContext(ctx, r.db).WithContext(ctx)
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
