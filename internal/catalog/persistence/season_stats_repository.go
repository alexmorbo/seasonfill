package persistence

import (
	"context"
	"fmt"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/alexmorbo/seasonfill/infrastructure/database"
	"github.com/alexmorbo/seasonfill/internal/catalog/domain/series"
	"github.com/alexmorbo/seasonfill/internal/shared/dbtx"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
)

// SeasonStatsRepository persists the per-(instance, sonarr_series_id,
// season_number) projection of Sonarr's seasons[].statistics block.
// Story 377 (B-13).
//
// The repo mirrors EpisodeStatesRepository's soft-delete model: deleted_at
// is set by CascadeSeriesDelete; Upsert always clears it on conflict so a
// later sync resurrects rows that survived a transient SeriesDelete. The
// DoUpdates list includes every counter column AND deleted_at — the same
// lesson story 374 paid for on episode_states.
type SeasonStatsRepository struct {
	db *gorm.DB
}

func NewSeasonStatsRepository(db *gorm.DB) *SeasonStatsRepository {
	return &SeasonStatsRepository{db: db}
}

// Upsert writes one season_stats row keyed on
// (instance_name, sonarr_series_id, season_number). Idempotent.
// updated_at is always stamped server-side; deleted_at is always cleared
// on conflict so soft-deleted rows resurrect on the next sync.
func (r *SeasonStatsRepository) Upsert(ctx context.Context, s series.SeasonStat) error {
	if s.InstanceName == "" {
		return fmt.Errorf("upsert season_stats: instance_name must be non-empty")
	}
	if s.SonarrSeriesID == 0 {
		return fmt.Errorf("upsert season_stats: sonarr_series_id must be non-zero")
	}
	now := time.Now().UTC()
	s.UpdatedAt = now
	s.DeletedAt = nil
	m := fromSeasonStat(s)
	err := dbtx.DBFromContext(ctx, r.db).WithContext(ctx).Clauses(clause.OnConflict{
		Columns: []clause.Column{
			{Name: "instance_name"},
			{Name: "sonarr_series_id"},
			{Name: "season_number"},
		},
		DoUpdates: clause.AssignmentColumns([]string{
			"episode_count",
			"episode_file_count",
			"total_episode_count",
			"aired_episode_count",
			"monitored",
			"size_on_disk_bytes",
			"updated_at",
			"deleted_at",
		}),
	}).Create(&m).Error
	if err != nil {
		return fmt.Errorf("upsert season_stats: %w", err)
	}
	return nil
}

// ListBySeries returns the active per-season stats for one
// (instance, sonarr_series_id), ordered by season_number ASC. Soft-
// deleted rows excluded.
func (r *SeasonStatsRepository) ListBySeries(
	ctx context.Context, instanceName domain.InstanceName, sonarrSeriesID domain.SonarrSeriesID,
) ([]series.SeasonStat, error) {
	if instanceName == "" {
		return nil, fmt.Errorf("list season_stats: instance_name must be non-empty")
	}
	var models []database.SeasonStatModel
	err := dbtx.DBFromContext(ctx, r.db).WithContext(ctx).
		Where("instance_name = ? AND sonarr_series_id = ? AND deleted_at IS NULL",
			instanceName, sonarrSeriesID).
		Order("season_number ASC").
		Find(&models).Error
	if err != nil {
		return nil, fmt.Errorf("list season_stats: %w", err)
	}
	out := make([]series.SeasonStat, 0, len(models))
	for _, m := range models {
		out = append(out, toSeasonStat(m))
	}
	return out, nil
}

// SoftDeleteBySeries sets deleted_at on every season_stats row for
// (instance, sonarr_series_id). Returns the affected-row count. Story
// 377 cascade — invoked by scan.CascadeSeriesDelete alongside the
// series_cache + episode_states stamps.
func (r *SeasonStatsRepository) SoftDeleteBySeries(
	ctx context.Context, instanceName domain.InstanceName, sonarrSeriesID domain.SonarrSeriesID,
) (int, error) {
	if instanceName == "" {
		return 0, fmt.Errorf("soft delete season_stats: instance_name must be non-empty")
	}
	now := time.Now().UTC()
	res := dbtx.DBFromContext(ctx, r.db).WithContext(ctx).
		Model(&database.SeasonStatModel{}).
		Where("instance_name = ? AND sonarr_series_id = ?", instanceName, sonarrSeriesID).
		Updates(map[string]any{
			"deleted_at": now,
			"updated_at": now,
		})
	if res.Error != nil {
		return 0, fmt.Errorf("soft delete season_stats: %w", res.Error)
	}
	return int(res.RowsAffected), nil
}

func toSeasonStat(m database.SeasonStatModel) series.SeasonStat {
	return series.SeasonStat{
		InstanceName:      m.InstanceName,
		SonarrSeriesID:    m.SonarrSeriesID,
		SeasonNumber:      m.SeasonNumber,
		EpisodeCount:      m.EpisodeCount,
		EpisodeFileCount:  m.EpisodeFileCount,
		TotalEpisodeCount: m.TotalEpisodeCount,
		AiredEpisodeCount: m.AiredEpisodeCount,
		Monitored:         m.Monitored,
		SizeOnDiskBytes:   m.SizeOnDiskBytes,
		UpdatedAt:         m.UpdatedAt,
		DeletedAt:         m.DeletedAt,
	}
}

func fromSeasonStat(s series.SeasonStat) database.SeasonStatModel {
	return database.SeasonStatModel{
		InstanceName:      s.InstanceName,
		SonarrSeriesID:    s.SonarrSeriesID,
		SeasonNumber:      s.SeasonNumber,
		EpisodeCount:      s.EpisodeCount,
		EpisodeFileCount:  s.EpisodeFileCount,
		TotalEpisodeCount: s.TotalEpisodeCount,
		AiredEpisodeCount: s.AiredEpisodeCount,
		Monitored:         s.Monitored,
		SizeOnDiskBytes:   s.SizeOnDiskBytes,
		UpdatedAt:         s.UpdatedAt,
		DeletedAt:         s.DeletedAt,
	}
}
