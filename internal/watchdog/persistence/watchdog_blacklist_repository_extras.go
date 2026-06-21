package persistence

import (
	"context"
	"errors"
	"fmt"
	"time"

	"gorm.io/gorm"

	database "github.com/alexmorbo/seasonfill/internal/shared/db"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
	"github.com/alexmorbo/seasonfill/internal/watchdog/domain/regrab"
)

// ListByInstanceWithLimit returns the page ordered by (blacklisted_at
// DESC, sonarr_series_id DESC, season_number DESC). The three-component
// keyset cursor (afterBlacklistedAt + afterSeriesID + afterSeason)
// breaks ties when two rows share a blacklisted_at timestamp.
// All-zero cursor = first page. limit must be > 0 — the repo does not
// enforce a hard upper bound; the HTTP handler caps it.
func (r *WatchdogBlacklistRepository) ListByInstanceWithLimit(
	ctx context.Context,
	instance domain.InstanceName,
	limit int,
	afterBlacklistedAt time.Time,
	afterSeriesID domain.SonarrSeriesID,
	afterSeason int,
) ([]regrab.BlacklistEntry, error) {
	if limit <= 0 {
		return nil, errors.New("watchdog_blacklist: limit must be positive")
	}

	q := dbFromContext(ctx, r.db).WithContext(ctx).
		Model(&database.WatchdogBlacklistModel{}).
		Where("instance_name = ?", instance)

	if !afterBlacklistedAt.IsZero() || afterSeriesID != 0 || afterSeason != 0 {
		// Keyset predicate: (blacklisted_at, sonarr_series_id, season_number)
		// < (afterBlacklistedAt, afterSeriesID, afterSeason) in DESC order.
		q = q.Where("(blacklisted_at, sonarr_series_id, season_number) < (?, ?, ?)",
			afterBlacklistedAt, afterSeriesID, afterSeason)
	}

	var models []database.WatchdogBlacklistModel
	if err := q.Order("blacklisted_at DESC, sonarr_series_id DESC, season_number DESC").
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
