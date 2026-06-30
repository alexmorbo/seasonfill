package persistence

import (
	"context"
	"errors"
	"fmt"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/alexmorbo/seasonfill/internal/catalog/domain/series"
	ports "github.com/alexmorbo/seasonfill/internal/shared/dataports"
	database "github.com/alexmorbo/seasonfill/internal/shared/db"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
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

func (r *SeasonsRepository) ListBySeries(ctx context.Context, seriesID domain.SeriesID) ([]series.CanonSeason, error) {
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

// GetEpisodesSyncedAt reads seasons.episodes_synced_at for one
// (series_id, season_number). Returns (nil, ports.ErrNotFound) when the
// season row is absent. Used by the E-1-A1 Probe to decide whether the
// SeasonSection verdict for this season is fresh.
func (r *SeasonsRepository) GetEpisodesSyncedAt(ctx context.Context, seriesID domain.SeriesID, seasonNumber int) (*time.Time, error) {
	var m database.SeasonModel
	err := dbFromContext(ctx, r.db).WithContext(ctx).
		Select("episodes_synced_at").
		Where("series_id = ? AND season_number = ?", seriesID, seasonNumber).
		First(&m).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ports.ErrNotFound
		}
		return nil, fmt.Errorf("get episodes_synced_at: %w", err)
	}
	return m.EpisodesSyncedAt, nil
}

// CountBySeries returns the row count in `seasons` for the given series_id.
// Used by the Story 533 staleness probe to detect canon rows whose stub
// hydration left the relations empty even when EnrichmentTMDBSyncedAt is
// set (defensive — should be impossible on a clean post-tx state, but the
// probe trusts no invariant). Single index lookup, cheap.
func (r *SeasonsRepository) CountBySeries(ctx context.Context, seriesID domain.SeriesID) (int, error) {
	var n int64
	err := dbFromContext(ctx, r.db).WithContext(ctx).
		Model(&database.SeasonModel{}).
		Where("series_id = ?", seriesID).
		Count(&n).Error
	if err != nil {
		return 0, fmt.Errorf("count seasons: %w", err)
	}
	return int(n), nil
}

// Upsert inserts or updates by natural key (series_id, season_number).
// Idempotent: re-running with the same payload mutates only updated_at.
// Returns the assigned id (or the existing id on update).
//
// E-1 A3a (carry-forward I-2 from A1 review): switched from
// clause.AssignmentColumns([explicit list]) to clause.Assignments(
// seasonsUpsertAssignments()) so that episodes_synced_at — stamped by
// Worker.RefreshSeasonSlim — survives subsequent Sonarr-driven
// Seasons.Upsert (which leaves the column NULL in canonOut). Without
// COALESCE protection every 6h scan would silently drop the stamp
// (Story 552 root cause class — see seriesUpsertAssignments for the
// series table analogue).
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
		DoUpdates: clause.Assignments(seasonsUpsertAssignments()),
	}).Create(&m).Error
	if err != nil {
		return 0, fmt.Errorf("upsert season: %w", err)
	}
	return m.ID, nil
}

// seasonsUpsertAssignments builds the DO UPDATE SET map for Upsert.
// Mirrors seriesUpsertAssignments shape (series_repository.go:737):
// direct excluded.X for TMDB-owned columns + COALESCE for the freshness
// stamp episodes_synced_at, ensuring a Sonarr-driven canonOut (PRD §5.4)
// that leaves episodes_synced_at nil does NOT blank a previously-set
// stamp written by Worker.RefreshSeasonSlim (A3a).
//
// The list mirrors the pre-A3a clause.AssignmentColumns explicit list
// (tmdb_season_id / name / overview / air_date / episode_count /
// poster_asset / updated_at) plus the new episodes_synced_at COALESCE
// entry.
func seasonsUpsertAssignments() map[string]any {
	return map[string]any{
		"tmdb_season_id": gorm.Expr("excluded.tmdb_season_id"),
		"name":           gorm.Expr("excluded.name"),
		"overview":       gorm.Expr("excluded.overview"),
		"air_date":       gorm.Expr("excluded.air_date"),
		"episode_count":  gorm.Expr("excluded.episode_count"),
		"poster_asset":   gorm.Expr("excluded.poster_asset"),
		// E-1 A1 carry-forward I-2 — Worker.RefreshSeasonSlim (A3a) stamps
		// episodes_synced_at via a single-column UPDATE inside its own
		// narrow tx. Without COALESCE the very next Sonarr-driven
		// Seasons.Upsert (6h scan via applyAllForLanguage step 3 — see
		// series_worker.go:792) writes NULL on top and silently drops the
		// stamp (Story 552 root cause repeats — see seriesUpsertAssignments
		// for the series-table analogue).
		"episodes_synced_at": gorm.Expr("COALESCE(excluded.episodes_synced_at, seasons.episodes_synced_at)"),
		"updated_at":         gorm.Expr("excluded.updated_at"),
	}
}

// MarkSeasonEpisodesSynced stamps seasons.episodes_synced_at = now for the
// (series_id, season_number) pair. A3a narrow refresh writer. The COALESCE
// on the Upsert path (seasonsUpsertAssignments) ensures a concurrent
// Sonarr scan does NOT overwrite this stamp with NULL.
//
// Composite-key UPDATE (matches the seasons natural key). Empty match (no
// such row) returns nil — caller (Worker.RefreshSeasonSlim step 5e) runs
// inside a tx that already invoked Upsert above, guaranteeing the row
// exists; the defensive nil-on-zero-rows-matched preserves idempotency
// against rare race where the row got deleted between Upsert and stamp.
func (r *SeasonsRepository) MarkSeasonEpisodesSynced(ctx context.Context, seriesID domain.SeriesID, seasonNumber int, now time.Time) error {
	if seriesID == 0 {
		return fmt.Errorf("mark season episodes synced: series_id must be non-zero")
	}
	err := dbFromContext(ctx, r.db).WithContext(ctx).
		Table("seasons").
		Where("series_id = ? AND season_number = ?", seriesID, seasonNumber).
		Updates(map[string]any{
			"episodes_synced_at": now.UTC(),
			"updated_at":         now.UTC(),
		}).Error
	if err != nil {
		return fmt.Errorf("mark season episodes synced: %w", err)
	}
	return nil
}

func toCanonSeason(m database.SeasonModel) series.CanonSeason {
	return series.CanonSeason{
		ID:               m.ID,
		SeriesID:         m.SeriesID,
		SeasonNumber:     m.SeasonNumber,
		TMDBSeasonID:     m.TMDBSeasonID,
		Name:             m.Name,
		Overview:         m.Overview,
		AirDate:          m.AirDate,
		EpisodeCount:     m.EpisodeCount,
		PosterAsset:      m.PosterAsset,
		CreatedAt:        m.CreatedAt,
		UpdatedAt:        m.UpdatedAt,
		EpisodesSyncedAt: m.EpisodesSyncedAt,
	}
}

func fromCanonSeason(s series.CanonSeason) database.SeasonModel {
	return database.SeasonModel{
		ID:               s.ID,
		SeriesID:         s.SeriesID,
		SeasonNumber:     s.SeasonNumber,
		TMDBSeasonID:     s.TMDBSeasonID,
		Name:             s.Name,
		Overview:         s.Overview,
		AirDate:          s.AirDate,
		EpisodeCount:     s.EpisodeCount,
		PosterAsset:      s.PosterAsset,
		CreatedAt:        s.CreatedAt,
		UpdatedAt:        s.UpdatedAt,
		EpisodesSyncedAt: s.EpisodesSyncedAt,
	}
}
