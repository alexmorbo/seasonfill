package repositories

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/alexmorbo/seasonfill/application/ports"
	"github.com/alexmorbo/seasonfill/domain/series"
	"github.com/alexmorbo/seasonfill/infrastructure/database"
)

// SeriesCacheRepository persists per-instance Sonarr series metadata
// (D66). Upsert resurrects soft-deleted rows by clearing deleted_at —
// the scan and queue handlers see Sonarr "ground truth" again whenever
// the series re-appears in /api/v3/series, while the row's identity
// (and any grab_records FK references) is preserved.
type SeriesCacheRepository struct {
	db *gorm.DB
}

func NewSeriesCacheRepository(db *gorm.DB) *SeriesCacheRepository {
	return &SeriesCacheRepository{db: db}
}

func (r *SeriesCacheRepository) Get(ctx context.Context, instanceName string, sonarrSeriesID int) (series.CacheEntry, error) {
	var m database.SeriesCacheModel
	err := dbFromContext(ctx, r.db).WithContext(ctx).
		Where("instance_name = ? AND sonarr_series_id = ?", instanceName, sonarrSeriesID).
		First(&m).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return series.CacheEntry{}, ports.ErrNotFound
		}
		return series.CacheEntry{}, fmt.Errorf("get series_cache: %w", err)
	}
	entry, cErr := toCacheEntry(m)
	if cErr != nil {
		return series.CacheEntry{}, fmt.Errorf("decode series_cache: %w", cErr)
	}
	return entry, nil
}

// Upsert writes/replaces the row keyed on composite PK. The conflict
// path always sets deleted_at = NULL. Callers wanting soft-delete use
// SoftDelete, not Upsert with DeletedAt set.
func (r *SeriesCacheRepository) Upsert(ctx context.Context, entry series.CacheEntry) error {
	if entry.InstanceName == "" {
		return fmt.Errorf("upsert series_cache: instance_name must be non-empty")
	}
	if entry.SonarrSeriesID == 0 {
		return fmt.Errorf("upsert series_cache: sonarr_series_id must be non-zero")
	}
	now := time.Now().UTC()
	entry.UpdatedAt = now
	entry.DeletedAt = nil

	m, mErr := cacheEntryToModel(entry)
	if mErr != nil {
		return fmt.Errorf("encode series_cache: %w", mErr)
	}

	res := dbFromContext(ctx, r.db).WithContext(ctx).Clauses(clause.OnConflict{
		Columns: []clause.Column{
			{Name: "instance_name"},
			{Name: "sonarr_series_id"},
		},
		DoUpdates: clause.AssignmentColumns([]string{
			"title", "title_slug", "year",
			"tvdb_id", "imdb_id", "tmdb_id",
			"status", "network", "genres",
			"runtime_minutes", "monitored", "overview",
			"poster_path", "fanart_path", "banner_path",
			"updated_at", "deleted_at",
		}),
	}).Create(&m)
	if res.Error != nil {
		return fmt.Errorf("upsert series_cache: %w", res.Error)
	}
	return nil
}

// SoftDelete stamps deleted_at = now. Idempotent — missing row OR
// already-deleted row both return nil. The 041f webhook fires
// SeriesDelete for IDs that may never have been cached; surfacing
// ErrNotFound would just spam logs without driving any action.
func (r *SeriesCacheRepository) SoftDelete(ctx context.Context, instanceName string, sonarrSeriesID int) error {
	now := time.Now().UTC()
	res := dbFromContext(ctx, r.db).WithContext(ctx).
		Model(&database.SeriesCacheModel{}).
		Where("instance_name = ? AND sonarr_series_id = ?", instanceName, sonarrSeriesID).
		Updates(map[string]interface{}{
			"deleted_at": now,
			"updated_at": now,
		})
	if res.Error != nil {
		return fmt.Errorf("soft delete series_cache: %w", res.Error)
	}
	return nil
}

func (r *SeriesCacheRepository) ListActiveByInstance(ctx context.Context, instanceName string) ([]series.CacheEntry, error) {
	var models []database.SeriesCacheModel
	err := dbFromContext(ctx, r.db).WithContext(ctx).
		Where("instance_name = ? AND deleted_at IS NULL", instanceName).
		Find(&models).Error
	if err != nil {
		return nil, fmt.Errorf("list active series_cache: %w", err)
	}
	out := make([]series.CacheEntry, 0, len(models))
	for _, m := range models {
		entry, cErr := toCacheEntry(m)
		if cErr != nil {
			return nil, fmt.Errorf("decode series_cache: %w", cErr)
		}
		out = append(out, entry)
	}
	return out, nil
}

// toCacheEntry maps DB model → domain. Genres JSON-decoded; *string
// "" treated as nil. Every other field copied 1:1.
func toCacheEntry(m database.SeriesCacheModel) (series.CacheEntry, error) {
	var genres []string
	if m.Genres != nil && *m.Genres != "" {
		if err := json.Unmarshal([]byte(*m.Genres), &genres); err != nil {
			return series.CacheEntry{}, fmt.Errorf("unmarshal genres: %w", err)
		}
	}
	return series.CacheEntry{
		InstanceName:   m.InstanceName,
		SonarrSeriesID: m.SonarrSeriesID,
		Title:          m.Title,
		TitleSlug:      m.TitleSlug,
		Year:           m.Year,
		TVDBID:         m.TVDBID,
		IMDBID:         m.IMDBID,
		TMDBID:         m.TMDBID,
		Status:         m.Status,
		Network:        m.Network,
		Genres:         genres,
		RuntimeMinutes: m.RuntimeMinutes,
		Monitored:      m.Monitored,
		Overview:       m.Overview,
		PosterPath:     m.PosterPath,
		FanartPath:     m.FanartPath,
		BannerPath:     m.BannerPath,
		UpdatedAt:      m.UpdatedAt,
		DeletedAt:      m.DeletedAt,
	}, nil
}

// cacheEntryToModel: inverse of toCacheEntry. Genres JSON-encoded;
// nil / empty slice both serialise to nil *string (DB NULL).
func cacheEntryToModel(e series.CacheEntry) (database.SeriesCacheModel, error) {
	var genresPtr *string
	if len(e.Genres) > 0 {
		raw, err := json.Marshal(e.Genres)
		if err != nil {
			return database.SeriesCacheModel{}, fmt.Errorf("marshal genres: %w", err)
		}
		s := string(raw)
		genresPtr = &s
	}
	return database.SeriesCacheModel{
		InstanceName:   e.InstanceName,
		SonarrSeriesID: e.SonarrSeriesID,
		Title:          e.Title,
		TitleSlug:      e.TitleSlug,
		Year:           e.Year,
		TVDBID:         e.TVDBID,
		IMDBID:         e.IMDBID,
		TMDBID:         e.TMDBID,
		Status:         e.Status,
		Network:        e.Network,
		Genres:         genresPtr,
		RuntimeMinutes: e.RuntimeMinutes,
		Monitored:      e.Monitored,
		Overview:       e.Overview,
		PosterPath:     e.PosterPath,
		FanartPath:     e.FanartPath,
		BannerPath:     e.BannerPath,
		UpdatedAt:      e.UpdatedAt,
		DeletedAt:      e.DeletedAt,
	}, nil
}

var _ ports.SeriesCacheRepository = (*SeriesCacheRepository)(nil)
