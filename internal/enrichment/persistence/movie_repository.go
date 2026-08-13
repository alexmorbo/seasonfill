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

	"github.com/alexmorbo/seasonfill/internal/catalog/domain/movie"
	ports "github.com/alexmorbo/seasonfill/internal/shared/dataports"
	database "github.com/alexmorbo/seasonfill/internal/shared/db"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
	sharedErrors "github.com/alexmorbo/seasonfill/internal/shared/errors"
)

// MovieRepository persists the canonical `movies` table (Ф6-R-3, ADR-0018
// §6b). Upsert is idempotent and COALESCE-guards every TMDB/OMDb enrichment
// column against a Radarr-driven write that carries nil (the "два писателя"
// invariant, [[project_seasonfill_upsert_coalesce_pattern]] — the movie
// analog of seriesUpsertAssignments).
type MovieRepository struct {
	db *gorm.DB
}

func NewMovieRepository(db *gorm.DB) *MovieRepository { return &MovieRepository{db: db} }

// Get by PK. ports.ErrNotFound on miss.
func (r *MovieRepository) Get(ctx context.Context, id domain.MovieID) (movie.Canon, error) {
	var m database.MovieModel
	err := dbFromContext(ctx, r.db).WithContext(ctx).Where("id = ?", id).First(&m).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return movie.Canon{}, errors.Join(&sharedErrors.MovieNotFoundError{ID: id}, ports.ErrNotFound)
		}
		return movie.Canon{}, fmt.Errorf("get movie: %w", err)
	}
	return movieToCanon(m), nil
}

// GetByTMDBID resolves via the partial-unique movies_tmdb_id_idx.
// ports.ErrNotFound on miss.
func (r *MovieRepository) GetByTMDBID(ctx context.Context, tmdbID domain.TMDBID) (movie.Canon, error) {
	var m database.MovieModel
	err := dbFromContext(ctx, r.db).WithContext(ctx).Where("tmdb_id = ?", tmdbID).First(&m).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return movie.Canon{}, errors.Join(&sharedErrors.MovieNotFoundError{}, ports.ErrNotFound)
		}
		return movie.Canon{}, fmt.Errorf("get movie by tmdb_id: %w", err)
	}
	return movieToCanon(m), nil
}

// ListByTMDBIDs returns canon rows for the given TMDB ids (zeros filtered).
// tmdb_id-ascending; missing ids dropped; empty → (nil, nil). Mirrors
// SeriesRepository.ListByTMDBIDs — powers the person_credits movie-canon
// linkage (F-20, §L3-3).
func (r *MovieRepository) ListByTMDBIDs(ctx context.Context, tmdbIDs []domain.TMDBID) ([]movie.Canon, error) {
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
	var models []database.MovieModel
	err := dbFromContext(ctx, r.db).WithContext(ctx).
		Where("tmdb_id IN ?", bound).Order("tmdb_id ASC").Find(&models).Error
	if err != nil {
		return nil, fmt.Errorf("list movies by tmdb_ids: %w", err)
	}
	out := make([]movie.Canon, 0, len(models))
	for _, m := range models {
		out = append(out, movieToCanon(m))
	}
	return out, nil
}

// Upsert inserts or updates the canon row. id!=0 ⇒ conflict on PK; else
// conflict on the partial-unique tmdb_id; else pure insert. Returns the id.
func (r *MovieRepository) Upsert(ctx context.Context, c movie.Canon) (domain.MovieID, error) {
	now := time.Now().UTC()
	if c.CreatedAt.IsZero() {
		c.CreatedAt = now
	}
	c.UpdatedAt = now
	if c.Hydration == "" {
		c.Hydration = movie.HydrationStub
	}
	if !c.Hydration.IsValid() {
		return 0, fmt.Errorf("upsert movie: invalid hydration %q", c.Hydration)
	}
	m := movieFromCanon(c)
	db := dbFromContext(ctx, r.db).WithContext(ctx)
	switch {
	case m.ID != 0:
		conflict := clause.OnConflict{
			Columns:   []clause.Column{{Name: "id"}},
			DoUpdates: clause.Assignments(movieUpsertAssignments()),
		}
		if err := db.Clauses(conflict).Create(&m).Error; err != nil {
			return 0, fmt.Errorf("upsert movie: %w", err)
		}
	case m.TMDBID != nil:
		// Partial unique index on tmdb_id WHERE tmdb_id IS NOT NULL —
		// SQLite + Postgres both require the index predicate repeated in
		// the ON CONFLICT target so the planner picks the partial index.
		conflict := clause.OnConflict{
			Columns:     []clause.Column{{Name: "tmdb_id"}},
			TargetWhere: clause.Where{Exprs: []clause.Expression{clause.Expr{SQL: "tmdb_id IS NOT NULL"}}},
			DoUpdates:   clause.Assignments(movieUpsertAssignments()),
		}
		if err := db.Clauses(conflict).Create(&m).Error; err != nil {
			return 0, fmt.Errorf("upsert movie: %w", err)
		}
	default:
		if err := db.Create(&m).Error; err != nil {
			return 0, fmt.Errorf("upsert movie: %w", err)
		}
	}
	return m.ID, nil
}

// UpsertStub inserts/updates a recommendation stub by the tmdb_id partial-unique (Ф1.1c). A
// thin stub must NEVER blank a richer previously-hydrated value, so movieStubUpsertAssignments
// is existing-wins COALESCE(movies.X, excluded.X) — the INVERSE polarity of the full-hydrate
// Upsert (movieUpsertAssignments = excluded-wins). Hydration is full-sticky: a 'full' movie is
// never downgraded to 'stub'. Mirror of SeriesRepository.UpsertStub. Called inside the recs
// tx BEFORE the movie_recommendations join insert so the recommended_movie_id FK is satisfied.
func (r *MovieRepository) UpsertStub(ctx context.Context, c movie.Canon) (domain.MovieID, error) {
	if c.TMDBID == nil {
		return 0, fmt.Errorf("upsert stub movie: tmdb_id required")
	}
	now := time.Now().UTC()
	if c.CreatedAt.IsZero() {
		c.CreatedAt = now
	}
	c.UpdatedAt = now
	if c.Hydration == "" {
		c.Hydration = movie.HydrationStub
	}
	if !c.Hydration.IsValid() {
		return 0, fmt.Errorf("upsert stub movie: invalid hydration %q", c.Hydration)
	}
	m := movieFromCanon(c)
	db := dbFromContext(ctx, r.db).WithContext(ctx)
	conflict := clause.OnConflict{
		Columns:     []clause.Column{{Name: "tmdb_id"}},
		TargetWhere: clause.Where{Exprs: []clause.Expression{clause.Expr{SQL: "tmdb_id IS NOT NULL"}}},
		DoUpdates:   clause.Assignments(movieStubUpsertAssignments()),
	}
	if err := db.Clauses(conflict).Create(&m).Error; err != nil {
		return 0, fmt.Errorf("upsert stub movie: %w", err)
	}
	return m.ID, nil
}

// movieStubUpsertAssignments — existing-row-wins COALESCE(movies.X, excluded.X). A thin
// recommendation stub may carry fewer / older values than a previously-hydrated row, so the
// EXISTING value wins and hydration is 'full'-sticky (CASE). Inverse polarity of
// movieUpsertAssignments. Title uses NULLIF so a prior stub's empty title can still be filled
// by the incoming stub, but a non-empty existing title always wins. Mirror of the
// SeriesRepository.UpsertStub assignment map.
func movieStubUpsertAssignments() map[string]any {
	return map[string]any{
		"imdb_id":           gorm.Expr("COALESCE(movies.imdb_id, excluded.imdb_id)"),
		"hydration":         gorm.Expr("CASE WHEN movies.hydration = 'full' THEN movies.hydration ELSE excluded.hydration END"),
		"title":             gorm.Expr("COALESCE(NULLIF(movies.title, ''), NULLIF(excluded.title, ''), movies.title)"),
		"original_title":    gorm.Expr("COALESCE(movies.original_title, excluded.original_title)"),
		"status":            gorm.Expr("COALESCE(movies.status, excluded.status)"),
		"release_date":      gorm.Expr("COALESCE(movies.release_date, excluded.release_date)"),
		"year":              gorm.Expr("COALESCE(movies.year, excluded.year)"),
		"runtime_minutes":   gorm.Expr("COALESCE(movies.runtime_minutes, excluded.runtime_minutes)"),
		"original_language": gorm.Expr("COALESCE(movies.original_language, excluded.original_language)"),
		"popularity":        gorm.Expr("COALESCE(movies.popularity, excluded.popularity)"),
		"poster_asset":      gorm.Expr("COALESCE(movies.poster_asset, excluded.poster_asset)"),
		"backdrop_asset":    gorm.Expr("COALESCE(movies.backdrop_asset, excluded.backdrop_asset)"),
		"tmdb_rating":       gorm.Expr("COALESCE(movies.tmdb_rating, excluded.tmdb_rating)"),
		"tmdb_votes":        gorm.Expr("COALESCE(movies.tmdb_votes, excluded.tmdb_votes)"),
		"updated_at":        gorm.Expr("excluded.updated_at"),
	}
}

// MarkTMDBSynced stamps movies.enrichment_tmdb_synced_at = now. Single-column
// stamp; mirrors SeriesRepository.MarkTMDBSynced.
func (r *MovieRepository) MarkTMDBSynced(ctx context.Context, id domain.MovieID, now time.Time) error {
	if id == 0 {
		return fmt.Errorf("mark movie tmdb synced: movie_id must be non-zero")
	}
	err := dbFromContext(ctx, r.db).WithContext(ctx).Table("movies").Where("id = ?", id).
		Updates(map[string]any{"enrichment_tmdb_synced_at": now.UTC(), "updated_at": now.UTC()}).Error
	if err != nil {
		return fmt.Errorf("mark movie tmdb synced: %w", err)
	}
	return nil
}

// MarkOMDBSynced stamps movies.enrichment_omdb_synced_at = now. Same shape
// as MarkTMDBSynced.
func (r *MovieRepository) MarkOMDBSynced(ctx context.Context, id domain.MovieID, now time.Time) error {
	if id == 0 {
		return fmt.Errorf("mark movie omdb synced: movie_id must be non-zero")
	}
	err := dbFromContext(ctx, r.db).WithContext(ctx).Table("movies").Where("id = ?", id).
		Updates(map[string]any{"enrichment_omdb_synced_at": now.UTC(), "updated_at": now.UTC()}).Error
	if err != nil {
		return fmt.Errorf("mark movie omdb synced: %w", err)
	}
	return nil
}

// MarkCastSynced stamps movies.enrichment_cast_synced_at = now. Single-column stamp
// (Ф1.1a); mirrors MarkTMDBSynced. Called by MovieWorker.writeCast inside the cast
// tx so the clock commits atomically with the person_credits rows.
func (r *MovieRepository) MarkCastSynced(ctx context.Context, id domain.MovieID, now time.Time) error {
	if id == 0 {
		return fmt.Errorf("mark movie cast synced: movie_id must be non-zero")
	}
	err := dbFromContext(ctx, r.db).WithContext(ctx).Table("movies").Where("id = ?", id).
		Updates(map[string]any{"enrichment_cast_synced_at": now.UTC(), "updated_at": now.UTC()}).Error
	if err != nil {
		return fmt.Errorf("mark movie cast synced: %w", err)
	}
	return nil
}

// MarkKeywordsSynced stamps movies.enrichment_keywords_synced_at = now (Ф1.1b). Single-column
// stamp; mirrors MarkCastSynced. Called by MovieWorker.writeKeywords inside the keywords-write
// tx so the clock commits atomically with the movie_keywords rows.
func (r *MovieRepository) MarkKeywordsSynced(ctx context.Context, id domain.MovieID, now time.Time) error {
	if id == 0 {
		return fmt.Errorf("mark movie keywords synced: movie_id must be non-zero")
	}
	err := dbFromContext(ctx, r.db).WithContext(ctx).Table("movies").Where("id = ?", id).
		Updates(map[string]any{"enrichment_keywords_synced_at": now.UTC(), "updated_at": now.UTC()}).Error
	if err != nil {
		return fmt.Errorf("mark movie keywords synced: %w", err)
	}
	return nil
}

// MarkMediaSynced stamps movies.enrichment_media_synced_at = now (Ф1.1c). Single-column stamp;
// mirrors MarkKeywordsSynced. Called inside the media-write tx.
func (r *MovieRepository) MarkMediaSynced(ctx context.Context, id domain.MovieID, now time.Time) error {
	if id == 0 {
		return fmt.Errorf("mark movie media synced: movie_id must be non-zero")
	}
	err := dbFromContext(ctx, r.db).WithContext(ctx).Table("movies").Where("id = ?", id).
		Updates(map[string]any{"enrichment_media_synced_at": now.UTC(), "updated_at": now.UTC()}).Error
	if err != nil {
		return fmt.Errorf("mark movie media synced: %w", err)
	}
	return nil
}

// MarkRecsSynced stamps movies.enrichment_recs_synced_at = now (Ф1.1c). Single-column stamp;
// mirrors MarkKeywordsSynced. Called inside the recs-write tx.
func (r *MovieRepository) MarkRecsSynced(ctx context.Context, id domain.MovieID, now time.Time) error {
	if id == 0 {
		return fmt.Errorf("mark movie recs synced: movie_id must be non-zero")
	}
	err := dbFromContext(ctx, r.db).WithContext(ctx).Table("movies").Where("id = ?", id).
		Updates(map[string]any{"enrichment_recs_synced_at": now.UTC(), "updated_at": now.UTC()}).Error
	if err != nil {
		return fmt.Errorf("mark movie recs synced: %w", err)
	}
	return nil
}

// movieUpsertAssignments — the "два писателя" guard. COALESCE(excluded.X,
// movies.X) on every TMDB/OMDb enrichment column so a Radarr-sync/webhook
// write that carries nil cannot blank a previously enriched value; hydration
// is 'full'-sticky via CASE; origin_countries uses NULLIF('[]') so the
// Radarr-stub sentinel does not overwrite enriched countries; title uses
// NULLIF empty-string so a Radarr-stub empty title does not blank the title.
// Mirror of seriesUpsertAssignments (series_repository.go:1051).
func movieUpsertAssignments() map[string]any {
	return map[string]any{
		"tmdb_id":                   gorm.Expr("excluded.tmdb_id"),
		"imdb_id":                   gorm.Expr("COALESCE(excluded.imdb_id, movies.imdb_id)"),
		"hydration":                 gorm.Expr("CASE WHEN movies.hydration = 'full' THEN 'full' WHEN excluded.hydration = 'full' THEN 'full' ELSE excluded.hydration END"),
		"original_title":            gorm.Expr("COALESCE(excluded.original_title, movies.original_title)"),
		"status":                    gorm.Expr("COALESCE(excluded.status, movies.status)"),
		"release_date":              gorm.Expr("COALESCE(excluded.release_date, movies.release_date)"),
		"digital_release_date":      gorm.Expr("COALESCE(excluded.digital_release_date, movies.digital_release_date)"),
		"physical_release_date":     gorm.Expr("COALESCE(excluded.physical_release_date, movies.physical_release_date)"),
		"year":                      gorm.Expr("COALESCE(excluded.year, movies.year)"),
		"runtime_minutes":           gorm.Expr("COALESCE(excluded.runtime_minutes, movies.runtime_minutes)"),
		"homepage":                  gorm.Expr("COALESCE(excluded.homepage, movies.homepage)"),
		"original_language":         gorm.Expr("COALESCE(excluded.original_language, movies.original_language)"),
		"origin_countries":          gorm.Expr("COALESCE(NULLIF(excluded.origin_countries, '[]'), movies.origin_countries)"),
		"collection_id":             gorm.Expr("COALESCE(excluded.collection_id, movies.collection_id)"),
		"popularity":                gorm.Expr("COALESCE(excluded.popularity, movies.popularity)"),
		"budget":                    gorm.Expr("COALESCE(excluded.budget, movies.budget)"),
		"revenue":                   gorm.Expr("COALESCE(excluded.revenue, movies.revenue)"),
		"poster_asset":              gorm.Expr("COALESCE(excluded.poster_asset, movies.poster_asset)"),
		"backdrop_asset":            gorm.Expr("COALESCE(excluded.backdrop_asset, movies.backdrop_asset)"),
		"tmdb_rating":               gorm.Expr("COALESCE(excluded.tmdb_rating, movies.tmdb_rating)"),
		"tmdb_votes":                gorm.Expr("COALESCE(excluded.tmdb_votes, movies.tmdb_votes)"),
		"imdb_rating":               gorm.Expr("COALESCE(excluded.imdb_rating, movies.imdb_rating)"),
		"imdb_votes":                gorm.Expr("COALESCE(excluded.imdb_votes, movies.imdb_votes)"),
		"omdb_rated":                gorm.Expr("COALESCE(excluded.omdb_rated, movies.omdb_rated)"),
		"omdb_awards":               gorm.Expr("COALESCE(excluded.omdb_awards, movies.omdb_awards)"),
		"enrichment_tmdb_synced_at": gorm.Expr("COALESCE(excluded.enrichment_tmdb_synced_at, movies.enrichment_tmdb_synced_at)"),
		"enrichment_omdb_synced_at": gorm.Expr("COALESCE(excluded.enrichment_omdb_synced_at, movies.enrichment_omdb_synced_at)"),
		"title":                     gorm.Expr("COALESCE(NULLIF(excluded.title, ''), movies.title)"),
		"updated_at":                gorm.Expr("excluded.updated_at"),
	}
}

func movieToCanon(m database.MovieModel) movie.Canon {
	return movie.Canon{
		ID:                         m.ID,
		TMDBID:                     m.TMDBID,
		IMDBID:                     m.IMDBID,
		Hydration:                  movie.Hydration(m.Hydration),
		Title:                      m.Title,
		OriginalTitle:              m.OriginalTitle,
		Status:                     m.Status,
		ReleaseDate:                m.ReleaseDate,
		DigitalReleaseDate:         m.DigitalReleaseDate,
		PhysicalReleaseDate:        m.PhysicalReleaseDate,
		Year:                       m.Year,
		RuntimeMinutes:             m.RuntimeMinutes,
		Homepage:                   m.Homepage,
		OriginalLanguage:           m.OriginalLanguage,
		OriginCountries:            decodeMovieOriginCountries(m.OriginCountries),
		CollectionID:               m.CollectionID,
		Popularity:                 m.Popularity,
		Budget:                     m.Budget,
		Revenue:                    m.Revenue,
		PosterAsset:                m.PosterAsset,
		BackdropAsset:              m.BackdropAsset,
		TMDBRating:                 m.TMDBRating,
		TMDBVotes:                  m.TMDBVotes,
		IMDBRating:                 m.IMDBRating,
		IMDBVotes:                  m.IMDBVotes,
		OMDBRated:                  m.OMDBRated,
		OMDBAwards:                 m.OMDBAwards,
		EnrichmentTMDBSyncedAt:     m.EnrichmentTMDBSyncedAt,
		EnrichmentOMDBSyncedAt:     m.EnrichmentOMDBSyncedAt,
		EnrichmentTextSyncedAt:     m.EnrichmentTextSyncedAt,
		EnrichmentCastSyncedAt:     m.EnrichmentCastSyncedAt,
		EnrichmentRecsSyncedAt:     m.EnrichmentRecsSyncedAt,
		EnrichmentMediaSyncedAt:    m.EnrichmentMediaSyncedAt,
		EnrichmentKeywordsSyncedAt: m.EnrichmentKeywordsSyncedAt,
		TMDBChangedAt:              m.TMDBChangedAt,
		CreatedAt:                  m.CreatedAt,
		UpdatedAt:                  m.UpdatedAt,
	}
}

func movieFromCanon(c movie.Canon) database.MovieModel {
	return database.MovieModel{
		ID:                         c.ID,
		TMDBID:                     c.TMDBID,
		IMDBID:                     c.IMDBID,
		Hydration:                  string(c.Hydration),
		Title:                      c.Title,
		OriginalTitle:              c.OriginalTitle,
		Status:                     c.Status,
		ReleaseDate:                c.ReleaseDate,
		DigitalReleaseDate:         c.DigitalReleaseDate,
		PhysicalReleaseDate:        c.PhysicalReleaseDate,
		Year:                       c.Year,
		RuntimeMinutes:             c.RuntimeMinutes,
		Homepage:                   c.Homepage,
		OriginalLanguage:           c.OriginalLanguage,
		OriginCountries:            encodeMovieOriginCountries(c.OriginCountries),
		CollectionID:               c.CollectionID,
		Popularity:                 c.Popularity,
		Budget:                     c.Budget,
		Revenue:                    c.Revenue,
		PosterAsset:                c.PosterAsset,
		BackdropAsset:              c.BackdropAsset,
		TMDBRating:                 c.TMDBRating,
		TMDBVotes:                  c.TMDBVotes,
		IMDBRating:                 c.IMDBRating,
		IMDBVotes:                  c.IMDBVotes,
		OMDBRated:                  c.OMDBRated,
		OMDBAwards:                 c.OMDBAwards,
		EnrichmentTMDBSyncedAt:     c.EnrichmentTMDBSyncedAt,
		EnrichmentOMDBSyncedAt:     c.EnrichmentOMDBSyncedAt,
		EnrichmentTextSyncedAt:     c.EnrichmentTextSyncedAt,
		EnrichmentCastSyncedAt:     c.EnrichmentCastSyncedAt,
		EnrichmentRecsSyncedAt:     c.EnrichmentRecsSyncedAt,
		EnrichmentMediaSyncedAt:    c.EnrichmentMediaSyncedAt,
		EnrichmentKeywordsSyncedAt: c.EnrichmentKeywordsSyncedAt,
		TMDBChangedAt:              c.TMDBChangedAt,
		CreatedAt:                  c.CreatedAt,
		UpdatedAt:                  c.UpdatedAt,
	}
}

// encodeMovieOriginCountries marshals a string slice to a datatypes.JSON
// (storage column origin_countries text NOT NULL DEFAULT '[]'). nil + empty
// slice both serialize as `[]` so the NOT NULL constraint holds; the
// read-side decodeMovieOriginCountries treats `[]` as nil. Mirror of
// encodeOriginCountries.
func encodeMovieOriginCountries(s []string) datatypes.JSON {
	if len(s) == 0 {
		return datatypes.JSON("[]")
	}
	b, err := json.Marshal(s)
	if err != nil {
		return datatypes.JSON("[]")
	}
	return datatypes.JSON(b)
}

// decodeMovieOriginCountries unmarshals datatypes.JSON to a string slice.
// Returns nil on empty / invalid JSON / empty array; never panics. Mirror
// of decodeOriginCountries.
func decodeMovieOriginCountries(j datatypes.JSON) []string {
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
