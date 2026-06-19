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

// EpisodesRepository persists the canonical `episodes` table. Natural
// key (series_id, season_number, episode_number) — TMDB / Sonarr both
// emit episode lists in batches, so BatchUpsert is the primary write
// path (one INSERT … ON CONFLICT round-trip for N rows).
type EpisodesRepository struct {
	db *gorm.DB
}

func NewEpisodesRepository(db *gorm.DB) *EpisodesRepository {
	return &EpisodesRepository{db: db}
}

// Get returns the canonical episode row by primary key. Missing row
// → typed EpisodeNotFoundError; F-2c-3 dropped the legacy
// errors.Join(typed, ports.ErrNotFound) shim. The method has no
// external callers; tests use errors.As to assert the typed sentinel.
func (r *EpisodesRepository) Get(ctx context.Context, id int64) (series.CanonEpisode, error) {
	var m database.EpisodeModel
	err := dbFromContext(ctx, r.db).WithContext(ctx).
		Where("id = ?", id).First(&m).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return series.CanonEpisode{}, &sharedErrors.EpisodeNotFoundError{ID: domain.EpisodeID(id)}
		}
		return series.CanonEpisode{}, fmt.Errorf("get episode: %w", err)
	}
	return toCanonEpisode(m), nil
}

func (r *EpisodesRepository) ListBySeries(ctx context.Context, seriesID domain.SeriesID) ([]series.CanonEpisode, error) {
	var models []database.EpisodeModel
	err := dbFromContext(ctx, r.db).WithContext(ctx).
		Where("series_id = ?", seriesID).
		Order("season_number ASC, episode_number ASC").
		Find(&models).Error
	if err != nil {
		return nil, fmt.Errorf("list episodes: %w", err)
	}
	out := make([]series.CanonEpisode, 0, len(models))
	for _, m := range models {
		out = append(out, toCanonEpisode(m))
	}
	return out, nil
}

func (r *EpisodesRepository) ListBySeason(ctx context.Context, seriesID domain.SeriesID, seasonNumber int) ([]series.CanonEpisode, error) {
	var models []database.EpisodeModel
	err := dbFromContext(ctx, r.db).WithContext(ctx).
		Where("series_id = ? AND season_number = ?", seriesID, seasonNumber).
		Order("episode_number ASC").
		Find(&models).Error
	if err != nil {
		return nil, fmt.Errorf("list season episodes: %w", err)
	}
	out := make([]series.CanonEpisode, 0, len(models))
	for _, m := range models {
		out = append(out, toCanonEpisode(m))
	}
	return out, nil
}

// CountBySeries returns the count of episodes rows for seriesID.
// Used by the H-1 cast composer (Story 216) as the divisor for
// per-cast Main / Recurring / Guest derivation
// (episode_count / total_episode_count). Indexed via the natural
// key UQ `episodes_natural (series_id, season_number,
// episode_number)` — Postgres + sqlite both pick the leading
// column for the count.
func (r *EpisodesRepository) CountBySeries(ctx context.Context, seriesID domain.SeriesID) (int, error) {
	var n int64
	err := dbFromContext(ctx, r.db).WithContext(ctx).
		Table("episodes").
		Where("series_id = ?", seriesID).
		Count(&n).Error
	if err != nil {
		return 0, fmt.Errorf("count episodes by series: %w", err)
	}
	return int(n), nil
}

// Upsert writes one episode by natural key. Idempotent.
func (r *EpisodesRepository) Upsert(ctx context.Context, e series.CanonEpisode) (int64, error) {
	id, err := r.batchUpsert(ctx, []series.CanonEpisode{e})
	if err != nil {
		return 0, err
	}
	if len(id) != 1 {
		return 0, fmt.Errorf("upsert episode: expected 1 id, got %d", len(id))
	}
	return id[0], nil
}

// BatchUpsert writes N episodes in a single INSERT … ON CONFLICT
// statement (or two on the rare partition: GORM emits one batch per
// round). The returned slice mirrors the input order; index i carries
// the assigned id for input i. Empty input returns empty slice + nil.
func (r *EpisodesRepository) BatchUpsert(ctx context.Context, episodes []series.CanonEpisode) ([]int64, error) {
	return r.batchUpsert(ctx, episodes)
}

func (r *EpisodesRepository) batchUpsert(ctx context.Context, episodes []series.CanonEpisode) ([]int64, error) {
	if len(episodes) == 0 {
		return nil, nil
	}
	now := time.Now().UTC()
	models := make([]database.EpisodeModel, 0, len(episodes))
	for _, e := range episodes {
		if e.SeriesID == 0 {
			return nil, fmt.Errorf("upsert episode: series_id must be non-zero")
		}
		if e.CreatedAt.IsZero() {
			e.CreatedAt = now
		}
		e.UpdatedAt = now
		models = append(models, fromCanonEpisode(e))
	}
	err := dbFromContext(ctx, r.db).WithContext(ctx).Clauses(clause.OnConflict{
		Columns: []clause.Column{
			{Name: "series_id"},
			{Name: "season_number"},
			{Name: "episode_number"},
		},
		DoUpdates: clause.AssignmentColumns([]string{
			"season_id",
			"tmdb_episode_number", "tmdb_episode_id",
			"sonarr_episode_id", "absolute_number",
			"air_date", "runtime_minutes", "finale_type",
			"still_asset", "tmdb_rating", "tmdb_votes",
			"updated_at",
		}),
	}).Create(&models).Error
	if err != nil {
		return nil, fmt.Errorf("batch upsert episodes: %w", err)
	}
	ids := make([]int64, len(models))
	for i, m := range models {
		ids[i] = m.ID
	}
	return ids, nil
}

func toCanonEpisode(m database.EpisodeModel) series.CanonEpisode {
	return series.CanonEpisode{
		ID:                m.ID,
		SeriesID:          m.SeriesID,
		SeasonID:          m.SeasonID,
		SeasonNumber:      m.SeasonNumber,
		EpisodeNumber:     m.EpisodeNumber,
		TMDBEpisodeNumber: m.TMDBEpisodeNumber,
		TMDBEpisodeID:     m.TMDBEpisodeID,
		SonarrEpisodeID:   m.SonarrEpisodeID,
		AbsoluteNumber:    m.AbsoluteNumber,
		AirDate:           m.AirDate,
		RuntimeMinutes:    m.RuntimeMinutes,
		FinaleType:        m.FinaleType,
		StillAsset:        m.StillAsset,
		TMDBRating:        m.TMDBRating,
		TMDBVotes:         m.TMDBVotes,
		CreatedAt:         m.CreatedAt,
		UpdatedAt:         m.UpdatedAt,
	}
}

func fromCanonEpisode(e series.CanonEpisode) database.EpisodeModel {
	return database.EpisodeModel{
		ID:                e.ID,
		SeriesID:          e.SeriesID,
		SeasonID:          e.SeasonID,
		SeasonNumber:      e.SeasonNumber,
		EpisodeNumber:     e.EpisodeNumber,
		TMDBEpisodeNumber: e.TMDBEpisodeNumber,
		TMDBEpisodeID:     e.TMDBEpisodeID,
		SonarrEpisodeID:   e.SonarrEpisodeID,
		AbsoluteNumber:    e.AbsoluteNumber,
		AirDate:           e.AirDate,
		RuntimeMinutes:    e.RuntimeMinutes,
		FinaleType:        e.FinaleType,
		StillAsset:        e.StillAsset,
		TMDBRating:        e.TMDBRating,
		TMDBVotes:         e.TMDBVotes,
		CreatedAt:         e.CreatedAt,
		UpdatedAt:         e.UpdatedAt,
	}
}
