package persistence

import (
	"context"
	"errors"
	"fmt"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/alexmorbo/seasonfill/internal/catalog/domain/movie"
	ports "github.com/alexmorbo/seasonfill/internal/shared/dataports"
	database "github.com/alexmorbo/seasonfill/internal/shared/db"
	"github.com/alexmorbo/seasonfill/internal/shared/dbtx"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
	sharedErrors "github.com/alexmorbo/seasonfill/internal/shared/errors"
)

// MovieStatesRepository persists the per-instance Radarr library-membership
// projection (Ф6-R-4b). Composite PK (instance_name, radarr_movie_id);
// soft-deleted via deleted_at. Rich Upsert / thin UpsertStub mirror
// SeriesCacheRepository.Upsert / UpsertStub — the ONLY difference between the
// two writers is the conflict-update column set, factored into
// upsertWithConflictColumns so the paths can't drift.
type MovieStatesRepository struct{ db *gorm.DB }

func NewMovieStatesRepository(db *gorm.DB) *MovieStatesRepository {
	return &MovieStatesRepository{db: db}
}

var _ ports.MovieStatesRepository = (*MovieStatesRepository)(nil)

// Upsert — RICH writer (radarr-sync). Full conflict-update set.
func (r *MovieStatesRepository) Upsert(ctx context.Context, e movie.StateEntry) error {
	return r.upsertWithConflictColumns(ctx, e, []string{
		"movie_id", "title_slug", "monitored", "has_file", "availability",
		"size_on_disk_bytes", "added_to_radarr", "updated_at", "deleted_at",
	})
}

// UpsertStub — THIN writer (radarr-webhook). STRICT SUBSET of Upsert's set:
// omits availability + size_on_disk_bytes so a stat-less webhook write can't
// zero a real cached stat on an existing row.
func (r *MovieStatesRepository) UpsertStub(ctx context.Context, e movie.StateEntry) error {
	return r.upsertWithConflictColumns(ctx, e, []string{
		"movie_id", "title_slug", "monitored", "has_file",
		"added_to_radarr", "updated_at", "deleted_at",
	})
}

func (r *MovieStatesRepository) upsertWithConflictColumns(ctx context.Context, e movie.StateEntry, updateCols []string) error {
	if e.InstanceName == "" {
		return fmt.Errorf("upsert movie_states: instance_name must be non-empty")
	}
	if e.RadarrMovieID == 0 {
		return fmt.Errorf("upsert movie_states: radarr_movie_id must be non-zero")
	}
	if e.MovieID == 0 {
		return fmt.Errorf("upsert movie_states: movie_id must be non-zero")
	}
	now := time.Now().UTC()
	m := database.MovieStateModel{
		InstanceName:    string(e.InstanceName),
		RadarrMovieID:   e.RadarrMovieID,
		MovieID:         e.MovieID,
		TitleSlug:       e.TitleSlug,
		Monitored:       e.Monitored,
		HasFile:         e.HasFile,
		Availability:    e.Availability,
		SizeOnDiskBytes: e.SizeOnDiskBytes,
		AddedToRadarr:   e.AddedToRadarr,
		UpdatedAt:       now,
		DeletedAt:       nil, // resurrect on write; mirror series_cache
	}
	res := dbtx.DBFromContext(ctx, r.db).WithContext(ctx).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "instance_name"}, {Name: "radarr_movie_id"}},
		DoUpdates: clause.AssignmentColumns(updateCols),
	}).Create(&m)
	if res.Error != nil {
		return fmt.Errorf("upsert movie_states: %w", res.Error)
	}
	return nil
}

// SoftDelete stamps deleted_at on the active row. ports.ErrNotFound when no
// active row matched (idempotent for the webhook caller to swallow).
func (r *MovieStatesRepository) SoftDelete(ctx context.Context, instanceName domain.InstanceName, radarrMovieID int) error {
	if instanceName == "" || radarrMovieID == 0 {
		return fmt.Errorf("soft delete movie_states: instance_name + radarr_movie_id required")
	}
	now := time.Now().UTC()
	res := dbtx.DBFromContext(ctx, r.db).WithContext(ctx).
		Model(&database.MovieStateModel{}).
		Where("instance_name = ? AND radarr_movie_id = ? AND deleted_at IS NULL",
			string(instanceName), radarrMovieID).
		Updates(map[string]any{"deleted_at": now, "updated_at": now})
	if res.Error != nil {
		return fmt.Errorf("soft delete movie_states: %w", res.Error)
	}
	if res.RowsAffected == 0 {
		return errors.Join(&sharedErrors.MovieNotFoundError{}, ports.ErrNotFound)
	}
	return nil
}

// Get returns the row regardless of soft-delete state.
func (r *MovieStatesRepository) Get(ctx context.Context, instanceName domain.InstanceName, radarrMovieID int) (movie.StateEntry, error) {
	var m database.MovieStateModel
	err := dbtx.DBFromContext(ctx, r.db).WithContext(ctx).
		Where("instance_name = ? AND radarr_movie_id = ?", string(instanceName), radarrMovieID).
		First(&m).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return movie.StateEntry{}, errors.Join(&sharedErrors.MovieNotFoundError{}, ports.ErrNotFound)
		}
		return movie.StateEntry{}, fmt.Errorf("get movie_states: %w", err)
	}
	return movieStateToEntry(m), nil
}

// ListActiveByInstance returns non-soft-deleted rows for the instance.
func (r *MovieStatesRepository) ListActiveByInstance(ctx context.Context, instanceName domain.InstanceName) ([]movie.StateEntry, error) {
	var models []database.MovieStateModel
	err := dbtx.DBFromContext(ctx, r.db).WithContext(ctx).
		Where("instance_name = ? AND deleted_at IS NULL", string(instanceName)).
		Order("radarr_movie_id ASC").Find(&models).Error
	if err != nil {
		return nil, fmt.Errorf("list movie_states: %w", err)
	}
	out := make([]movie.StateEntry, 0, len(models))
	for _, m := range models {
		out = append(out, movieStateToEntry(m))
	}
	return out, nil
}

// ListActiveByMovieID returns the ACTIVE per-instance states for one movie id.
// Powers the movie-detail library block (Ф6-R-6a). instance_name ASC deterministic.
func (r *MovieStatesRepository) ListActiveByMovieID(ctx context.Context, movieID domain.MovieID) ([]movie.StateEntry, error) {
	var models []database.MovieStateModel
	err := dbtx.DBFromContext(ctx, r.db).WithContext(ctx).
		Where("movie_id = ? AND deleted_at IS NULL", movieID).
		Order("instance_name ASC").Find(&models).Error
	if err != nil {
		return nil, fmt.Errorf("list movie_states by movie_id: %w", err)
	}
	out := make([]movie.StateEntry, 0, len(models))
	for _, m := range models {
		out = append(out, movieStateToEntry(m))
	}
	return out, nil
}

func movieStateToEntry(m database.MovieStateModel) movie.StateEntry {
	return movie.StateEntry{
		InstanceName:    domain.InstanceName(m.InstanceName),
		RadarrMovieID:   m.RadarrMovieID,
		MovieID:         m.MovieID,
		TitleSlug:       m.TitleSlug,
		Monitored:       m.Monitored,
		HasFile:         m.HasFile,
		Availability:    m.Availability,
		SizeOnDiskBytes: m.SizeOnDiskBytes,
		AddedToRadarr:   m.AddedToRadarr,
		UpdatedAt:       m.UpdatedAt,
		DeletedAt:       m.DeletedAt,
	}
}
