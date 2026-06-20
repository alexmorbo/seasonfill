package persistence

import (
	"context"
	"fmt"

	database "github.com/alexmorbo/seasonfill/internal/shared/db"
)

// CountByInstance returns the watchdog_blacklist row count for instanceID.
func (r *WatchdogBlacklistRepository) CountByInstance(ctx context.Context, instanceID uint) (int, error) {
	var count int64
	err := dbFromContext(ctx, r.db).WithContext(ctx).
		Model(&database.WatchdogBlacklistModel{}).
		Where("instance_id = ?", instanceID).
		Count(&count).Error
	if err != nil {
		return 0, fmt.Errorf("count blacklist by instance: %w", err)
	}
	return int(count), nil
}
