package persistence

import (
	"context"
	"fmt"

	"github.com/alexmorbo/seasonfill/infrastructure/database"
	"github.com/alexmorbo/seasonfill/internal/shared/dbtx"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
)

// CountImportedEpisodes implements ports.GrabRepository.CountImportedEpisodes.
//
// Counts grab_records rows for the (instance, series, season) triple
// whose status is "imported". Lives in a sibling file so the grab repo
// keeps Phase-10 + 046a additions clearly separated in the diff — the
// receiver matches the existing GrabRepository struct in
// grab_repository.go (same package).
//
// Zero-rows returns 0 with nil err — the calling evaluator treats that
// as "this triple has never been imported by us" which is a valid
// steady state, not an error. Real DB failures wrap with %w.
func (r *GrabRepository) CountImportedEpisodes(ctx context.Context, instance domain.InstanceName, seriesID domain.SonarrSeriesID, seasonNumber int) (int, error) {
	var count int64
	err := dbtx.DBFromContext(ctx, r.db).WithContext(ctx).
		Model(&database.GrabRecordModel{}).
		Where("instance_name = ? AND series_id = ? AND season_number = ? AND status = ?",
			instance, seriesID, seasonNumber, "imported").
		Count(&count).Error
	if err != nil {
		return 0, fmt.Errorf("count imported episodes: %w", err)
	}
	return int(count), nil
}
