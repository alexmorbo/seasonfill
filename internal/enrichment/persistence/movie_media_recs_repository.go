package persistence

import (
	"context"
	"errors"
	"fmt"
	"time"

	"gorm.io/gorm"

	"github.com/alexmorbo/seasonfill/internal/catalog/domain/movie"
	ports "github.com/alexmorbo/seasonfill/internal/shared/dataports"
	database "github.com/alexmorbo/seasonfill/internal/shared/db"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
)

// MovieVideosRepository persists the `movie_videos` table (Ф1.1c). Unlike the series
// VideosRepository (which stores every video), the movie card shows a single hero trailer, so
// this repo stores at most one row per movie via ReplaceBestTrailer (authoritative DELETE-all +
// INSERT-one).
type MovieVideosRepository struct {
	db *gorm.DB
}

func NewMovieVideosRepository(db *gorm.DB) *MovieVideosRepository {
	return &MovieVideosRepository{db: db}
}

// ReplaceBestTrailer authoritatively replaces the movie's movie_videos rows with the single
// chosen trailer (Ф1.1c). A nil trailer just clears the movie's rows (the movie has no YouTube
// trailer this refresh). DELETE-all + INSERT-one is idempotent, leaves no stale rows when the
// chosen trailer changes, and satisfies the tmdb_video_id partial-unique at the column level.
// Runs in an inner transaction (a SAVEPOINT when the movie worker's Transactor already opened
// one). name is NOT NULL — a defensive "Trailer" fallback guards a blank TMDB name.
func (r *MovieVideosRepository) ReplaceBestTrailer(ctx context.Context, movieID domain.MovieID, v *movie.Video) error {
	if movieID == 0 {
		return fmt.Errorf("replace movie best trailer: movie_id must be non-zero")
	}
	db := dbFromContext(ctx, r.db).WithContext(ctx)
	now := time.Now().UTC()
	return db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("movie_id = ?", movieID).
			Delete(&database.MovieVideoModel{}).Error; err != nil {
			return fmt.Errorf("replace movie best trailer: clear: %w", err)
		}
		if v == nil {
			return nil
		}
		name := v.Name
		if name == "" {
			name = "Trailer"
		}
		row := database.MovieVideoModel{
			MovieID:     movieID,
			TMDBVideoID: v.TMDBVideoID,
			Name:        name,
			Site:        v.Site,
			Key:         v.Key,
			Type:        v.Type,
			Official:    v.Official,
			Language:    v.Language,
			PublishedAt: v.PublishedAt,
			CreatedAt:   now,
			UpdatedAt:   now,
		}
		if err := tx.Create(&row).Error; err != nil {
			return fmt.Errorf("replace movie best trailer: insert: %w", err)
		}
		return nil
	})
}

// MovieVideo is the read-shape returned by MovieVideosRepository — the model row
// 1:1 (mirror of the series VideosRepository.Video alias). The movie card shows a
// single hero trailer, so reads return at most one row.
type MovieVideo = database.MovieVideoModel

// GetBestTrailer returns the movie's single stored best trailer (Ф2.5b, audit
// F-Ф2-02). ReplaceBestTrailer persists at most one row per movie, so First is
// authoritative; the ORDER BY is defensive against a hypothetical multi-row state
// (official first, then most recent). Returns ports.ErrNotFound when the movie has
// no trailer row (the caller omits the field — fail-open, never a 500).
func (r *MovieVideosRepository) GetBestTrailer(ctx context.Context, movieID domain.MovieID) (MovieVideo, error) {
	var m database.MovieVideoModel
	err := dbFromContext(ctx, r.db).WithContext(ctx).
		Where("movie_id = ?", movieID).
		Order("official DESC, published_at DESC, id ASC").
		First(&m).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return MovieVideo{}, ports.ErrNotFound
		}
		return MovieVideo{}, fmt.Errorf("get movie best trailer: %w", err)
	}
	return m, nil
}

// MovieRecommendationsRepository persists the `movie_recommendations` table (Ф1.1c). Composite
// PK (movie_id, recommended_movie_id); Set replaces the full set for a parent in one tx
// (DELETE+INSERT), position preserved as the input index. Mirror of RecommendationsRepository
// (series). Both join FKs reference movies(id) — the CALLER must ensure the recommended movie
// rows exist (stub upserts in the same enrichment tx) BEFORE calling Set.
type MovieRecommendationsRepository struct {
	db *gorm.DB
}

func NewMovieRecommendationsRepository(db *gorm.DB) *MovieRecommendationsRepository {
	return &MovieRecommendationsRepository{db: db}
}

// ListByMovie returns recommended_movie_ids in position-ASC order (test/read convenience).
func (r *MovieRecommendationsRepository) ListByMovie(ctx context.Context, movieID domain.MovieID) ([]domain.MovieID, error) {
	var rows []database.MovieRecommendationModel
	err := dbFromContext(ctx, r.db).WithContext(ctx).
		Where("movie_id = ?", movieID).
		Order("position ASC, recommended_movie_id ASC").
		Find(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("list movie_recommendations: %w", err)
	}
	out := make([]domain.MovieID, 0, len(rows))
	for _, row := range rows {
		out = append(out, row.RecommendedMovieID)
	}
	return out, nil
}

// Set replaces the full movie_recommendations set for movieID with the given recommended ids,
// in a single transaction. Position is the input index (0-based) so TMDB-ranked ids yield
// TMDB-ranked rows. Empty ids clears the set. A recommended id equal to movieID is rejected
// (defensive; the DB CHECK also forbids it).
func (r *MovieRecommendationsRepository) Set(ctx context.Context, movieID domain.MovieID, recommendedIDs []domain.MovieID) error {
	if movieID == 0 {
		return fmt.Errorf("set movie_recommendations: movie_id must be non-zero")
	}
	db := dbFromContext(ctx, r.db).WithContext(ctx)
	now := time.Now().UTC()
	return db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("movie_id = ?", movieID).
			Delete(&database.MovieRecommendationModel{}).Error; err != nil {
			return fmt.Errorf("set movie_recommendations: clear: %w", err)
		}
		if len(recommendedIDs) == 0 {
			return nil
		}
		rows := make([]database.MovieRecommendationModel, 0, len(recommendedIDs))
		for i, rid := range recommendedIDs {
			if rid == movieID {
				return fmt.Errorf("set movie_recommendations: recommended_movie_id must differ from movie_id")
			}
			pos := i
			rows = append(rows, database.MovieRecommendationModel{
				MovieID:            movieID,
				RecommendedMovieID: rid,
				Position:           &pos,
				UpdatedAt:          now,
			})
		}
		if err := tx.Create(&rows).Error; err != nil {
			return fmt.Errorf("set movie_recommendations: insert: %w", err)
		}
		return nil
	})
}
