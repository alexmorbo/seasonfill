package repositories

import (
	"context"
	"errors"
	"fmt"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/alexmorbo/seasonfill/application/ports"
	"github.com/alexmorbo/seasonfill/infrastructure/database"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
)

// Video is the read-shape returned by VideosRepository. We carry the
// model row 1:1 — videos is a thin TMDB-projection table with no
// localisation or merge policy at the domain layer, so a value type
// in a domain package would be empty ceremony. The composer uses the
// row directly; the model lives in the infrastructure layer per the
// repo convention.
type Video = database.VideoModel

// VideosRepository persists the `videos` table (PRD §5.3 row
// "videos"). Upsert is idempotent on the natural key tmdb_video_id
// when present; the partial unique on `tmdb_video_id WHERE NOT NULL`
// permits operator-curated rows (rare).
type VideosRepository struct {
	db *gorm.DB
}

func NewVideosRepository(db *gorm.DB) *VideosRepository {
	return &VideosRepository{db: db}
}

// Get fetches by primary key. Returns ports.ErrNotFound on miss.
func (r *VideosRepository) Get(ctx context.Context, id int64) (Video, error) {
	var m database.VideoModel
	err := dbFromContext(ctx, r.db).WithContext(ctx).
		Where("id = ?", id).First(&m).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return Video{}, ports.ErrNotFound
		}
		return Video{}, fmt.Errorf("get video: %w", err)
	}
	return m, nil
}

// ListBySeries returns every video row for seriesID ordered by
// (type ASC, official DESC, published_at DESC NULLS LAST). The
// composite index `videos_series_type` supports the (series_id,
// type, official) prefix.
func (r *VideosRepository) ListBySeries(ctx context.Context, seriesID domain.SeriesID) ([]Video, error) {
	var models []database.VideoModel
	err := dbFromContext(ctx, r.db).WithContext(ctx).
		Where("series_id = ?", seriesID).
		Order("type ASC, official DESC, published_at DESC").
		Find(&models).Error
	if err != nil {
		return nil, fmt.Errorf("list videos: %w", err)
	}
	return models, nil
}

// ListBySeriesAndType returns rows filtered by type
// ("Trailer"/"Teaser"/...). Used by the composer's BestTrailer query
// (PRD §5.6 read-path step "trailer = repo.BestTrailer(s.ID)").
func (r *VideosRepository) ListBySeriesAndType(ctx context.Context, seriesID domain.SeriesID, videoType string) ([]Video, error) {
	if videoType == "" {
		return nil, fmt.Errorf("list videos by type: type must be non-empty")
	}
	var models []database.VideoModel
	err := dbFromContext(ctx, r.db).WithContext(ctx).
		Where("series_id = ? AND type = ?", seriesID, videoType).
		Order("official DESC, published_at DESC").
		Find(&models).Error
	if err != nil {
		return nil, fmt.Errorf("list videos by type: %w", err)
	}
	return models, nil
}

// Upsert inserts or updates by natural key tmdb_video_id when
// present, otherwise by PK (id). Idempotent: a no-op upsert mutates
// only updated_at.
func (r *VideosRepository) Upsert(ctx context.Context, v Video) (int64, error) {
	if v.SeriesID == 0 {
		return 0, fmt.Errorf("upsert video: series_id must be non-zero")
	}
	if v.Name == "" {
		return 0, fmt.Errorf("upsert video: name must be non-empty")
	}
	now := time.Now().UTC()
	if v.CreatedAt.IsZero() {
		v.CreatedAt = now
	}
	v.UpdatedAt = now

	db := dbFromContext(ctx, r.db).WithContext(ctx)
	// No PK + no natural key ⇒ pure INSERT, no ON CONFLICT clause.
	// Previously this branch emitted `clause.OnConflict{DoNothing:
	// false}` which serialized to a bare `ON CONFLICT DO UPDATE`;
	// SQLite tolerates the empty target, Postgres rejects it with
	// SQLSTATE 42601 ("requires inference specification or constraint
	// name"). Story 424a dual-backend migration caught this on the
	// curated-video path (tmdb_video_id NULL — e.g. operator-added).
	switch {
	case v.ID != 0:
		conflict := clause.OnConflict{
			Columns:   []clause.Column{{Name: "id"}},
			DoUpdates: clause.AssignmentColumns(videoUpdateCols()),
		}
		if err := db.Clauses(conflict).Create(&v).Error; err != nil {
			return 0, fmt.Errorf("upsert video: %w", err)
		}
	case v.TMDBVideoID != nil:
		// Partial unique on tmdb_video_id WHERE tmdb_video_id IS NOT NULL
		// — both engines require the index predicate be repeated in the
		// ON CONFLICT target so the planner picks the partial index.
		conflict := clause.OnConflict{
			Columns:     []clause.Column{{Name: "tmdb_video_id"}},
			TargetWhere: clause.Where{Exprs: []clause.Expression{clause.Expr{SQL: "tmdb_video_id IS NOT NULL"}}},
			DoUpdates:   clause.AssignmentColumns(videoUpdateCols()),
		}
		if err := db.Clauses(conflict).Create(&v).Error; err != nil {
			return 0, fmt.Errorf("upsert video: %w", err)
		}
	default:
		// No PK and no natural key — pure insert. GORM assigns id.
		if err := db.Create(&v).Error; err != nil {
			return 0, fmt.Errorf("upsert video: %w", err)
		}
	}
	return v.ID, nil
}

func videoUpdateCols() []string {
	return []string{
		"series_id", "tmdb_video_id", "name", "site", "key",
		"type", "official", "language", "published_at",
		"updated_at",
	}
}
