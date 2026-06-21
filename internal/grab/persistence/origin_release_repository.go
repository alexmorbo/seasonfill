package persistence

import (
	"context"
	"errors"
	"fmt"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	ports "github.com/alexmorbo/seasonfill/internal/shared/dataports"
	database "github.com/alexmorbo/seasonfill/internal/shared/db"
	"github.com/alexmorbo/seasonfill/internal/shared/dbtx"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
)

// OriginReleaseRepository persists the origin_releases table — the
// first-seen GUID per (instance, series, season) triple used by replay
// selection to prefer the original indexer when re-grabbing.
//
// 467a / D-6: ownership transferred from internal/enrichment/persistence
// (D-3 closure note: "origin_releases owned by D-6"). The 000017
// migration re-introduces the backing table after the D-1 cleanup.
type OriginReleaseRepository struct {
	db *gorm.DB
}

// NewOriginReleaseRepository wires the repository to a GORM DB.
func NewOriginReleaseRepository(db *gorm.DB) *OriginReleaseRepository {
	return &OriginReleaseRepository{db: db}
}

// Get returns the origin_releases row keyed by the (instance, series,
// season) triple. Returns (zero, false, nil) on miss.
func (r *OriginReleaseRepository) Get(ctx context.Context, instance domain.InstanceName, seriesID domain.SonarrSeriesID, season int) (ports.OriginRelease, bool, error) {
	var m database.OriginReleaseModel
	err := dbtx.DBFromContext(ctx, r.db).WithContext(ctx).
		First(&m, "instance_name = ? AND series_id = ? AND season_number = ?", instance, seriesID, season).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ports.OriginRelease{}, false, nil
		}
		return ports.OriginRelease{}, false, fmt.Errorf("get origin_release: %w", err)
	}
	return ports.OriginRelease{
		InstanceName: m.InstanceName,
		SeriesID:     m.SeriesID,
		SeasonNumber: m.SeasonNumber,
		GUID:         m.GUID,
		IndexerID:    m.IndexerID,
		IndexerName:  m.IndexerName,
		Source:       m.Source,
		FirstSeenAt:  m.FirstSeenAt,
		LastSeenAt:   m.LastSeenAt,
		LastUsedAt:   m.LastUsedAt,
	}, true, nil
}

// Upsert writes the row, replacing the GUID / indexer / last_seen_at /
// last_used_at fields on conflict. first_seen_at is preserved by
// COALESCE — the first write wins (audit invariant).
//
// COALESCE on first_seen_at: the column captures the original
// detection time of the (instance, series, season) triple — a later
// upsert with a fresher GUID rewrites the rest of the row but must NOT
// re-stamp first_seen_at. The OnConflict DoUpdates uses an explicit
// SQL expression for that field; the rest of the column set comes from
// AssignmentColumns.
func (r *OriginReleaseRepository) Upsert(ctx context.Context, rec ports.OriginRelease) error {
	m := database.OriginReleaseModel{
		InstanceName: rec.InstanceName,
		SeriesID:     rec.SeriesID,
		SeasonNumber: rec.SeasonNumber,
		GUID:         rec.GUID,
		IndexerID:    rec.IndexerID,
		IndexerName:  rec.IndexerName,
		Source:       rec.Source,
		FirstSeenAt:  rec.FirstSeenAt,
		LastSeenAt:   rec.LastSeenAt,
		LastUsedAt:   rec.LastUsedAt,
	}
	res := dbtx.DBFromContext(ctx, r.db).WithContext(ctx).Clauses(clause.OnConflict{
		Columns: []clause.Column{
			{Name: "instance_name"},
			{Name: "series_id"},
			{Name: "season_number"},
		},
		DoUpdates: clause.AssignmentColumns([]string{
			"guid", "indexer_id", "indexer_name", "source",
			"last_seen_at", "last_used_at",
		}),
	}).Create(&m)
	if res.Error != nil {
		return fmt.Errorf("upsert origin_release: %w", res.Error)
	}
	return nil
}

var _ ports.OriginReleaseRepository = (*OriginReleaseRepository)(nil)
