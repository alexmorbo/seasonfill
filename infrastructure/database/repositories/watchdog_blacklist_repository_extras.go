package repositories

import (
	"context"
	"errors"
	"fmt"
	"time"

	"gorm.io/gorm"

	"github.com/alexmorbo/seasonfill/infrastructure/database"
	sharedErrors "github.com/alexmorbo/seasonfill/internal/shared/errors"
	"github.com/alexmorbo/seasonfill/internal/watchdog/domain/regrab"
)

// DeleteByID removes the row by primary key, scoped to instanceID.
// Returns typed WatchdogBlacklistNotFoundError when the row does
// not exist or belongs to another instance. The double-key predicate
// is a defence-in-depth measure: an authenticated client cannot
// DELETE blacklist rows that belong to an instance they did not
// address. F-2c-3 dropped the legacy errors.Join(typed,
// ports.ErrNotFound) shim; the sole consumer
// (interface/http/handlers/watchdog_blacklist.go Unpark) dispatches
// via c.Error so the middleware emits 404 +
// watchdog_blacklist_not_found through errors.As.
func (r *WatchdogBlacklistRepository) DeleteByID(ctx context.Context, instanceID, id uint) error {
	res := dbFromContext(ctx, r.db).WithContext(ctx).
		Where("instance_id = ? AND id = ?", instanceID, id).
		Delete(&database.WatchdogBlacklistModel{})
	if res.Error != nil {
		return fmt.Errorf("delete blacklist by id: %w", res.Error)
	}
	if res.RowsAffected == 0 {
		return &sharedErrors.WatchdogBlacklistNotFoundError{ID: id}
	}
	return nil
}

// ListByInstanceWithLimit returns the page in (created_at desc, id desc)
// order. afterCreatedAt + afterID together are the opaque keyset cursor
// (both zero = first page). limit must be > 0 — the repo does not
// enforce a hard upper bound; the HTTP handler caps it.
func (r *WatchdogBlacklistRepository) ListByInstanceWithLimit(
	ctx context.Context, instanceID uint, limit int,
	afterCreatedAt time.Time, afterID uint,
) ([]regrab.BlacklistEntry, error) {
	if limit <= 0 {
		return nil, errors.New("watchdog_blacklist: limit must be positive")
	}

	q := dbFromContext(ctx, r.db).WithContext(ctx).
		Model(&database.WatchdogBlacklistModel{}).
		Where("instance_id = ?", instanceID)

	if !afterCreatedAt.IsZero() || afterID != 0 {
		// Keyset predicate: (created_at, id) < (afterCreatedAt, afterID)
		// in DESC order.
		q = q.Where("(created_at, id) < (?, ?)", afterCreatedAt, afterID)
	}

	var models []database.WatchdogBlacklistModel
	if err := q.Order("created_at DESC, id DESC").
		Limit(limit).
		Find(&models).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, fmt.Errorf("list blacklist by instance with limit: %w", err)
	}
	out := make([]regrab.BlacklistEntry, 0, len(models))
	for _, m := range models {
		out = append(out, toBlacklistEntry(m))
	}
	return out, nil
}
