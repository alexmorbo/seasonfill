package persistence

import (
	"context"
	"errors"
	"fmt"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	grab "github.com/alexmorbo/seasonfill/internal/grab/domain"
	ports "github.com/alexmorbo/seasonfill/internal/shared/dataports"
	database "github.com/alexmorbo/seasonfill/internal/shared/db"
	"github.com/alexmorbo/seasonfill/internal/shared/dbtx"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
)

// DownloadLinkRepository persists download_links rows. 467a / D-6 Phase 1
// (webhook + arr-poll). The instance-backfill job lives in N-5.
type DownloadLinkRepository struct {
	db *gorm.DB
}

// NewDownloadLinkRepository wires the repository to a GORM DB.
func NewDownloadLinkRepository(db *gorm.DB) *DownloadLinkRepository {
	return &DownloadLinkRepository{db: db}
}

// InsertOnly inserts a download_links row, treating qbit_hash conflicts
// as silent success (the webhook + arr-poll loops race to populate the
// same row with equivalent payloads). Returns nil on conflict.
//
// The CHECK constraint download_links_type_id_check enforces
// (sonarr+series_id) XOR (radarr+movie_id); the writer must satisfy it.
func (r *DownloadLinkRepository) InsertOnly(ctx context.Context, link grab.DownloadLink) error {
	now := time.Now().UTC()
	created := link.CreatedAt
	if created.IsZero() {
		created = now
	}
	discovered := link.DiscoveredAt
	if discovered.IsZero() {
		discovered = now
	}
	instanceType := link.InstanceType
	if instanceType == "" {
		instanceType = "sonarr"
	}
	source := link.Source
	if source == "" {
		source = grab.LinkSourceWebhook
	}
	var globalSeriesID *int64
	if link.GlobalSeriesID != nil {
		v := int64(*link.GlobalSeriesID)
		globalSeriesID = &v
	}
	var episodeIDs *string
	if link.ExternalEpisodeIDs != "" {
		ids := link.ExternalEpisodeIDs
		episodeIDs = &ids
	}
	model := database.DownloadLinkModel{
		QbitHash:           string(link.QbitHash),
		InstanceName:       link.InstanceName,
		InstanceType:       instanceType,
		ExternalSeriesID:   link.ExternalSeriesID,
		ExternalMovieID:    link.ExternalMovieID,
		ExternalEpisodeIDs: episodeIDs,
		GlobalSeriesID:     globalSeriesID,
		DiscoveredAt:       discovered,
		Source:             string(source),
		CreatedAt:          created,
		UpdatedAt:          now,
	}
	res := dbtx.DBFromContext(ctx, r.db).WithContext(ctx).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "qbit_hash"}},
		DoNothing: true,
	}).Create(&model)
	if res.Error != nil {
		return fmt.Errorf("insert download_link: %w", res.Error)
	}
	return nil
}

// FindByHash resolves the matcher Strategy 1 lookup. Returns ErrNotFound
// on miss; the caller falls back to fuzzy matching.
func (r *DownloadLinkRepository) FindByHash(ctx context.Context, hash domain.QbitHash) (grab.DownloadLink, error) {
	var model database.DownloadLinkModel
	err := dbtx.DBFromContext(ctx, r.db).WithContext(ctx).
		First(&model, "qbit_hash = ?", string(hash)).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return grab.DownloadLink{}, ports.ErrNotFound
		}
		return grab.DownloadLink{}, fmt.Errorf("find download_link by hash: %w", err)
	}
	return toDownloadLink(model), nil
}

// SetGlobalSeriesID stamps the canon series_id when enrichment hydrates
// the foreign series. Idempotent — only updates rows where the column
// is currently NULL, never overwrites a value already set. Returns nil
// on miss (row may have been swept while enrichment ran).
func (r *DownloadLinkRepository) SetGlobalSeriesID(ctx context.Context, hash domain.QbitHash, seriesID domain.SeriesID) error {
	now := time.Now().UTC()
	res := dbtx.DBFromContext(ctx, r.db).WithContext(ctx).
		Model(&database.DownloadLinkModel{}).
		Where("qbit_hash = ? AND global_series_id IS NULL", string(hash)).
		Updates(map[string]any{
			"global_series_id": int64(seriesID),
			"updated_at":       now,
		})
	if res.Error != nil {
		return fmt.Errorf("set download_link global_series_id: %w", res.Error)
	}
	return nil
}

// ListByInstance returns the N most-recently-discovered download_links
// rows for instance, optionally filtered by source. limit <= 0 / >
// MaxListLimit is clamped to MaxListLimit.
func (r *DownloadLinkRepository) ListByInstance(ctx context.Context, instance domain.InstanceName, source *grab.LinkSource, limit int) ([]grab.DownloadLink, error) {
	if limit <= 0 || limit > ports.MaxListLimit {
		limit = ports.MaxListLimit
	}
	q := dbtx.DBFromContext(ctx, r.db).WithContext(ctx).
		Model(&database.DownloadLinkModel{}).
		Where("instance_name = ?", instance)
	if source != nil {
		q = q.Where("source = ?", string(*source))
	}
	var models []database.DownloadLinkModel
	if err := q.Order("discovered_at DESC, qbit_hash DESC").
		Limit(limit).
		Find(&models).Error; err != nil {
		return nil, fmt.Errorf("list download_links by instance: %w", err)
	}
	out := make([]grab.DownloadLink, 0, len(models))
	for _, m := range models {
		out = append(out, toDownloadLink(m))
	}
	return out, nil
}

func toDownloadLink(m database.DownloadLinkModel) grab.DownloadLink {
	episodes := ""
	if m.ExternalEpisodeIDs != nil {
		episodes = *m.ExternalEpisodeIDs
	}
	var globalSeriesID *domain.SeriesID
	if m.GlobalSeriesID != nil {
		v := domain.SeriesID(*m.GlobalSeriesID)
		globalSeriesID = &v
	}
	return grab.DownloadLink{
		QbitHash:           domain.QbitHash(m.QbitHash),
		InstanceName:       m.InstanceName,
		InstanceType:       m.InstanceType,
		ExternalSeriesID:   m.ExternalSeriesID,
		ExternalMovieID:    m.ExternalMovieID,
		ExternalEpisodeIDs: episodes,
		GlobalSeriesID:     globalSeriesID,
		DiscoveredAt:       m.DiscoveredAt,
		Source:             grab.LinkSource(m.Source),
		CreatedAt:          m.CreatedAt,
		UpdatedAt:          m.UpdatedAt,
	}
}

var _ ports.DownloadLinkRepository = (*DownloadLinkRepository)(nil)
