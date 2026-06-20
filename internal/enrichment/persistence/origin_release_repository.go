package persistence

import (
	"context"
	"errors"
	"fmt"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/alexmorbo/seasonfill/application/ports"
	database "github.com/alexmorbo/seasonfill/internal/shared/db"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
)

type OriginReleaseRepository struct {
	db *gorm.DB
}

func NewOriginReleaseRepository(db *gorm.DB) *OriginReleaseRepository {
	return &OriginReleaseRepository{db: db}
}

func (r *OriginReleaseRepository) Get(ctx context.Context, instance domain.InstanceName, seriesID domain.SonarrSeriesID, season int) (ports.OriginRelease, bool, error) {
	var model database.OriginReleaseModel
	err := dbFromContext(ctx, r.db).WithContext(ctx).
		First(&model, "instance_name = ? AND series_id = ? AND season_number = ?", instance, seriesID, season).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ports.OriginRelease{}, false, nil
		}
		return ports.OriginRelease{}, false, fmt.Errorf("get origin: %w", err)
	}
	return ports.OriginRelease{
		InstanceName: model.InstanceName,
		SeriesID:     model.SeriesID,
		SeasonNumber: model.SeasonNumber,
		GUID:         model.GUID,
		IndexerID:    model.IndexerID,
		IndexerName:  model.IndexerName,
		Source:       model.Source,
		FirstSeenAt:  model.FirstSeenAt,
		LastSeenAt:   model.LastSeenAt,
		LastUsedAt:   model.LastUsedAt,
	}, true, nil
}

func (r *OriginReleaseRepository) Upsert(ctx context.Context, rec ports.OriginRelease) error {
	model := database.OriginReleaseModel{
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
	res := dbFromContext(ctx, r.db).WithContext(ctx).Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "instance_name"}, {Name: "series_id"}, {Name: "season_number"}},
		DoUpdates: clause.AssignmentColumns([]string{
			"guid", "indexer_id", "indexer_name", "source", "last_seen_at", "last_used_at",
		}),
	}).Create(&model)
	if res.Error != nil {
		return fmt.Errorf("upsert origin: %w", res.Error)
	}
	return nil
}

var _ ports.OriginReleaseRepository = (*OriginReleaseRepository)(nil)
