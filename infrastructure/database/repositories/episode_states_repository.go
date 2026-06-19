package repositories

import (
	"context"
	"errors"
	"fmt"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/alexmorbo/seasonfill/domain/series"
	"github.com/alexmorbo/seasonfill/infrastructure/database"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
	sharedErrors "github.com/alexmorbo/seasonfill/internal/shared/errors"
)

// EpisodeStatesRepository persists per-instance file state for
// canonical episodes. PK (instance_name, episode_id); writes always
// originate from a Sonarr sync path (§5.4).
type EpisodeStatesRepository struct {
	db *gorm.DB
}

func NewEpisodeStatesRepository(db *gorm.DB) *EpisodeStatesRepository {
	return &EpisodeStatesRepository{db: db}
}

// Get returns the per-instance state for a canonical episode. Missing
// row → typed EpisodeNotFoundError; F-2c-3 dropped the legacy
// errors.Join(typed, ports.ErrNotFound) shim. The method has no
// external callers; tests use errors.As to assert the typed sentinel.
func (r *EpisodeStatesRepository) Get(ctx context.Context, instanceName domain.InstanceName, episodeID domain.EpisodeID) (series.EpisodeState, error) {
	var m database.EpisodeStateModel
	err := dbFromContext(ctx, r.db).WithContext(ctx).
		Where("instance_name = ? AND episode_id = ? AND deleted_at IS NULL", instanceName, episodeID).
		First(&m).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return series.EpisodeState{}, &sharedErrors.EpisodeNotFoundError{ID: episodeID}
		}
		return series.EpisodeState{}, fmt.Errorf("get episode_state: %w", err)
	}
	return toEpisodeState(m), nil
}

// ListBySeries returns every episode_state row for the given instance
// whose episode belongs to seriesID. JOINs against `episodes` to walk
// only the series's slice rather than scanning the whole per-instance
// state table.
func (r *EpisodeStatesRepository) ListBySeries(ctx context.Context, instanceName domain.InstanceName, seriesID domain.SeriesID) ([]series.EpisodeState, error) {
	var models []database.EpisodeStateModel
	err := dbFromContext(ctx, r.db).WithContext(ctx).
		Model(&database.EpisodeStateModel{}).
		Joins("JOIN episodes ON episodes.id = episode_states.episode_id").
		Where("episode_states.instance_name = ? AND episodes.series_id = ? AND episode_states.deleted_at IS NULL", instanceName, seriesID).
		Find(&models).Error
	if err != nil {
		return nil, fmt.Errorf("list episode_states by series: %w", err)
	}
	out := make([]series.EpisodeState, 0, len(models))
	for _, m := range models {
		out = append(out, toEpisodeState(m))
	}
	return out, nil
}

// Upsert writes per-instance state by composite PK. Idempotent.
func (r *EpisodeStatesRepository) Upsert(ctx context.Context, s series.EpisodeState) error {
	if s.InstanceName == "" {
		return fmt.Errorf("upsert episode_state: instance_name must be non-empty")
	}
	if s.EpisodeID == 0 {
		return fmt.Errorf("upsert episode_state: episode_id must be non-zero")
	}
	s.UpdatedAt = time.Now().UTC()
	m := fromEpisodeState(s)
	err := dbFromContext(ctx, r.db).WithContext(ctx).Clauses(clause.OnConflict{
		Columns: []clause.Column{
			{Name: "instance_name"},
			{Name: "episode_id"},
		},
		DoUpdates: clause.AssignmentColumns([]string{
			"monitored", "has_file",
			"episode_file_id", "quality", "size_bytes",
			"video_codec", "audio_codec", "audio_channels", "release_group",
			"updated_at", "deleted_at",
		}),
	}).Create(&m).Error
	if err != nil {
		return fmt.Errorf("upsert episode_state: %w", err)
	}
	return nil
}

// SoftDeleteBySeries sets deleted_at on every episode_states row for
// the (instance_name, series_id) pair, scoped via the episodes JOIN —
// the per-instance state table doesn't carry series_id natively
// (PK is (instance_name, episode_id)). Returns the affected-row count.
//
// Story 218 (E-2). episode_states.deleted_at is added by migration
// 000034 (paired with this story).
func (r *EpisodeStatesRepository) SoftDeleteBySeries(
	ctx context.Context, instanceName domain.InstanceName, sonarrSeriesID domain.SonarrSeriesID,
) (int, error) {
	if instanceName == "" {
		return 0, fmt.Errorf("soft delete episode_states by series: instance_name must be non-empty")
	}
	now := time.Now().UTC()
	res := dbFromContext(ctx, r.db).WithContext(ctx).
		Table("episode_states").
		Where(`instance_name = ?
		   AND episode_id IN (
		       SELECT e.id FROM episodes e
		       JOIN series_cache sc ON sc.series_id = e.series_id
		       WHERE sc.instance_name = ? AND sc.sonarr_series_id = ?
		   )`,
			instanceName, instanceName, sonarrSeriesID).
		Updates(map[string]any{
			"deleted_at": now,
			"updated_at": now,
		})
	if res.Error != nil {
		return 0, fmt.Errorf("soft delete episode_states by series: %w", res.Error)
	}
	return int(res.RowsAffected), nil
}

func toEpisodeState(m database.EpisodeStateModel) series.EpisodeState {
	return series.EpisodeState{
		InstanceName:  m.InstanceName,
		EpisodeID:     m.EpisodeID,
		Monitored:     m.Monitored,
		HasFile:       m.HasFile,
		EpisodeFileID: m.EpisodeFileID,
		Quality:       m.Quality,
		SizeBytes:     m.SizeBytes,
		VideoCodec:    m.VideoCodec,
		AudioCodec:    m.AudioCodec,
		AudioChannels: m.AudioChannels,
		ReleaseGroup:  m.ReleaseGroup,
		UpdatedAt:     m.UpdatedAt,
	}
}

func fromEpisodeState(s series.EpisodeState) database.EpisodeStateModel {
	return database.EpisodeStateModel{
		InstanceName:  s.InstanceName,
		EpisodeID:     s.EpisodeID,
		Monitored:     s.Monitored,
		HasFile:       s.HasFile,
		EpisodeFileID: s.EpisodeFileID,
		Quality:       s.Quality,
		SizeBytes:     s.SizeBytes,
		VideoCodec:    s.VideoCodec,
		AudioCodec:    s.AudioCodec,
		AudioChannels: s.AudioChannels,
		ReleaseGroup:  s.ReleaseGroup,
		UpdatedAt:     s.UpdatedAt,
	}
}
