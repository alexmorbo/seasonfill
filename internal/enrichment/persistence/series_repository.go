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

// ListMissingSyncLog returns series.id rows that have NO sync_log
// row for (entity_type='series', source=<source>). LEFT JOIN +
// IS NULL — selectivity is good on a typical library (300 rows) and
// the (entity_type, entity_id, source) index covers the join. Limit
// caps result-set size; cold-start callers pass 5000.
//
// Used by the application-layer cold-start backfill (Story 212).
func (r *SeriesRepository) ListMissingSyncLog(ctx context.Context, source string, limit int) ([]domain.SeriesID, error) {
	if limit <= 0 {
		limit = 1000
	}
	var ids []domain.SeriesID
	err := dbFromContext(ctx, r.db).WithContext(ctx).
		Table("series AS s").
		Select("s.id").
		Joins(`LEFT JOIN sync_log sl
		       ON sl.entity_type = 'series'
		      AND sl.entity_id   = s.id
		      AND sl.source      = ?`, source).
		Where("sl.entity_id IS NULL").
		Limit(limit).
		Pluck("s.id", &ids).Error
	if err != nil {
		return nil, fmt.Errorf("list series missing sync_log(%s): %w", source, err)
	}
	return ids, nil
}

// ListLibraryWithIMDBStale returns series.id rows that:
//   - have a non-NULL imdb_id, AND
//   - have AT LEAST ONE live (not soft-deleted) series_cache reference
//     (excludes recommendation stubs that never entered the library), AND
//   - either have no sync_log(omdb) row OR the last sync_log row is
//     older than `ttl` AND its outcome is NOT 'not_found' (terminal).
//
// Used by the Story 213 OMDb daily batch (cron 04:30).
// The series_cache INNER JOIN is the library filter — series_cache rows
// land on Sonarr import / webhook; stub-only series (recommendation
// tiles, anime hold-overs) NEVER have a series_cache reference. The
// `series_cache.deleted_at IS NULL` guard preserves the soft-delete
// contract: a series whose only instance reference was deleted no
// longer "lives" in the library, so OMDb stops refreshing it
// (matches the PRD §5.4 grain decision).
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
		Joins(`LEFT JOIN sync_log sl
		       ON sl.entity_type = 'series'
		      AND sl.entity_id   = s.id
		      AND sl.source      = ?`, string(enrichmentpkg.SourceOMDb)).
		Where("s.imdb_id IS NOT NULL").
		Where("s.imdb_id != ''").
		Where("(sl.outcome IS NULL OR sl.outcome != ?)", string(enrichmentpkg.OutcomeNotFound)).
		Where("(sl.synced_at IS NULL OR sl.synced_at < ?)", cutoff).
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
		stmts := []struct {
			name string
			sql  string
			args []any
		}{
			{"episode_people",
				`DELETE FROM episode_people
				    WHERE episode_id IN (SELECT id FROM episodes WHERE series_id = ?)`,
				[]any{seriesID}},
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
			{"series_people", `DELETE FROM series_people WHERE series_id = ?`, []any{seriesID}},
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
		"origin_countries":  gorm.Expr("COALESCE(excluded.origin_countries, series.origin_countries)"),
		"popularity":        gorm.Expr("COALESCE(excluded.popularity, series.popularity)"),
		"in_production":     gorm.Expr("excluded.in_production"),
		"poster_asset":      gorm.Expr("COALESCE(excluded.poster_asset, series.poster_asset)"),
		"backdrop_asset":    gorm.Expr("COALESCE(excluded.backdrop_asset, series.backdrop_asset)"),
		"tmdb_rating":       gorm.Expr("COALESCE(excluded.tmdb_rating, series.tmdb_rating)"),
		"tmdb_votes":        gorm.Expr("COALESCE(excluded.tmdb_votes, series.tmdb_votes)"),
		"imdb_rating":       gorm.Expr("COALESCE(excluded.imdb_rating, series.imdb_rating)"),
		"imdb_votes":        gorm.Expr("COALESCE(excluded.imdb_votes, series.imdb_votes)"),
		"omdb_rated":        gorm.Expr("COALESCE(excluded.omdb_rated, series.omdb_rated)"),
		"omdb_awards":       gorm.Expr("COALESCE(excluded.omdb_awards, series.omdb_awards)"),
		"updated_at":        gorm.Expr("excluded.updated_at"),
	}
}

func toCanon(m database.SeriesModel) series.Canon {
	return series.Canon{
		ID:               m.ID,
		TMDBID:           m.TMDBID,
		TVDBID:           m.TVDBID,
		IMDBID:           m.IMDBID,
		Hydration:        series.Hydration(m.Hydration),
		Title:            m.Title,
		OriginalTitle:    m.OriginalTitle,
		Status:           m.Status,
		FirstAirDate:     m.FirstAirDate,
		LastAirDate:      m.LastAirDate,
		NextAirDate:      m.NextAirDate,
		Year:             m.Year,
		RuntimeMinutes:   m.RuntimeMinutes,
		Homepage:         m.Homepage,
		OriginalLanguage: m.OriginalLanguage,
		OriginCountry:    m.OriginCountry,
		OriginCountries:  decodeOriginCountries(m.OriginCountries),
		Popularity:       m.Popularity,
		InProduction:     m.InProduction,
		PosterAsset:      m.PosterAsset,
		BackdropAsset:    m.BackdropAsset,
		TMDBRating:       m.TMDBRating,
		TMDBVotes:        m.TMDBVotes,
		IMDBRating:       m.IMDBRating,
		IMDBVotes:        m.IMDBVotes,
		OMDBRated:        m.OMDBRated,
		OMDBAwards:       m.OMDBAwards,
		CreatedAt:        m.CreatedAt,
		UpdatedAt:        m.UpdatedAt,
	}
}

func fromCanon(c series.Canon) database.SeriesModel {
	return database.SeriesModel{
		ID:               c.ID,
		TMDBID:           c.TMDBID,
		TVDBID:           c.TVDBID,
		IMDBID:           c.IMDBID,
		Hydration:        string(c.Hydration),
		Title:            c.Title,
		OriginalTitle:    c.OriginalTitle,
		Status:           c.Status,
		FirstAirDate:     c.FirstAirDate,
		LastAirDate:      c.LastAirDate,
		NextAirDate:      c.NextAirDate,
		Year:             c.Year,
		RuntimeMinutes:   c.RuntimeMinutes,
		Homepage:         c.Homepage,
		OriginalLanguage: c.OriginalLanguage,
		OriginCountry:    c.OriginCountry,
		OriginCountries:  encodeOriginCountries(c.OriginCountries),
		Popularity:       c.Popularity,
		InProduction:     c.InProduction,
		PosterAsset:      c.PosterAsset,
		BackdropAsset:    c.BackdropAsset,
		TMDBRating:       c.TMDBRating,
		TMDBVotes:        c.TMDBVotes,
		IMDBRating:       c.IMDBRating,
		IMDBVotes:        c.IMDBVotes,
		OMDBRated:        c.OMDBRated,
		OMDBAwards:       c.OMDBAwards,
		CreatedAt:        c.CreatedAt,
		UpdatedAt:        c.UpdatedAt,
	}
}

// encodeOriginCountries marshals a string slice to a datatypes.JSON
// (storage column origin_countries text). nil + empty slice both
// roundtrip as NULL in the database — neither has display value and
// distinguishing them is not worth the read-side branch.
func encodeOriginCountries(s []string) datatypes.JSON {
	if len(s) == 0 {
		return nil
	}
	b, err := json.Marshal(s)
	if err != nil {
		return nil
	}
	return datatypes.JSON(b)
}

// decodeOriginCountries unmarshals datatypes.JSON to a string slice.
// Returns nil on empty / invalid JSON; never panics.
func decodeOriginCountries(j datatypes.JSON) []string {
	if len(j) == 0 {
		return nil
	}
	var out []string
	if err := json.Unmarshal(j, &out); err != nil {
		return nil
	}
	return out
}
