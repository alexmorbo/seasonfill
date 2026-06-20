package persistence

import (
	"context"
	"fmt"
	"time"

	"github.com/alexmorbo/seasonfill/infrastructure/database"
	"github.com/alexmorbo/seasonfill/internal/shared/dbtx"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
)

// CountReplaysSince — instance + window filter.
func (r *GrabRepository) CountReplaysSince(ctx context.Context, instanceName domain.InstanceName, since time.Time) (int, error) {
	var count int64
	err := dbtx.DBFromContext(ctx, r.db).WithContext(ctx).
		Model(&database.GrabRecordModel{}).
		Where("instance_name = ? AND replay_of_id IS NOT NULL AND created_at >= ?", instanceName, since).
		Count(&count).Error
	if err != nil {
		return 0, fmt.Errorf("count replays since: %w", err)
	}
	return int(count), nil
}

// CountReplaysAll — lifetime count for instance.
func (r *GrabRepository) CountReplaysAll(ctx context.Context, instanceName domain.InstanceName) (int, error) {
	var count int64
	err := dbtx.DBFromContext(ctx, r.db).WithContext(ctx).
		Model(&database.GrabRecordModel{}).
		Where("instance_name = ? AND replay_of_id IS NOT NULL", instanceName).
		Count(&count).Error
	if err != nil {
		return 0, fmt.Errorf("count replays all: %w", err)
	}
	return int(count), nil
}
