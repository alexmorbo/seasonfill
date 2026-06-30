package persistence

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"gorm.io/datatypes"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/alexmorbo/seasonfill/internal/catalog/domain/series"
	enrichmentpkg "github.com/alexmorbo/seasonfill/internal/enrichment/domain/enrichment"
	ports "github.com/alexmorbo/seasonfill/internal/shared/dataports"
	database "github.com/alexmorbo/seasonfill/internal/shared/db"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
	sharedErrors "github.com/alexmorbo/seasonfill/internal/shared/errors"
)

// SeriesRepository persists the canonical `series` table (PRD §5).
// Upsert is idempotent: subsequent calls with the same payload re-emit
// the row's columns identically and bump only updated_at. Natural key
// resolution lives on the side helpers (GetByTMDBID / FindByExternalIDs)
// rather than baked into Upsert because the merge boundary (§5.4) wants
// to decide which id to write before it commits to a row.
type SeriesRepository struct {
	db *gorm.DB
}

func NewSeriesRepository(db *gorm.DB) *SeriesRepository {
	return &SeriesRepository{db: db}
}

// Get fetches by primary key. Returns ports.ErrNotFound on miss.
func (r *SeriesRepository) Get(ctx context.Context, id domain.SeriesID) (series.Canon, error) {
	var m database.SeriesModel
	err := dbFromContext(ctx, r.db).WithContext(ctx).
		Where("id = ?", id).First(&m).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return series.Canon{}, errors.Join(
				&sharedErrors.SeriesNotFoundError{ID: id},
				ports.ErrNotFound,
			)
		}
		return series.Canon{}, fmt.Errorf("get series: %w", err)
	}
	return toCanon(m), nil
}

// GetByTMDBID looks up the canon row by TMDB id. The partial unique
// index (`series_tmdb_id WHERE tmdb_id IS NOT NULL`) guarantees at
// most one row. Returns ports.ErrNotFound on miss.
func (r *SeriesRepository) GetByTMDBID(ctx context.Context, tmdbID domain.TMDBID) (series.Canon, error) {
	var m database.SeriesModel
	err := dbFromContext(ctx, r.db).WithContext(ctx).
		Where("tmdb_id = ?", tmdbID).First(&m).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return series.Canon{}, errors.Join(
				&sharedErrors.SeriesNotFoundError{},
				ports.ErrNotFound,
			)
		}
		return series.Canon{}, fmt.Errorf("get series by tmdb_id: %w", err)
	}
	return toCanon(m), nil
}

// ListByIDs returns canon rows for the supplied ids in id-ascending
// order. Missing ids are silently dropped (callers needing a presence
// check go through Get / GetByTMDBID per id). Empty input returns
// (nil, nil) — callers MUST tolerate a nil slice (matches People.ListByIDs).
//
// Story 551 (E-1 Z2) — replaces the per-rec Series.Get loop in the
// seriesdetail composer (composer.go loadRecommendations +
// recommendations.go GetRecommendations). One round-trip per call site
// regardless of M; the `id IN (?)` predicate rides the PK index on
// both Postgres and sqlite, so the read is sub-millisecond for the
// M=10-20 typical recommendations batch.
//
// Read shape is byte-equal to Get(): every row goes through the same
// toCanon projector (origin_countries JSON, hydration enum, the
// enrichment_*_synced_at columns). The Recommendation use-case wires
// the slice into a map[SeriesID]series.Canon locally; this method
// stays neutral and returns the wire-stable slice form to mirror
// PeopleRepository.ListByIDs / NetworksRepository.ListByIDs.
func (r *SeriesRepository) ListByIDs(ctx context.Context, ids []domain.SeriesID) ([]series.Canon, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	// Cast to int64 because GORM's `IN ?` expander walks `any` slices
	// element-wise; using the typed primitive directly works on
	// Postgres but trips sqlite's bind-conversion in older driver
	// builds. Mirrors the same conversion in Story 550's
	// ListByEpisodeIDsWithFallback.
	bound := make([]int64, len(ids))
	for i, id := range ids {
		bound[i] = int64(id)
	}
	var models []database.SeriesModel
	err := dbFromContext(ctx, r.db).WithContext(ctx).
		Where("id IN ?", bound).
		Order("id ASC").
		Find(&models).Error
	if err != nil {
		return nil, fmt.Errorf("list series by ids: %w", err)
	}
	out := make([]series.Canon, 0, len(models))
	for _, m := range models {
		out = append(out, toCanon(m))
	}
	return out, nil
}

// ListByTMDBIDs returns canon rows for the supplied TMDB ids in
// tmdb_id-ascending order. Missing ids are silently dropped (callers
// needing a presence check go through GetByTMDBID per id). Zero ids
// are filtered out at the input boundary — the partial-unique index
// `series_tmdb_id WHERE tmdb_id IS NOT NULL` would not catch them
// anyway. Empty effective input returns (nil, nil) — callers MUST
// tolerate a nil slice (matches the ListByIDs convention).
//
// Story 556 (E-1 Z7) — replaces the per-credit Series.GetByTMDBID loop
// in CastComposer.probeInLibrary (cast.go:319-353). One round-trip per
// /cast request regardless of M (typical M=200-500 for rich shows);
// the `tmdb_id IN (?)` predicate rides the partial-unique
// `series_tmdb_id` index on both Postgres and sqlite, so the read is
// sub-millisecond for the M ≤ 1000 batch.
//
// Read shape is byte-equal to GetByTMDBID(): every row goes through
// the same toCanon projector. The CastComposer builds a local
// map[TMDBID]SeriesID from the slice; this method stays neutral and
// returns the wire-stable slice form to mirror ListByIDs.
func (r *SeriesRepository) ListByTMDBIDs(ctx context.Context, tmdbIDs []domain.TMDBID) ([]series.Canon, error) {
	if len(tmdbIDs) == 0 {
		return nil, nil
	}
	bound := make([]int64, 0, len(tmdbIDs))
	for _, id := range tmdbIDs {
		if id == 0 {
			continue
		}
		bound = append(bound, int64(id))
	}
	if len(bound) == 0 {
		return nil, nil
	}
	var models []database.SeriesModel
	err := dbFromContext(ctx, r.db).WithContext(ctx).
		Where("tmdb_id IN ?", bound).
		Order("tmdb_id ASC").
		Find(&models).Error
	if err != nil {
		return nil, fmt.Errorf("list series by tmdb_ids: %w", err)
	}
	out := make([]series.Canon, 0, len(models))
	for _, m := range models {
		out = append(out, toCanon(m))
	}
	return out, nil
}

// FindByExternalIDs resolves a canon row by trying TMDB id first,
// then TVDB id, then IMDB id, in that order — same priority the
// Sonarr sync worker uses to attach `series_cache.series_id` (§5.4).
// Any of the *int / *string pointers may be nil; nil pointers skip
// that probe. Returns ports.ErrNotFound when every probe misses.
func (r *SeriesRepository) FindByExternalIDs(
	ctx context.Context,
	tmdbID *domain.TMDBID,
	tvdbID *domain.TVDBID,
	imdbID *domain.IMDBID,
) (series.Canon, error) {
	db := dbFromContext(ctx, r.db).WithContext(ctx)
	probe := func(where string, args ...any) (series.Canon, bool, error) {
		var m database.SeriesModel
		err := db.Where(where, args...).First(&m).Error
		if err == nil {
			return toCanon(m), true, nil
		}
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return series.Canon{}, false, nil
		}
		return series.Canon{}, false, fmt.Errorf("find series: %w", err)
	}
	if tmdbID != nil {
		c, ok, err := probe("tmdb_id = ?", *tmdbID)
		if err != nil || ok {
			return c, err
		}
	}
	if tvdbID != nil {
		c, ok, err := probe("tvdb_id = ?", *tvdbID)
		if err != nil || ok {
			return c, err
		}
	}
	if imdbID != nil && *imdbID != "" {
		c, ok, err := probe("imdb_id = ?", *imdbID)
		if err != nil || ok {
			return c, err
		}
	}
	return series.Canon{}, errors.Join(
		&sharedErrors.SeriesNotFoundError{},
		ports.ErrNotFound,
	)
}

// Upsert inserts or updates the canon row. The PK column (id) is
// the conflict target only when the caller supplies a non-zero ID;
// otherwise the natural key (tmdb_id) is the conflict target — the
// merge-policy boundary picks which one. Pass id == 0 to "insert by
// natural key, or update existing"; pass id != 0 to "I know the row,
// update it". Returns the assigned id (relevant on the insert path).
//
// Idempotency contract: a no-op upsert (same canonical payload) leaves
// every column byte-equal except updated_at, which bumps to the new
// `now`.
func (r *SeriesRepository) Upsert(ctx context.Context, c series.Canon) (domain.SeriesID, error) {
	if c.Title == "" {
		return 0, fmt.Errorf("upsert series: title must be non-empty")
	}
	now := time.Now().UTC()
	if c.CreatedAt.IsZero() {
		c.CreatedAt = now
	}
	c.UpdatedAt = now
	if c.Hydration == "" {
		c.Hydration = series.HydrationStub
	}
	if !c.Hydration.IsValid() {
		return 0, fmt.Errorf("upsert series: invalid hydration %q", c.Hydration)
	}
	m := fromCanon(c)

	db := dbFromContext(ctx, r.db).WithContext(ctx)
	// No PK + no natural key (TMDBID) ⇒ pure INSERT, no ON CONFLICT
	// clause. Previously this branch emitted `clause.OnConflict{
	// DoNothing: false}` which serialized to a bare `ON CONFLICT DO
	// UPDATE` — SQLite tolerates the empty target; Postgres rejects it
	// with SQLSTATE 42601 ("requires inference specification or
	// constraint name"). The dual-backend pilot caught this via
	// TestSeriesCacheRepository_NilPointerFieldsRoundTrip / postgres.
	switch {
	case m.ID != 0:
		conflict := clause.OnConflict{
			Columns:   []clause.Column{{Name: "id"}},
			DoUpdates: clause.Assignments(seriesUpsertAssignments()),
		}
		if err := db.Clauses(conflict).Create(&m).Error; err != nil {
			return 0, fmt.Errorf("upsert series: %w", err)
		}
	case m.TMDBID != nil:
		// Partial unique index on tmdb_id WHERE tmdb_id IS NOT NULL —
		// SQLite + Postgres both require the index predicate to be
		// repeated in the ON CONFLICT target so the planner picks the
		// partial index rather than rejecting "no matching constraint".
		conflict := clause.OnConflict{
			Columns:     []clause.Column{{Name: "tmdb_id"}},
			TargetWhere: clause.Where{Exprs: []clause.Expression{clause.Expr{SQL: "tmdb_id IS NOT NULL"}}},
			DoUpdates:   clause.Assignments(seriesUpsertAssignments()),
		}
		if err := db.Clauses(conflict).Create(&m).Error; err != nil {
			return 0, fmt.Errorf("upsert series: %w", err)
		}
	default:
		// No PK and no natural key — pure insert. GORM will assign id.
		if err := db.Create(&m).Error; err != nil {
			return 0, fmt.Errorf("upsert series: %w", err)
		}
	}
	return m.ID, nil
}

// UpsertStub is the recommendation-stub variant of Upsert. Story 319:
// recommendation stubs built by MapTVToRecommendations carry NIL
// PosterAsset (often) and ALWAYS-NIL BackdropAsset, plus hydration='stub'.
// The legacy Upsert path overwrites every column in DO UPDATE SET, so a
// stub upsert against an existing 'full' canon row blanks out the
// images and downgrades hydration — frontend then renders monograms
// forever until the next TMDB sync of that series.
//
// UpsertStub uses a hand-written ON CONFLICT DO UPDATE that COALESCEs
// the columns a stub has no authority over (PRD §5.4: PosterAsset,
// BackdropAsset, Status, FirstAirDate, LastAirDate, Homepage,
// OriginalLanguage, OriginCountry, Popularity, InProduction,
// RuntimeMinutes, TMDBRating, TMDBVotes, IMDBID, IMDBRating, IMDBVotes,
// OMDBRated, OMDBAwards) against the existing row — the stub value is
// applied ONLY when the existing value is NULL. Hydration is preserved
// when the existing row is 'full' (a stub MUST NOT downgrade a full
// row). Title, Year, OriginalTitle, TVDBID, NextAirDate, UpdatedAt are
// still refreshable from the stub.
//
// The id-conflict branch is intentionally absent: callers reach this
// method only via the tmdb_id natural-key path (recommendation stubs
// always carry a tmdb_id from the TMDB recommendation summary). An
// id-known caller should keep using Upsert (it has the authoritative
// row in hand).
func (r *SeriesRepository) UpsertStub(ctx context.Context, c series.Canon) (domain.SeriesID, error) {
	if c.Title == "" {
		return 0, fmt.Errorf("upsert stub series: title must be non-empty")
	}
	if c.TMDBID == nil {
		return 0, fmt.Errorf("upsert stub series: tmdb_id required")
	}
	now := time.Now().UTC()
	if c.CreatedAt.IsZero() {
		c.CreatedAt = now
	}
	c.UpdatedAt = now
	if c.Hydration == "" {
		c.Hydration = series.HydrationStub
	}
	if !c.Hydration.IsValid() {
		return 0, fmt.Errorf("upsert stub series: invalid hydration %q", c.Hydration)
	}
	m := fromCanon(c)

	db := dbFromContext(ctx, r.db).WithContext(ctx)
	conflict := clause.OnConflict{
		Columns:     []clause.Column{{Name: "tmdb_id"}},
		TargetWhere: clause.Where{Exprs: []clause.Expression{clause.Expr{SQL: "tmdb_id IS NOT NULL"}}},
		// Hand-rolled assignments: COALESCE preserves the existing
		// value when the stub's value is NULL. Hydration uses CASE so
		// a stub never downgrades 'full' → 'stub'. The `series.` table
		// prefix references the existing row in both Postgres and
		// SQLite ON CONFLICT semantics; `excluded.` is the proposed
		// row in both dialects, so no branching is needed.
		DoUpdates: clause.Assignments(map[string]any{
			"tvdb_id":           gorm.Expr("COALESCE(series.tvdb_id, excluded.tvdb_id)"),
			"imdb_id":           gorm.Expr("COALESCE(series.imdb_id, excluded.imdb_id)"),
			"hydration":         gorm.Expr("CASE WHEN series.hydration = 'full' THEN series.hydration ELSE excluded.hydration END"),
			"title":             gorm.Expr("excluded.title"),
			"original_title":    gorm.Expr("COALESCE(series.original_title, excluded.original_title)"),
			"status":            gorm.Expr("COALESCE(series.status, excluded.status)"),
			"first_air_date":    gorm.Expr("COALESCE(series.first_air_date, excluded.first_air_date)"),
			"last_air_date":     gorm.Expr("COALESCE(series.last_air_date, excluded.last_air_date)"),
			"next_air_date":     gorm.Expr("COALESCE(series.next_air_date, excluded.next_air_date)"),
			"year":              gorm.Expr("COALESCE(series.year, excluded.year)"),
			"runtime_minutes":   gorm.Expr("COALESCE(series.runtime_minutes, excluded.runtime_minutes)"),
			"homepage":          gorm.Expr("COALESCE(series.homepage, excluded.homepage)"),
			"original_language": gorm.Expr("COALESCE(series.original_language, excluded.original_language)"),
			"origin_country":    gorm.Expr("COALESCE(series.origin_country, excluded.origin_country)"),
			"origin_countries":  gorm.Expr("COALESCE(series.origin_countries, excluded.origin_countries)"),
			"popularity":        gorm.Expr("COALESCE(series.popularity, excluded.popularity)"),
			"in_production":     gorm.Expr("series.in_production"),
			"poster_asset":      gorm.Expr("COALESCE(series.poster_asset, excluded.poster_asset)"),
			"backdrop_asset":    gorm.Expr("COALESCE(series.backdrop_asset, excluded.backdrop_asset)"),
			"tmdb_rating":       gorm.Expr("COALESCE(series.tmdb_rating, excluded.tmdb_rating)"),
			"tmdb_votes":        gorm.Expr("COALESCE(series.tmdb_votes, excluded.tmdb_votes)"),
			"imdb_rating":       gorm.Expr("series.imdb_rating"),
			"imdb_votes":        gorm.Expr("series.imdb_votes"),
			"omdb_rated":        gorm.Expr("series.omdb_rated"),
			"omdb_awards":       gorm.Expr("series.omdb_awards"),
			"updated_at":        gorm.Expr("excluded.updated_at"),
		}),
	}
	if err := db.Clauses(conflict).Create(&m).Error; err != nil {
		return 0, fmt.Errorf("upsert stub series: %w", err)
	}
	return m.ID, nil
}

// ListCanonImagesCorrupted returns series.id rows where the canon row
// finished a full enrichment pass (tmdb_id IS NOT NULL AND
// hydration = 'full') but EITHER poster_asset OR backdrop_asset is
// NULL. Story 319: rows corrupted by the pre-fix recommendation-stub
// upsert path. Caller (cmd/server/enrichment_wiring.go boot one-shot)
// enqueues each id to the enrichment dispatcher at PriorityCold; the
// TMDB sync repopulates the missing paths via MergeSeries.
//
// Limit caps the result-set size at 5000 to mirror the cold-start
// re-sweep budget. Selectivity is good on a typical library (300
// series, ~30 corrupted on a fresh library that's been through one
// recommendations pass) — the WHERE NULL filter rides the planner's
// row scan; no index added.
func (r *SeriesRepository) ListCanonImagesCorrupted(ctx context.Context, limit int) ([]domain.SeriesID, error) {
	if limit <= 0 {
		limit = 1000
	}
	var ids []domain.SeriesID
	err := dbFromContext(ctx, r.db).WithContext(ctx).
		Table("series").
		Select("id").
		Where("tmdb_id IS NOT NULL").
		Where("hydration = 'full'").
		Where("(poster_asset IS NULL OR backdrop_asset IS NULL)").
		Limit(limit).
		Pluck("id", &ids).Error
	if err != nil {
		return nil, fmt.Errorf("list canon images corrupted: %w", err)
	}
	return ids, nil
}

// CountCanonImagesBreakdown returns (poster_null_count, backdrop_null_count)
// across the same population ListCanonImagesCorrupted draws from
// (tmdb_id IS NOT NULL AND hydration = 'full'). Story 346 telemetry so
// operators can grep the cold-start log for converging counts across
// successive deploys. Two indexed scans; no full table walk — the
// hydration='full' filter narrows the scan to the library-active set.
func (r *SeriesRepository) CountCanonImagesBreakdown(ctx context.Context) (int, int, error) {
	base := dbFromContext(ctx, r.db).WithContext(ctx).Table("series").
		Where("tmdb_id IS NOT NULL").
		Where("hydration = 'full'")
	var posterNull, backdropNull int64
	if err := base.Session(&gorm.Session{}).Where("poster_asset IS NULL").Count(&posterNull).Error; err != nil {
		return 0, 0, fmt.Errorf("count canon poster nulls: %w", err)
	}
	if err := base.Session(&gorm.Session{}).Where("backdrop_asset IS NULL").Count(&backdropNull).Error; err != nil {
		return 0, 0, fmt.Errorf("count canon backdrop nulls: %w", err)
	}
	return int(posterNull), int(backdropNull), nil
}

// MarkTMDBSynced stamps series.enrichment_tmdb_synced_at = now for one row.
// Single-column UPDATE — no other columns are touched, so a concurrent
// upsert that COALESCEs enrichment_tmdb_synced_at preserves the value we
// just wrote (and vice versa). SeriesWorker calls this after a successful
// hydration tx; ClearOnSuccess on enrichment_errors fires alongside.
//
// Idempotent on the column: re-bumping a fresh row just refreshes the
// timestamp. The caller passes the desired wall time so tests +
// production share the same clock seam.
func (r *SeriesRepository) MarkTMDBSynced(ctx context.Context, seriesID domain.SeriesID, now time.Time) error {
	if seriesID == 0 {
		return fmt.Errorf("mark series tmdb synced: series_id must be non-zero")
	}
	err := dbFromContext(ctx, r.db).WithContext(ctx).
		Table("series").
		Where("id = ?", seriesID).
		Updates(map[string]any{
			"enrichment_tmdb_synced_at": now.UTC(),
			"updated_at":                now.UTC(),
		}).Error
	if err != nil {
		return fmt.Errorf("mark series tmdb synced: %w", err)
	}
	return nil
}

// MarkOMDBSynced stamps series.enrichment_omdb_synced_at = now. Same
// shape as MarkTMDBSynced — see comment there.
func (r *SeriesRepository) MarkOMDBSynced(ctx context.Context, seriesID domain.SeriesID, now time.Time) error {
	if seriesID == 0 {
		return fmt.Errorf("mark series omdb synced: series_id must be non-zero")
	}
	err := dbFromContext(ctx, r.db).WithContext(ctx).
		Table("series").
		Where("id = ?", seriesID).
		Updates(map[string]any{
			"enrichment_omdb_synced_at": now.UTC(),
			"updated_at":                now.UTC(),
		}).Error
	if err != nil {
		return fmt.Errorf("mark series omdb synced: %w", err)
	}
	return nil
}

// ListStaleForTMDB returns series ids whose enrichment_tmdb_synced_at is
// NULL or older than now-ttl, capped at `limit` rows ordered by id ASC.
// Like ListLibraryWithIMDBStale but for TMDB source: requires a tmdb_id,
// has at least one live series_cache reference (library scope), and
// excludes series with > 5 enrichment_errors attempts (terminal retry
// give-up). Used by the D-3 dispatcher loop for the nightly TMDB sweep.
//
// `GROUP BY` on `s.id, s.tmdb_id` dedups when a series has multiple
// instance refs (1080p + 4K Sonarr is the typical case).
func (r *SeriesRepository) ListStaleForTMDB(ctx context.Context, ttl time.Duration, limit int) ([]domain.SeriesID, error) {
	if limit <= 0 {
		limit = 100
	}
	cutoff := time.Now().UTC().Add(-ttl)
	var ids []domain.SeriesID
	err := dbFromContext(ctx, r.db).WithContext(ctx).
		Table("series AS s").
		Select("s.id").
		Joins(`INNER JOIN series_cache sc
		       ON sc.series_id = s.id
		      AND sc.deleted_at IS NULL`).
		Where("s.tmdb_id IS NOT NULL").
		Where("(s.enrichment_tmdb_synced_at IS NULL OR s.enrichment_tmdb_synced_at < ?)", cutoff).
		Where(`NOT EXISTS (
		    SELECT 1 FROM enrichment_errors ee
		     WHERE ee.entity_type = 'series'
		       AND ee.entity_id = s.id
		       AND ee.source = ?
		       AND ee.attempts > 5)`, string(enrichmentpkg.SourceTMDBSeries)).
		Group("s.id, s.tmdb_id").
		Limit(limit).
		Pluck("s.id", &ids).Error
	if err != nil {
		return nil, fmt.Errorf("list series stale for tmdb: %w", err)
	}
	return ids, nil
}

// ListMissingTMDBSync returns series.id rows that:
//   - have a non-NULL tmdb_id (TMDB-enrichable; B-38 fix), AND
//   - have never been TMDB-enriched (enrichment_tmdb_synced_at IS NULL).
//
// The tmdb_id filter (Story 510, B-38) excludes legacy Sonarr-imported
// series whose canon row has no TMDB ID — those rows are unreachable
// by the TMDB enrichment path and re-enqueuing them every 6h sweep
// produced log noise ("no tmdb_id on canon") with no actionable
// outcome. Operator-driven manual refresh still works via the
// /series/{id}/refresh path (which bypasses this scanner).
//
// The cold-start backfill loop (Story 212, rewritten in 464b) consumes
// this to enqueue initial enrichment jobs. The column-on-canon path
// replaces the pre-D-3 LEFT JOIN sync_log lookup — same selectivity,
// ~5× faster (no join, direct WHERE on the column the worker stamps on
// success).
func (r *SeriesRepository) ListMissingTMDBSync(ctx context.Context, limit int) ([]domain.SeriesID, error) {
	if limit <= 0 {
		limit = 1000
	}
	var ids []domain.SeriesID
	err := dbFromContext(ctx, r.db).WithContext(ctx).
		Table("series").
		Select("id").
		Where("enrichment_tmdb_synced_at IS NULL AND tmdb_id IS NOT NULL").
		Limit(limit).
		Pluck("id", &ids).Error
	if err != nil {
		return nil, fmt.Errorf("list series missing tmdb sync: %w", err)
	}
	return ids, nil
}

// ListLibraryWithIMDBStale returns series.id rows that:
//   - have a non-NULL imdb_id, AND
//   - have AT LEAST ONE live (not soft-deleted) series_cache reference
//     (excludes recommendation stubs that never entered the library), AND
//   - either have no enrichment_omdb_synced_at OR it's older than `ttl`, AND
//   - do NOT have an outstanding enrichment_errors row with attempts > 5
//     (PRD §5.5 retry-give-up — terminal failures stop refreshing).
//
// The terminal-failure filter previously read sync_log.outcome='not_found';
// the new D-3 schema doesn't carry an outcome enum, so the equivalent
// signal is "we've retried this >5 times — leave it alone". The
// 5-attempt threshold matches the OMDb worker's max-retries constant.
// Operator-driven refresh (POST /api/v1/series/{id}/refresh) clears
// the row via ClearOnSuccess — same UX as before.
//
// Used by the Story 213 OMDb daily batch (cron 04:30). The
// series_cache INNER JOIN is the library filter — series_cache rows
// land on Sonarr import / webhook; stub-only series NEVER have a
// series_cache reference. The `series_cache.deleted_at IS NULL` guard
// preserves the soft-delete contract.
//
// `GROUP BY` on `s.id, s.imdb_id` dedups when a series has multiple
// instance refs (typical: 1080p + 4K Sonarr). Postgres + sqlite
// both accept this shape (the SELECT list is a subset of the
// GROUP BY list — no `ANY_VALUE` needed).
func (r *SeriesRepository) ListLibraryWithIMDBStale(ctx context.Context, ttl time.Duration, limit int) ([]domain.SeriesID, error) {
	if limit <= 0 {
		limit = 900
	}
	cutoff := time.Now().UTC().Add(-ttl)
	var ids []domain.SeriesID
	err := dbFromContext(ctx, r.db).WithContext(ctx).
		Table("series AS s").
		Select("s.id").
		Joins(`INNER JOIN series_cache sc
		       ON sc.series_id = s.id
		      AND sc.deleted_at IS NULL`).
		Where("s.imdb_id IS NOT NULL").
		Where("s.imdb_id != ''").
		Where("(s.enrichment_omdb_synced_at IS NULL OR s.enrichment_omdb_synced_at < ?)", cutoff).
		Where(`NOT EXISTS (
		    SELECT 1 FROM enrichment_errors ee
		     WHERE ee.entity_type = 'series'
		       AND ee.entity_id = s.id
		       AND ee.source = ?
		       AND ee.attempts > 5)`, string(enrichmentpkg.SourceOMDb)).
		Group("s.id, s.imdb_id").
		Limit(limit).
		Pluck("s.id", &ids).Error
	if err != nil {
		return nil, fmt.Errorf("list library with imdb stale: %w", err)
	}
	return ids, nil
}

// ListOrphanCandidates returns series.id rows older than cutoff that
// have no live series_cache reference AND no series_recommendations
// reference. Story 218 (E-2).
func (r *SeriesRepository) ListOrphanCandidates(ctx context.Context, cutoff time.Time, limit int) ([]domain.SeriesID, error) {
	if limit <= 0 {
		limit = 1000
	}
	var ids []domain.SeriesID
	err := dbFromContext(ctx, r.db).WithContext(ctx).
		Table("series AS s").
		Select("s.id").
		Where("s.created_at < ?", cutoff).
		Where(`NOT EXISTS (
		    SELECT 1 FROM series_cache sc
		     WHERE sc.series_id = s.id AND sc.deleted_at IS NULL)`).
		Where(`NOT EXISTS (
		    SELECT 1 FROM series_recommendations sr
		     WHERE sr.recommended_series_id = s.id)`).
		Limit(limit).
		Pluck("s.id", &ids).Error
	if err != nil {
		return nil, fmt.Errorf("list orphan series candidates: %w", err)
	}
	return ids, nil
}

// DropSeriesCascade hard-deletes the canon series row + every
// dependent entity-model row in a single transaction. Story 218
// (E-2). Idempotent: DELETEs of zero rows are fine.
//
// The deletion order follows the dependency direction so a re-run
// after a crash is safe: dependent rows first, then the canon row.
func (r *SeriesRepository) DropSeriesCascade(ctx context.Context, seriesID domain.SeriesID) error {
	if seriesID == 0 {
		return errors.New("drop series cascade: series_id must be non-zero")
	}
	db := dbFromContext(ctx, r.db).WithContext(ctx)
	return db.Transaction(func(tx *gorm.DB) error {
		// D-3 (story 464c): episode_people / series_people / origin_releases
		// dropped at the schema level. The cascade no longer needs to DELETE
		// from them — the table doesn't exist in the new schema, so the
		// statement would fail at runtime. person_credits is shared globally
		// per tmdb_media_id (PRD §5.3) and explicitly NOT cascade-deleted
		// from a single series — the credit row outlives every individual
		// series that referenced it.
		stmts := []struct {
			name string
			sql  string
			args []any
		}{
			{"episode_texts",
				`DELETE FROM episode_texts
				    WHERE episode_id IN (SELECT id FROM episodes WHERE series_id = ?)`,
				[]any{seriesID}},
			{"episode_states",
				`DELETE FROM episode_states
				    WHERE episode_id IN (SELECT id FROM episodes WHERE series_id = ?)`,
				[]any{seriesID}},
			{"episodes", `DELETE FROM episodes WHERE series_id = ?`, []any{seriesID}},
			{"seasons", `DELETE FROM seasons WHERE series_id = ?`, []any{seriesID}},
			{"series_genres", `DELETE FROM series_genres WHERE series_id = ?`, []any{seriesID}},
			{"series_networks", `DELETE FROM series_networks WHERE series_id = ?`, []any{seriesID}},
			{"series_companies", `DELETE FROM series_companies WHERE series_id = ?`, []any{seriesID}},
			{"series_keywords", `DELETE FROM series_keywords WHERE series_id = ?`, []any{seriesID}},
			{"videos", `DELETE FROM videos WHERE series_id = ?`, []any{seriesID}},
			{"content_ratings", `DELETE FROM content_ratings WHERE series_id = ?`, []any{seriesID}},
			{"external_ids",
				`DELETE FROM external_ids WHERE entity_type = 'series' AND entity_id = ?`,
				[]any{seriesID}},
			{"series_texts", `DELETE FROM series_texts WHERE series_id = ?`, []any{seriesID}},
			{"series_recommendations",
				`DELETE FROM series_recommendations WHERE series_id = ? OR recommended_series_id = ?`,
				[]any{seriesID, seriesID}},
			{"series", `DELETE FROM series WHERE id = ?`, []any{seriesID}},
		}
		for _, s := range stmts {
			if err := tx.Exec(s.sql, s.args...).Error; err != nil {
				return fmt.Errorf("drop series cascade (%s): %w", s.name, err)
			}
		}
		return nil
	})
}

// seriesUpsertAssignments builds the DO UPDATE SET map for Upsert.
//
// Most columns are direct assignments (excluded.X) — Upsert is the
// authoritative path for TMDB enrichment and merge-boundary writes, so
// new values overwrite. The exceptions are three "additive-only" columns
// that a Sonarr-driven canonOut (PRD §5.4) carries as NULL even though
// the existing row may already hold valid TMDB-enriched values:
//
//   - poster_asset / backdrop_asset: COALESCE(excluded.X, series.X)
//     — non-NULL input wins (TMDB sync stays authoritative); NULL input
//     keeps the existing value (Sonarr-only payloads don't clobber).
//   - hydration: CASE — 'full' is sticky in both directions. Once the
//     row is enriched the Sonarr canonOut's 'stub' value MUST NOT
//     downgrade it back to stub.
//
// The pre-fix path used clause.AssignmentColumns over the full column
// set, which blanked posters/backdrops and downgraded hydration whenever
// a Sonarr scan re-emitted an already-enriched series. Shared across
// the id-conflict and tmdb_id-conflict branches: callers reach the
// id-conflict branch with a row they already loaded, but their canonOut
// may still be a Sonarr-merge product missing the image fields, so the
// same guard applies.
func seriesUpsertAssignments() map[string]any {
	// COALESCE(excluded.X, series.X) on TMDB/OMDb-owned columns: a
	// Sonarr-driven Upsert (MergeSeries(SourceSonarr) path) leaves
	// those columns nil in canonOut whenever the in-memory canon read
	// was stale or raced. excluded.X then writes NULL on top of a
	// previously-enriched row. Mirror of Story 552's poster/backdrop
	// protection, extended after live regression on series.id=8 R&M
	// and id=96 Star City lost tmdb_rating + first_air_date +
	// origin_countries between /refresh and the next scan tick.
	return map[string]any{
		"tmdb_id":           gorm.Expr("excluded.tmdb_id"),
		"tvdb_id":           gorm.Expr("excluded.tvdb_id"),
		"imdb_id":           gorm.Expr("excluded.imdb_id"),
		"hydration":         gorm.Expr("CASE WHEN series.hydration = 'full' THEN 'full' WHEN excluded.hydration = 'full' THEN 'full' ELSE excluded.hydration END"),
		"title":             gorm.Expr("excluded.title"),
		"original_title":    gorm.Expr("COALESCE(excluded.original_title, series.original_title)"),
		"status":            gorm.Expr("COALESCE(excluded.status, series.status)"),
		"first_air_date":    gorm.Expr("COALESCE(excluded.first_air_date, series.first_air_date)"),
		"last_air_date":     gorm.Expr("COALESCE(excluded.last_air_date, series.last_air_date)"),
		"next_air_date":     gorm.Expr("excluded.next_air_date"),
		"year":              gorm.Expr("excluded.year"),
		"runtime_minutes":   gorm.Expr("excluded.runtime_minutes"),
		"homepage":          gorm.Expr("COALESCE(excluded.homepage, series.homepage)"),
		"original_language": gorm.Expr("COALESCE(excluded.original_language, series.original_language)"),
		"origin_country":    gorm.Expr("COALESCE(excluded.origin_country, series.origin_country)"),
		// origin_countries is NOT NULL DEFAULT '[]' so a Sonarr-stub
		// canonOut writes the literal '[]' here. Plain COALESCE picks
		// the first non-NULL value — '[]' wins over series.['US']' and
		// nukes the enrichment. NULLIF turns the empty-array sentinel
		// back into NULL so COALESCE falls through to the existing row.
		"origin_countries": gorm.Expr("COALESCE(NULLIF(excluded.origin_countries, '[]'), series.origin_countries)"),
		"popularity":       gorm.Expr("COALESCE(excluded.popularity, series.popularity)"),
		"in_production":    gorm.Expr("excluded.in_production"),
		"poster_asset":     gorm.Expr("COALESCE(excluded.poster_asset, series.poster_asset)"),
		"backdrop_asset":   gorm.Expr("COALESCE(excluded.backdrop_asset, series.backdrop_asset)"),
		"tmdb_rating":      gorm.Expr("COALESCE(excluded.tmdb_rating, series.tmdb_rating)"),
		"tmdb_votes":       gorm.Expr("COALESCE(excluded.tmdb_votes, series.tmdb_votes)"),
		"imdb_rating":      gorm.Expr("COALESCE(excluded.imdb_rating, series.imdb_rating)"),
		"imdb_votes":       gorm.Expr("COALESCE(excluded.imdb_votes, series.imdb_votes)"),
		"omdb_rated":       gorm.Expr("COALESCE(excluded.omdb_rated, series.omdb_rated)"),
		"omdb_awards":      gorm.Expr("COALESCE(excluded.omdb_awards, series.omdb_awards)"),
		// D-3 freshness columns — COALESCE so a Sonarr-driven canonOut
		// (PRD §5.4) that carries nil does NOT blank a previously-set
		// enrichment timestamp. Same protection as poster_asset /
		// tmdb_rating above; without it every Sonarr re-scan would
		// reset enrichment_*_synced_at to NULL and trigger a re-enrichment
		// storm.
		"enrichment_tmdb_synced_at": gorm.Expr("COALESCE(excluded.enrichment_tmdb_synced_at, series.enrichment_tmdb_synced_at)"),
		"enrichment_omdb_synced_at": gorm.Expr("COALESCE(excluded.enrichment_omdb_synced_at, series.enrichment_omdb_synced_at)"),
		"updated_at":                gorm.Expr("excluded.updated_at"),
	}
}

func toCanon(m database.SeriesModel) series.Canon {
	return series.Canon{
		ID:                     m.ID,
		TMDBID:                 m.TMDBID,
		TVDBID:                 m.TVDBID,
		IMDBID:                 m.IMDBID,
		Hydration:              series.Hydration(m.Hydration),
		Title:                  m.Title,
		OriginalTitle:          m.OriginalTitle,
		Status:                 m.Status,
		FirstAirDate:           m.FirstAirDate,
		LastAirDate:            m.LastAirDate,
		NextAirDate:            m.NextAirDate,
		Year:                   m.Year,
		RuntimeMinutes:         m.RuntimeMinutes,
		Homepage:               m.Homepage,
		OriginalLanguage:       m.OriginalLanguage,
		OriginCountry:          m.OriginCountry,
		OriginCountries:        decodeOriginCountries(m.OriginCountries),
		Popularity:             m.Popularity,
		InProduction:           m.InProduction,
		PosterAsset:            m.PosterAsset,
		BackdropAsset:          m.BackdropAsset,
		TMDBRating:             m.TMDBRating,
		TMDBVotes:              m.TMDBVotes,
		IMDBRating:             m.IMDBRating,
		IMDBVotes:              m.IMDBVotes,
		OMDBRated:              m.OMDBRated,
		OMDBAwards:             m.OMDBAwards,
		EnrichmentTMDBSyncedAt: m.EnrichmentTMDBSyncedAt,
		EnrichmentOMDBSyncedAt: m.EnrichmentOMDBSyncedAt,
		CreatedAt:              m.CreatedAt,
		UpdatedAt:              m.UpdatedAt,
	}
}

func fromCanon(c series.Canon) database.SeriesModel {
	return database.SeriesModel{
		ID:                     c.ID,
		TMDBID:                 c.TMDBID,
		TVDBID:                 c.TVDBID,
		IMDBID:                 c.IMDBID,
		Hydration:              string(c.Hydration),
		Title:                  c.Title,
		OriginalTitle:          c.OriginalTitle,
		Status:                 c.Status,
		FirstAirDate:           c.FirstAirDate,
		LastAirDate:            c.LastAirDate,
		NextAirDate:            c.NextAirDate,
		Year:                   c.Year,
		RuntimeMinutes:         c.RuntimeMinutes,
		Homepage:               c.Homepage,
		OriginalLanguage:       c.OriginalLanguage,
		OriginCountry:          c.OriginCountry,
		OriginCountries:        encodeOriginCountries(c.OriginCountries),
		Popularity:             c.Popularity,
		InProduction:           c.InProduction,
		PosterAsset:            c.PosterAsset,
		BackdropAsset:          c.BackdropAsset,
		TMDBRating:             c.TMDBRating,
		TMDBVotes:              c.TMDBVotes,
		IMDBRating:             c.IMDBRating,
		IMDBVotes:              c.IMDBVotes,
		OMDBRated:              c.OMDBRated,
		OMDBAwards:             c.OMDBAwards,
		EnrichmentTMDBSyncedAt: c.EnrichmentTMDBSyncedAt,
		EnrichmentOMDBSyncedAt: c.EnrichmentOMDBSyncedAt,
		CreatedAt:              c.CreatedAt,
		UpdatedAt:              c.UpdatedAt,
	}
}

// encodeOriginCountries marshals a string slice to a datatypes.JSON
// (storage column origin_countries text NOT NULL DEFAULT '[]'). nil
// + empty slice both serialize as the literal `[]` JSON document so
// the NOT NULL column constraint holds whether the caller leaves the
// field unset or pins it to an empty slice. The read-side
// decodeOriginCountries treats `[]` as nil, preserving the previous
// "no countries" sentinel.
func encodeOriginCountries(s []string) datatypes.JSON {
	if len(s) == 0 {
		return datatypes.JSON("[]")
	}
	b, err := json.Marshal(s)
	if err != nil {
		return datatypes.JSON("[]")
	}
	return datatypes.JSON(b)
}

// decodeOriginCountries unmarshals datatypes.JSON to a string slice.
// Returns nil on empty / invalid JSON; never panics. An empty array
// (`[]`) round-trips to nil too — callers that wrote an empty slice
// (or left the field unset) read back nil, matching the pre-D-3
// "no countries" sentinel.
func decodeOriginCountries(j datatypes.JSON) []string {
	if len(j) == 0 {
		return nil
	}
	var out []string
	if err := json.Unmarshal(j, &out); err != nil {
		return nil
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
