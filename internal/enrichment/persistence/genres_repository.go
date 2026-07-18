package persistence

import (
	"context"
	"errors"
	"fmt"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/alexmorbo/seasonfill/internal/enrichment/domain/taxonomy"
	ports "github.com/alexmorbo/seasonfill/internal/shared/dataports"
	database "github.com/alexmorbo/seasonfill/internal/shared/db"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
)

// GenresRepository persists the `genres` table + the `series_genres`
// join. Localised names live in `genres_i18n`; GenresRepository.Get
// composes the resolved name via the shared §5.6 fallback helper.
//
// ResolveByName implements the PRD §5.4 Sonarr-genre fallback:
// "Drama" string in en-US resolves to a canonical genres.id row. The
// (language, name) index on genres_i18n is what makes this query an
// index range rather than a full scan.
type GenresRepository struct {
	db *gorm.DB
}

func NewGenresRepository(db *gorm.DB) *GenresRepository {
	return &GenresRepository{db: db}
}

// Get fetches by primary key and resolves the localised name via the
// shared §5.6 fallback helper. Empty language is normalised to en-US
// by the helper. Returns ports.ErrNotFound on miss of the genre row;
// a genre without any genres_i18n row returns the Genre with empty
// Name / Language (NOT an error — a freshly-stubbed genre may have
// no i18n rows yet).
func (r *GenresRepository) Get(ctx context.Context, id int64, language string) (taxonomy.Genre, error) {
	db := dbFromContext(ctx, r.db).WithContext(ctx)
	var m database.GenreModel
	err := db.Where("id = ?", id).First(&m).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return taxonomy.Genre{}, ports.ErrNotFound
		}
		return taxonomy.Genre{}, fmt.Errorf("get genre: %w", err)
	}
	g := toGenre(m)

	var i18n database.GenreI18nModel
	if err := pickLanguageFallback(
		ctx, r.db,
		"genres_i18n", "genre_id",
		id, language,
		&i18n,
	); err != nil {
		return taxonomy.Genre{}, fmt.Errorf("resolve genre name: %w", err)
	}
	if i18n.GenreID != 0 {
		g.Name = i18n.Name
		g.Language = i18n.Language
	}
	return g, nil
}

// ListByIDsWithFallback returns one Genre per requested id, applying
// the §5.6 two-tier fallback (requested language first, en-US second)
// in a bounded number of round-trips (at most three SELECTs total:
// genres-by-id, genres_i18n requested-lang, genres_i18n en-US fill-in;
// the en-US pass is skipped when lang == en-US).
//
// Story 552 (E-1 Z3) — replaces the per-genre Get loop in the
// seriesdetail composer (composer.go loadTaxonomy genres branch).
//
// Semantics:
//   - Empty ids slice            → nil slice, nil error (no SQL issued).
//   - Genre with row in `lang`   → entry uses that name + language.
//   - Genre with no `lang` row
//     but en-US                  → entry uses en-US name + language.
//   - Genre with no i18n rows    → entry returned with empty Name +
//     empty Language (mirrors per-id Get
//     behaviour: the row is real, the i18n
//     side is just empty — never returned
//     as ErrNotFound).
//   - Genre id absent from
//     `genres` table             → entry NOT included in the result
//     (callers mirror the prior `gerr ==
//     nil` skip).
//
// Result is ordered by id ASC. Callers needing input-order projection
// build a `map[int64]Genre` locally and iterate the original ids slice
// (composer pattern — same shape as loadTopCast).
//
// The third §5.6 tier ("first available row by language ASC") is NOT
// applied on this batch path — see story-552 design notes. Per-id Get
// retains the full three-tier behaviour for callers that need it.
func (r *GenresRepository) ListByIDsWithFallback(
	ctx context.Context,
	ids []int64,
	language string,
) ([]taxonomy.Genre, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	if language == "" {
		language = fallbackLanguage
	}

	// 1. Parent rows.
	var parents []database.GenreModel
	if err := dbFromContext(ctx, r.db).WithContext(ctx).
		Where("id IN ?", ids).
		Order("id ASC").
		Find(&parents).Error; err != nil {
		return nil, fmt.Errorf("list genres by ids: %w", err)
	}
	if len(parents) == 0 {
		return nil, nil
	}

	parentIDs := make([]int64, 0, len(parents))
	for _, p := range parents {
		parentIDs = append(parentIDs, p.ID)
	}

	// 2. Requested-language i18n rows (one round-trip).
	var primary []database.GenreI18nModel
	if err := dbFromContext(ctx, r.db).WithContext(ctx).
		Where("genre_id IN ? AND language = ?", parentIDs, language).
		Find(&primary).Error; err != nil {
		return nil, fmt.Errorf("list genres_i18n by ids (lang=%s): %w", language, err)
	}
	i18nByID := make(map[int64]database.GenreI18nModel, len(primary))
	for _, m := range primary {
		i18nByID[m.GenreID] = m
	}

	// 3. en-US fill-in (skipped when lang IS en-US).
	if language != fallbackLanguage {
		remaining := make([]int64, 0, len(parentIDs))
		for _, id := range parentIDs {
			if _, ok := i18nByID[id]; !ok {
				remaining = append(remaining, id)
			}
		}
		if len(remaining) > 0 {
			var fallback []database.GenreI18nModel
			if err := dbFromContext(ctx, r.db).WithContext(ctx).
				Where("genre_id IN ? AND language = ?", remaining, fallbackLanguage).
				Find(&fallback).Error; err != nil {
				return nil, fmt.Errorf("list genres_i18n en-US fallback: %w", err)
			}
			for _, m := range fallback {
				i18nByID[m.GenreID] = m
			}
		}
	}

	// 4. Merge in id-ASC order.
	out := make([]taxonomy.Genre, 0, len(parents))
	for _, p := range parents {
		g := toGenre(p)
		if i, ok := i18nByID[p.ID]; ok {
			g.Name = i.Name
			g.Language = i.Language
		}
		out = append(out, g)
	}
	return out, nil
}

// GetByTMDBID looks up the genre by TMDB id. Name is NOT resolved
// here — hot-path used by the series enrichment worker to answer "do
// I already have this genre id?".
func (r *GenresRepository) GetByTMDBID(ctx context.Context, tmdbID domain.TMDBID) (taxonomy.Genre, error) {
	var m database.GenreModel
	err := dbFromContext(ctx, r.db).WithContext(ctx).
		Where("tmdb_id = ?", tmdbID).First(&m).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return taxonomy.Genre{}, ports.ErrNotFound
		}
		return taxonomy.Genre{}, fmt.Errorf("get genre by tmdb_id: %w", err)
	}
	return toGenre(m), nil
}

// ResolveByName implements the PRD §5.4 Sonarr-genre fallback. Maps
// a genre string (case-sensitive, exact match) to the canonical
// genres.id by querying the genres_i18n_name index. Returns
// ports.ErrNotFound when no row matches.
//
// Case sensitivity: TMDB and Sonarr both emit "Drama" (capitalised
// first letter) for the 16 TMDB TV genres in v1, so case-sensitive
// match is sufficient. If a future source emits other casings, the
// index would be re-created on LOWER(name) and the comparison
// rewritten — not a v1 concern.
func (r *GenresRepository) ResolveByName(ctx context.Context, language, name string) (int64, error) {
	if language == "" {
		return 0, fmt.Errorf("resolve genre by name: language must be non-empty")
	}
	if name == "" {
		return 0, fmt.Errorf("resolve genre by name: name must be non-empty")
	}
	var m database.GenreI18nModel
	err := dbFromContext(ctx, r.db).WithContext(ctx).
		Where("language = ? AND name = ?", language, name).
		First(&m).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return 0, ports.ErrNotFound
		}
		return 0, fmt.Errorf("resolve genre by name: %w", err)
	}
	return m.GenreID, nil
}

// Upsert inserts or updates by natural key (tmdb_id). Idempotent.
func (r *GenresRepository) Upsert(ctx context.Context, g taxonomy.Genre) (int64, error) {
	now := time.Now().UTC()
	if g.CreatedAt.IsZero() {
		g.CreatedAt = now
	}
	g.UpdatedAt = now
	m := database.GenreModel{
		ID:        g.ID,
		TMDBID:    g.TMDBID,
		CreatedAt: g.CreatedAt,
		UpdatedAt: g.UpdatedAt,
	}

	db := dbFromContext(ctx, r.db).WithContext(ctx)
	// No PK + no natural key ⇒ pure INSERT, no ON CONFLICT clause.
	// Previously this branch emitted `clause.OnConflict{DoNothing:
	// false}` which serialized to a bare `ON CONFLICT DO UPDATE`;
	// SQLite tolerates the empty target, Postgres rejects it with
	// SQLSTATE 42601 ("requires inference specification or constraint
	// name"). Story 424a dual-backend migration caught this.
	switch {
	case m.ID != 0:
		conflict := clause.OnConflict{
			Columns:   []clause.Column{{Name: "id"}},
			DoUpdates: clause.AssignmentColumns([]string{"tmdb_id", "updated_at"}),
		}
		if err := db.Clauses(conflict).Create(&m).Error; err != nil {
			return 0, fmt.Errorf("upsert genre: %w", err)
		}
	case m.TMDBID != nil:
		conflict := clause.OnConflict{
			Columns:     []clause.Column{{Name: "tmdb_id"}},
			TargetWhere: clause.Where{Exprs: []clause.Expression{clause.Expr{SQL: "tmdb_id IS NOT NULL"}}},
			DoUpdates:   clause.AssignmentColumns([]string{"tmdb_id", "updated_at"}),
		}
		if err := db.Clauses(conflict).Create(&m).Error; err != nil {
			return 0, fmt.Errorf("upsert genre: %w", err)
		}
	default:
		// No PK and no natural key — pure insert. GORM assigns id.
		if err := db.Create(&m).Error; err != nil {
			return 0, fmt.Errorf("upsert genre: %w", err)
		}
	}
	return m.ID, nil
}

// Set replaces the full series_genres set for seriesID. Same
// semantics as NetworksRepository.Set.
func (r *GenresRepository) Set(ctx context.Context, seriesID domain.SeriesID, genreIDs []int64) error {
	if seriesID == 0 {
		return fmt.Errorf("set series_genres: series_id must be non-zero")
	}
	db := dbFromContext(ctx, r.db).WithContext(ctx)
	return db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("series_id = ?", seriesID).
			Delete(&database.SeriesGenreModel{}).Error; err != nil {
			return fmt.Errorf("set series_genres: clear: %w", err)
		}
		if len(genreIDs) == 0 {
			return nil
		}
		genreIDs = dedupInt64Preserve(genreIDs)
		rows := make([]database.SeriesGenreModel, 0, len(genreIDs))
		for i, gid := range genreIDs {
			pos := i
			rows = append(rows, database.SeriesGenreModel{
				SeriesID: seriesID,
				GenreID:  gid,
				Position: &pos,
			})
		}
		if err := tx.Create(&rows).Error; err != nil {
			return fmt.Errorf("set series_genres: insert: %w", err)
		}
		return nil
	})
}

// ListBySeries returns the genre ids for the given series in
// position-ascending order.
func (r *GenresRepository) ListBySeries(ctx context.Context, seriesID domain.SeriesID) ([]int64, error) {
	var rows []database.SeriesGenreModel
	err := dbFromContext(ctx, r.db).WithContext(ctx).
		Where("series_id = ?", seriesID).
		Order("position ASC, genre_id ASC").
		Find(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("list series_genres: %w", err)
	}
	out := make([]int64, 0, len(rows))
	for _, row := range rows {
		out = append(out, row.GenreID)
	}
	return out, nil
}

func toGenre(m database.GenreModel) taxonomy.Genre {
	return taxonomy.Genre{
		ID:        m.ID,
		TMDBID:    m.TMDBID,
		CreatedAt: m.CreatedAt,
		UpdatedAt: m.UpdatedAt,
	}
}

// GenresI18nRepository persists the localised name rows for a genre.
// Mirrors SeriesTextsRepository / PersonBiographiesRepository — same
// PK shape, same fallback semantics, same shared helper.
type GenresI18nRepository struct {
	db *gorm.DB
}

func NewGenresI18nRepository(db *gorm.DB) *GenresI18nRepository {
	return &GenresI18nRepository{db: db}
}

// Get fetches the row for (genre_id, language) exactly.
func (r *GenresI18nRepository) Get(ctx context.Context, genreID int64, language string) (taxonomy.GenreI18n, error) {
	var m database.GenreI18nModel
	err := dbFromContext(ctx, r.db).WithContext(ctx).
		Where("genre_id = ? AND language = ?", genreID, language).
		First(&m).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return taxonomy.GenreI18n{}, ports.ErrNotFound
		}
		return taxonomy.GenreI18n{}, fmt.Errorf("get genres_i18n: %w", err)
	}
	return taxonomy.GenreI18n{
		GenreID:   m.GenreID,
		Language:  m.Language,
		Name:      m.Name,
		UpdatedAt: m.UpdatedAt,
	}, nil
}

// GetWithFallback returns the row for the requested language, or
// the en-US fallback, or the first available row by language
// ascending.
func (r *GenresI18nRepository) GetWithFallback(ctx context.Context, genreID int64, language string) (taxonomy.GenreI18n, error) {
	var m database.GenreI18nModel
	if err := pickLanguageFallback(ctx, r.db, "genres_i18n", "genre_id", genreID, language, &m); err != nil {
		return taxonomy.GenreI18n{}, err
	}
	if m.GenreID == 0 {
		return taxonomy.GenreI18n{}, ports.ErrNotFound
	}
	return taxonomy.GenreI18n{
		GenreID:   m.GenreID,
		Language:  m.Language,
		Name:      m.Name,
		UpdatedAt: m.UpdatedAt,
	}, nil
}

// Upsert writes a localised name row by composite PK. Idempotent.
func (r *GenresI18nRepository) Upsert(ctx context.Context, t taxonomy.GenreI18n) error {
	if t.GenreID == 0 {
		return fmt.Errorf("upsert genres_i18n: genre_id must be non-zero")
	}
	if t.Language == "" {
		return fmt.Errorf("upsert genres_i18n: language must be non-empty")
	}
	if t.Name == "" {
		return fmt.Errorf("upsert genres_i18n: name must be non-empty")
	}
	t.UpdatedAt = time.Now().UTC()
	m := database.GenreI18nModel{
		GenreID:   t.GenreID,
		Language:  t.Language,
		Name:      t.Name,
		UpdatedAt: t.UpdatedAt,
	}
	err := dbFromContext(ctx, r.db).WithContext(ctx).Clauses(clause.OnConflict{
		Columns: []clause.Column{
			{Name: "genre_id"},
			{Name: "language"},
		},
		DoUpdates: clause.AssignmentColumns([]string{"name", "updated_at"}),
	}).Create(&m).Error
	if err != nil {
		return fmt.Errorf("upsert genres_i18n: %w", err)
	}
	return nil
}
