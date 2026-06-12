package repositories

import (
	"context"
	"errors"
	"fmt"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/alexmorbo/seasonfill/application/ports"
	"github.com/alexmorbo/seasonfill/domain/series"
	"github.com/alexmorbo/seasonfill/infrastructure/database"
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
func (r *SeriesRepository) Get(ctx context.Context, id int64) (series.Canon, error) {
	var m database.SeriesModel
	err := dbFromContext(ctx, r.db).WithContext(ctx).
		Where("id = ?", id).First(&m).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return series.Canon{}, ports.ErrNotFound
		}
		return series.Canon{}, fmt.Errorf("get series: %w", err)
	}
	return toCanon(m), nil
}

// GetByTMDBID looks up the canon row by TMDB id. The partial unique
// index (`series_tmdb_id WHERE tmdb_id IS NOT NULL`) guarantees at
// most one row. Returns ports.ErrNotFound on miss.
func (r *SeriesRepository) GetByTMDBID(ctx context.Context, tmdbID int) (series.Canon, error) {
	var m database.SeriesModel
	err := dbFromContext(ctx, r.db).WithContext(ctx).
		Where("tmdb_id = ?", tmdbID).First(&m).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return series.Canon{}, ports.ErrNotFound
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
	tmdbID *int,
	tvdbID *int,
	imdbID *string,
) (series.Canon, error) {
	db := dbFromContext(ctx, r.db).WithContext(ctx)
	probe := func(where string, args ...interface{}) (series.Canon, bool, error) {
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
	return series.Canon{}, ports.ErrNotFound
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
func (r *SeriesRepository) Upsert(ctx context.Context, c series.Canon) (int64, error) {
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
	var conflict clause.OnConflict
	switch {
	case m.ID != 0:
		conflict = clause.OnConflict{
			Columns:   []clause.Column{{Name: "id"}},
			DoUpdates: clause.AssignmentColumns(seriesUpdateCols()),
		}
	case m.TMDBID != nil:
		// Partial unique index on tmdb_id WHERE tmdb_id IS NOT NULL —
		// SQLite + Postgres both require the index predicate to be
		// repeated in the ON CONFLICT target so the planner picks the
		// partial index rather than rejecting "no matching constraint".
		conflict = clause.OnConflict{
			Columns:     []clause.Column{{Name: "tmdb_id"}},
			TargetWhere: clause.Where{Exprs: []clause.Expression{clause.Expr{SQL: "tmdb_id IS NOT NULL"}}},
			DoUpdates:   clause.AssignmentColumns(seriesUpdateCols()),
		}
	default:
		// No PK and no natural key — pure insert. GORM will assign id.
		conflict = clause.OnConflict{DoNothing: false}
	}
	if err := db.Clauses(conflict).Create(&m).Error; err != nil {
		return 0, fmt.Errorf("upsert series: %w", err)
	}
	return m.ID, nil
}

// seriesUpdateCols lists the columns updated on a conflict. id /
// created_at are deliberately excluded so the row's identity and
// insertion timestamp survive the upsert path.
func seriesUpdateCols() []string {
	return []string{
		"tmdb_id", "tvdb_id", "imdb_id",
		"hydration", "title", "original_title", "status",
		"first_air_date", "last_air_date", "next_air_date",
		"year", "runtime_minutes", "homepage",
		"original_language", "origin_country", "popularity",
		"in_production", "poster_asset", "backdrop_asset",
		"tmdb_rating", "tmdb_votes",
		"imdb_rating", "imdb_votes",
		"omdb_rated", "omdb_awards",
		"updated_at",
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
