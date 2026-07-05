package persistence

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"gorm.io/gorm"

	disco "github.com/alexmorbo/seasonfill/internal/discovery/domain"
	shareddomain "github.com/alexmorbo/seasonfill/internal/shared/domain"
)

// DiscoveryListsModel mirrors the discovery_lists table (migration
// 000021). Composite PK (kind, param, language, series_id) — declared
// via `primaryKey` tags on all four columns; GORM lifts that into the
// CREATE TABLE constraint and the equivalent UPDATE/DELETE WHERE
// clause when GORM-builder methods are used. The repository writes
// through `tx.CreateInBatches(&rows, 100)` only (NO `ON CONFLICT`
// clause) — see ReplaceList godoc for why DELETE+INSERT is the
// canonical idiom here.
type DiscoveryListsModel struct {
	Kind        string                `gorm:"primaryKey;column:kind;type:text;not null"`
	Param       string                `gorm:"primaryKey;column:param;type:text;not null;default:''"`
	Language    string                `gorm:"primaryKey;column:language;type:text;not null"`
	SeriesID    shareddomain.SeriesID `gorm:"primaryKey;column:series_id;not null"`
	Position    int                   `gorm:"column:position;not null"`
	RefreshedAt time.Time             `gorm:"column:refreshed_at;not null"`
	// Year / TMDBRating (story 1036) — ingest-stored TMDB list facts
	// (first_air_date year + vote_average). Nullable: a TMDB list entry
	// may omit first_air_date or ship vote_average 0 (→ NULL).
	Year       *int     `gorm:"column:year"`
	TMDBRating *float64 `gorm:"column:tmdb_rating"`
}

// TableName pins the GORM mapping to discovery_lists (migration 000021).
func (DiscoveryListsModel) TableName() string { return "discovery_lists" }

// ListRepository is the GORM-backed implementation of
// app.DiscoveryListRepo. Reads ride the discovery_lists_lookup_idx
// (kind, param, language, position); writes go through a transactional
// DELETE+INSERT pair.
//
// COALESCE pattern audit (project_seasonfill_upsert_coalesce_pattern):
// the table has 8 columns. Four (kind/param/language/series_id) form
// the PK and are written verbatim. The rest (position, refreshed_at,
// year, tmdb_rating) are owned solely by the discovery worker — no other
// source emits writes against them, so there is no second writer to
// COALESCE against. The
// risk pattern from B-13 / Story 552 (multi-writer column blanking)
// does not apply here. ReplaceList ships DELETE+INSERT instead of
// UPSERT specifically so a partial item-set never half-clears the
// table; the transaction guarantees the swap is atomic.
type ListRepository struct {
	db *gorm.DB
}

// NewListRepository binds the repo to db.
func NewListRepository(db *gorm.DB) *ListRepository {
	return &ListRepository{db: db}
}

// GetRanked returns one Page of items hydrated from the
// discovery_lists × series join. Items are sorted by position ASC.
// Total is the unpaged count of rows for the (kind, param, language)
// tuple — issued as a second cheap COUNT(*) query because
// pulling COUNT(*) OVER() into the same SELECT pessimises the
// index-only-scan plan on Postgres.
//
// page is 1-indexed; perPage clamps to [1, 200]. The clamp is defensive
// against a forgotten validation in the handler — the repository
// refuses to issue an OFFSET larger than 1M rows to avoid accidental
// table scans.
func (r *ListRepository) GetRanked(
	ctx context.Context,
	kind disco.Kind,
	param, language string,
	page, perPage int,
) (disco.Page, error) {
	if !kind.IsValid() {
		return disco.Page{}, fmt.Errorf("discovery list repo: get ranked: invalid kind %q", kind)
	}
	if page < 1 {
		page = 1
	}
	if perPage < 1 {
		perPage = 20
	}
	if perPage > 200 {
		perPage = 200
	}
	offset := (page - 1) * perPage
	if offset > 1_000_000 {
		return disco.Page{}, fmt.Errorf("discovery list repo: get ranked: offset overflow (page=%d perPage=%d)", page, perPage)
	}

	// Story 523 / N-4 unblock: tvdb_id + original_language join into the
	// projection so the FE AddToSonarr modal can submit without a
	// separate /series/{id} fetch. Both columns are NULL-tolerant — a
	// stub upserted via the legacy Sonarr-orphan path may carry NULL.
	// S-E3a — canon series.title / poster_asset / backdrop_asset were dropped
	// from the domain (columns now dead). The display title resolves from
	// series_texts and the art from series_media_texts, both with the list's
	// language → en-US fallback.
	const selectQ = `
		SELECT d.series_id, d.refreshed_at,
		       s.tmdb_id, s.tvdb_id,
		       COALESCE((SELECT st.title FROM series_texts st WHERE st.series_id = s.id
		         ORDER BY CASE WHEN st.language = ? THEN 2 WHEN st.language = 'en-US' THEN 1 ELSE 0 END DESC,
		                  st.language ASC LIMIT 1), s.original_title) AS title,
		       COALESCE(s.year, d.year) AS year,
		       COALESCE(s.tmdb_rating, d.tmdb_rating) AS tmdb_rating,
		       (SELECT smt.poster_asset FROM series_media_texts smt WHERE smt.series_id = s.id
		         ORDER BY CASE WHEN smt.language = ? THEN 2 WHEN smt.language = 'en-US' THEN 1 ELSE 0 END DESC,
		                  smt.language ASC LIMIT 1) AS poster_asset,
		       (SELECT smt.backdrop_asset FROM series_media_texts smt WHERE smt.series_id = s.id
		         ORDER BY CASE WHEN smt.language = ? THEN 2 WHEN smt.language = 'en-US' THEN 1 ELSE 0 END DESC,
		                  smt.language ASC LIMIT 1) AS backdrop_asset,
		       s.original_language, s.origin_countries, s.tmdb_type, d.position
		  FROM discovery_lists d
		  JOIN series s ON s.id = d.series_id
		 WHERE d.kind = ? AND d.param = ? AND d.language = ?
		 ORDER BY d.position ASC
		 LIMIT ? OFFSET ?`

	type joinedRow struct {
		SeriesID         shareddomain.SeriesID `gorm:"column:series_id"`
		RefreshedAt      time.Time             `gorm:"column:refreshed_at"`
		TMDBID           *shareddomain.TMDBID  `gorm:"column:tmdb_id"`
		TVDBID           *shareddomain.TVDBID  `gorm:"column:tvdb_id"`
		Title            string                `gorm:"column:title"`
		Year             *int                  `gorm:"column:year"`
		TMDBRating       *float64              `gorm:"column:tmdb_rating"`
		PosterAsset      *string               `gorm:"column:poster_asset"`
		BackdropAsset    *string               `gorm:"column:backdrop_asset"`
		OriginalLanguage *string               `gorm:"column:original_language"`
		OriginCountries  []byte                `gorm:"column:origin_countries"`
		TMDBType         *int                  `gorm:"column:tmdb_type"`
		Position         int                   `gorm:"column:position"`
	}
	var rows []joinedRow
	if err := r.db.WithContext(ctx).
		Raw(selectQ, language, language, language, string(kind), param, language, perPage, offset).
		Scan(&rows).Error; err != nil {
		return disco.Page{}, fmt.Errorf("discovery list repo: get ranked: %w", err)
	}

	const countQ = `
		SELECT COUNT(*) FROM discovery_lists
		 WHERE kind = ? AND param = ? AND language = ?`
	var total int64
	if err := r.db.WithContext(ctx).
		Raw(countQ, string(kind), param, language).Scan(&total).Error; err != nil {
		return disco.Page{}, fmt.Errorf("discovery list repo: get ranked: count: %w", err)
	}

	items := make([]disco.Item, 0, len(rows))
	var maxRefreshedAt time.Time
	for _, row := range rows {
		if row.RefreshedAt.After(maxRefreshedAt) {
			maxRefreshedAt = row.RefreshedAt
		}
		var countries []string
		if len(row.OriginCountries) > 0 {
			if err := json.Unmarshal(row.OriginCountries, &countries); err != nil {
				// Bad JSON on the joined series row is a data-integrity bug,
				// not a query failure — surface it but keep the rest of the
				// page consumable. Story 506 worker will resync on next tick.
				countries = nil
			}
		}
		items = append(items, disco.Item{
			SeriesID:         row.SeriesID,
			TMDBID:           row.TMDBID,
			TVDBID:           row.TVDBID,
			Title:            row.Title,
			Year:             row.Year,
			TMDBRating:       row.TMDBRating,
			PosterPath:       row.PosterAsset,
			BackdropPath:     row.BackdropAsset,
			OriginalLanguage: row.OriginalLanguage,
			OriginCountries:  countries,
			TMDBType:         row.TMDBType,
		})
	}

	return disco.Page{
		Items:       items,
		RefreshedAt: maxRefreshedAt,
		Total:       int(total),
	}, nil
}

// IsStale returns true when the max(refreshed_at) for (kind, param,
// language) is older than `ttl` ago, OR when no rows exist (the
// "never refreshed" branch returns true so the worker enqueues a
// first refresh).
func (r *ListRepository) IsStale(
	ctx context.Context,
	kind disco.Kind,
	param, language string,
	ttl time.Duration,
) (bool, error) {
	if !kind.IsValid() {
		return false, fmt.Errorf("discovery list repo: is stale: invalid kind %q", kind)
	}
	at, err := r.LastRefreshedAt(ctx, kind, param, language)
	if err != nil {
		return false, err
	}
	if at.IsZero() {
		return true, nil
	}
	return time.Since(at) > ttl, nil
}

// LastRefreshedAt returns max(refreshed_at) for the (kind, param,
// language) tuple, or time.Time{} (zero value) if no row exists.
//
// Implementation note: scanning a raw `SELECT MAX(refreshed_at)`
// aggregate hits a driver edge case — the SQLite shadow returns the
// column as a text-format string the database/sql layer refuses to
// auto-coerce into time.Time, while Postgres returns a proper timestamp.
// We sidestep this by selecting a full row via the GORM-registered
// DiscoveryListsModel (so GORM's dialect-aware converter normalises
// the timestamp) ordered by refreshed_at DESC LIMIT 1. The empty-result
// branch returns time.Time{} so callers can distinguish "never
// refreshed" via IsZero().
func (r *ListRepository) LastRefreshedAt(
	ctx context.Context,
	kind disco.Kind,
	param, language string,
) (time.Time, error) {
	if !kind.IsValid() {
		return time.Time{}, fmt.Errorf("discovery list repo: last refreshed at: invalid kind %q", kind)
	}
	var row DiscoveryListsModel
	err := r.db.WithContext(ctx).
		Where("kind = ? AND param = ? AND language = ?", string(kind), param, language).
		Order("refreshed_at DESC").
		Limit(1).
		Take(&row).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return time.Time{}, nil
		}
		return time.Time{}, fmt.Errorf("discovery list repo: last refreshed at: %w", err)
	}
	return row.RefreshedAt.UTC(), nil
}

// HasAnyList reports whether at least one discovery_lists row exists,
// across every (kind, param, language) tuple. Issued as a cheap LIMIT 1
// probe via GORM's Take — returns (false, nil) on gorm.ErrRecordNotFound
// (empty table). Used by the worker's post-Tick warming probe to flip
// warming=false on a redeploy where every list is already fresh, so the
// "no refresh fired this Tick" branch doesn't strand the cold-start
// envelope on the HTTP handlers forever.
func (r *ListRepository) HasAnyList(ctx context.Context) (bool, error) {
	var row DiscoveryListsModel
	err := r.db.WithContext(ctx).Limit(1).Take(&row).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return false, nil
		}
		return false, fmt.Errorf("discovery list repo: has any list: %w", err)
	}
	return true, nil
}

// ReplaceList atomically swaps the row-set for (kind, param, language).
// Steps inside the transaction:
//  1. DELETE FROM discovery_lists WHERE kind=? AND param=? AND language=?
//  2. Batch-insert the new items with positions 1..N.
//
// All rows share the same refreshed_at (the transaction wall-clock at
// step 1). This matches the worker's "one fetch → one ReplaceList"
// contract: every row in a freshly-refreshed list IS contemporaneous.
//
// Empty `items` clears the list (DELETE without INSERT). Caller is
// responsible for ensuring every Item.SeriesID exists in `series`
// before invoking — the FK is RDBMS-enforced and an orphan id surfaces
// as a wrapped error from the INSERT step.
//
// Concurrency: two ReplaceList calls against the same (kind, param,
// language) key serialise on the row-level lock taken by step 1's
// DELETE — Postgres acquires a row-exclusive lock on every matched
// row, and the second tx blocks until the first commits. Net result:
// the LATER caller's row-set wins. The repo does NOT take an
// advisory/table lock — the row-level serialisation is sufficient and
// the cost of a missed-update is just a stale page until the next
// worker tick.
func (r *ListRepository) ReplaceList(
	ctx context.Context,
	kind disco.Kind,
	param, language string,
	items []disco.Item,
) error {
	if !kind.IsValid() {
		return fmt.Errorf("discovery list repo: replace list: invalid kind %q", kind)
	}
	now := time.Now().UTC()
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.
			Where("kind = ? AND param = ? AND language = ?", string(kind), param, language).
			Delete(&DiscoveryListsModel{}).Error; err != nil {
			return fmt.Errorf("discovery list repo: replace list: clear: %w", err)
		}
		if len(items) == 0 {
			return nil
		}
		rows := make([]DiscoveryListsModel, 0, len(items))
		for i, it := range items {
			if it.SeriesID == 0 {
				return fmt.Errorf("discovery list repo: replace list: item[%d] series_id must be non-zero", i)
			}
			rows = append(rows, DiscoveryListsModel{
				Kind:        string(kind),
				Param:       param,
				Language:    language,
				SeriesID:    it.SeriesID,
				Position:    i + 1,
				RefreshedAt: now,
				Year:        it.Year,
				TMDBRating:  it.TMDBRating,
			})
		}
		// CreateInBatches keeps the per-INSERT parameter count under
		// Postgres' 65k bind-param ceiling for very large lists
		// (worker typically writes ≤200 items, but the limit is
		// cheap insurance).
		if err := tx.CreateInBatches(&rows, 100).Error; err != nil {
			return fmt.Errorf("discovery list repo: replace list: insert: %w", err)
		}
		return nil
	})
}
