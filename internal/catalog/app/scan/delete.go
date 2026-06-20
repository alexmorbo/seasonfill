// Package scan — SeriesDelete cascade (story 218, E-2).
//
// On Sonarr SeriesDelete webhook, the canonical `series` row is
// preserved (PRD §5.11 — another instance may still reference it;
// recommendations may reference it) but two per-instance rows are
// soft-deleted in a single transaction:
//
//   - series_cache(instance_name, sonarr_series_id) — sets deleted_at
//   - episode_states(instance_name, episode_id) for every episode
//     under the canon series — sets deleted_at
//
// Both writes happen inside the same Transactor scope so a half-
// applied cascade is impossible. Repeated SeriesDelete deliveries are
// safe — both UPDATEs are idempotent (set deleted_at = now() each time;
// production readers filter by IS NULL).

package scan

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	ports "github.com/alexmorbo/seasonfill/internal/shared/dataports"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
	sharedports "github.com/alexmorbo/seasonfill/internal/shared/ports"
)

// CascadeDeleteDeps is the narrow port surface. Tx is required when
// atomicity matters; nil-Tx mode (test convenience) runs the writes
// sequentially without a transaction.
type CascadeDeleteDeps struct {
	SeriesCache   ports.SeriesCacheRepository
	EpisodeStates EpisodeStatesSoftDeleter
	SeasonStats   SeasonStatsSoftDeleter
	Tx            ports.Transactor
	Logger        *slog.Logger
}

// EpisodeStatesSoftDeleter is the new narrow port the cascade adds.
// Implemented by EpisodeStatesRepository in 218.
type EpisodeStatesSoftDeleter interface {
	SoftDeleteBySeries(ctx context.Context, instanceName domain.InstanceName, sonarrSeriesID domain.SonarrSeriesID) (int, error)
}

// SeasonStatsSoftDeleter — story 377 cascade port. Implemented by
// SeasonStatsRepository.
type SeasonStatsSoftDeleter interface {
	SoftDeleteBySeries(ctx context.Context, instanceName domain.InstanceName, sonarrSeriesID domain.SonarrSeriesID) (int, error)
}

// CascadeSeriesDelete soft-deletes the cache row + every episode_state
// row that belongs to it. Returns (cacheDeleted, episodeStateRows,
// error). episodeStateRows = 0 with no error means "no episodes
// recorded yet" — common for instances that haven't completed their
// first scan.
//
// Errors from either side are wrapped with %w so the caller can
// errors.Is the upstream sentinel. The cache-side `SoftDelete` is
// idempotent today and returns nil on already-deleted rows.
func CascadeSeriesDelete(
	ctx context.Context,
	deps CascadeDeleteDeps,
	instanceName domain.InstanceName,
	sonarrSeriesID domain.SonarrSeriesID,
) (cacheDeleted bool, episodeRows int, seasonRows int, err error) {
	if instanceName == "" {
		return false, 0, 0, errors.New("cascade series delete: instance_name must be non-empty")
	}
	if sonarrSeriesID == 0 {
		return false, 0, 0, errors.New("cascade series delete: sonarr_series_id must be non-zero")
	}
	if deps.SeriesCache == nil {
		return false, 0, 0, errors.New("cascade series delete: SeriesCache required")
	}
	log := deps.Logger
	if log == nil {
		log = sharedports.DomainLogger(slog.Default(), "scan")
	}

	work := func(txCtx context.Context) error {
		if serr := deps.SeriesCache.SoftDelete(txCtx, instanceName, sonarrSeriesID); serr != nil {
			return fmt.Errorf("soft delete series_cache: %w", serr)
		}
		cacheDeleted = true
		if deps.EpisodeStates != nil {
			n, ierr := deps.EpisodeStates.SoftDeleteBySeries(txCtx, instanceName, sonarrSeriesID)
			if ierr != nil {
				return fmt.Errorf("soft delete episode_states: %w", ierr)
			}
			episodeRows = n
		}
		if deps.SeasonStats != nil {
			n, ierr := deps.SeasonStats.SoftDeleteBySeries(txCtx, instanceName, sonarrSeriesID)
			if ierr != nil {
				return fmt.Errorf("soft delete season_stats: %w", ierr)
			}
			seasonRows = n
		}
		return nil
	}

	if deps.Tx != nil {
		if terr := deps.Tx.Transaction(ctx, work); terr != nil {
			return cacheDeleted, episodeRows, seasonRows, terr
		}
	} else {
		if werr := work(ctx); werr != nil {
			return cacheDeleted, episodeRows, seasonRows, werr
		}
	}

	log.InfoContext(ctx, "scan.cascade_series_delete.ok",
		slog.String("instance_name", string(instanceName)),
		slog.Int("sonarr_series_id", int(sonarrSeriesID)),
		slog.Bool("cache_deleted", cacheDeleted),
		slog.Int("episode_states_deleted", episodeRows),
		slog.Int("season_stats_deleted", seasonRows),
	)
	return cacheDeleted, episodeRows, seasonRows, nil
}
