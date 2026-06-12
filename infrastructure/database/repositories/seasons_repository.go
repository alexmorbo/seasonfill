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

// SeasonsRepository persists the `seasons` table. Natural key is
// (series_id, season_number); Upsert always conflict-targets that
// pair so the canon side is unique per series. ListBySeries returns
// rows in season_number ASC for the composer (§5.6).
type SeasonsRepository struct {
	db *gorm.DB
}

func NewSeasonsRepository(db *gorm.DB) *SeasonsRepository {
	return &SeasonsRepository{db: db}
}

func (r *SeasonsRepository) Get(ctx context.Context, id int64) (series.CanonSeason, error) {
	var m database.SeasonModel
	err := dbFromContext(ctx, r.db).WithContext(ctx).
		Where("id = ?", id).First(&m).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return series.CanonSeason{}, ports.ErrNotFound
		}
		return series.CanonSeason{}, fmt.Errorf("get season: %w", err)
	}
	return toCanonSeason(m), nil
}

func (r *SeasonsRepository) ListBySeries(ctx context.Context, seriesID int64) ([]series.CanonSeason, error) {
	var models []database.SeasonModel
	err := dbFromContext(ctx, r.db).WithContext(ctx).
		Where("series_id = ?", seriesID).
		Order("season_number ASC").
		Find(&models).Error
	if err != nil {
		return nil, fmt.Errorf("list seasons: %w", err)
	}
	out := make([]series.CanonSeason, 0, len(models))
	for _, m := range models {
		out = append(out, toCanonSeason(m))
	}
	return out, nil
}

// Upsert inserts or updates by natural key (series_id, season_number).
// Idempotent: re-running with the same payload mutates only updated_at.
// Returns the assigned id (or the existing id on update).
func (r *SeasonsRepository) Upsert(ctx context.Context, s series.CanonSeason) (int64, error) {
	if s.SeriesID == 0 {
		return 0, fmt.Errorf("upsert season: series_id must be non-zero")
	}
	now := time.Now().UTC()
	if s.CreatedAt.IsZero() {
		s.CreatedAt = now
	}
	s.UpdatedAt = now
	m := fromCanonSeason(s)

	err := dbFromContext(ctx, r.db).WithContext(ctx).Clauses(clause.OnConflict{
		Columns: []clause.Column{
			{Name: "series_id"},
			{Name: "season_number"},
		},
		DoUpdates: clause.AssignmentColumns([]string{
			"tmdb_season_id", "name", "overview",
			"air_date", "episode_count", "poster_asset",
			"updated_at",
		}),
	}).Create(&m).Error
	if err != nil {
		return 0, fmt.Errorf("upsert season: %w", err)
	}
	return m.ID, nil
}

func toCanonSeason(m database.SeasonModel) series.CanonSeason {
	return series.CanonSeason{
		ID:           m.ID,
		SeriesID:     m.SeriesID,
		SeasonNumber: m.SeasonNumber,
		TMDBSeasonID: m.TMDBSeasonID,
		Name:         m.Name,
		Overview:     m.Overview,
		AirDate:      m.AirDate,
		EpisodeCount: m.EpisodeCount,
		PosterAsset:  m.PosterAsset,
		CreatedAt:    m.CreatedAt,
		UpdatedAt:    m.UpdatedAt,
	}
}

func fromCanonSeason(s series.CanonSeason) database.SeasonModel {
	return database.SeasonModel{
		ID:           s.ID,
		SeriesID:     s.SeriesID,
		SeasonNumber: s.SeasonNumber,
		TMDBSeasonID: s.TMDBSeasonID,
		Name:         s.Name,
		Overview:     s.Overview,
		AirDate:      s.AirDate,
		EpisodeCount: s.EpisodeCount,
		PosterAsset:  s.PosterAsset,
		CreatedAt:    s.CreatedAt,
		UpdatedAt:    s.UpdatedAt,
	}
}
