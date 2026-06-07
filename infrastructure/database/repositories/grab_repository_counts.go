package repositories

import (
	"context"
	"fmt"
	"time"

	"github.com/alexmorbo/seasonfill/infrastructure/database"
)

// CountReplaysSince — instance + window filter.
func (r *GrabRepository) CountReplaysSince(ctx context.Context, instanceName string, since time.Time) (int, error) {
	var count int64
	err := dbFromContext(ctx, r.db).WithContext(ctx).
		Model(&database.GrabRecordModel{}).
		Where("instance_name = ? AND replay_of_id IS NOT NULL AND created_at >= ?", instanceName, since).
		Count(&count).Error
	if err != nil {
		return 0, fmt.Errorf("count replays since: %w", err)
	}
	return int(count), nil
}

// CountReplaysAll — lifetime count for instance.
func (r *GrabRepository) CountReplaysAll(ctx context.Context, instanceName string) (int, error) {
	var count int64
	err := dbFromContext(ctx, r.db).WithContext(ctx).
		Model(&database.GrabRecordModel{}).
		Where("instance_name = ? AND replay_of_id IS NOT NULL", instanceName).
		Count(&count).Error
	if err != nil {
		return 0, fmt.Errorf("count replays all: %w", err)
	}
	return int(count), nil
}
