package persistence

import (
	"context"
	"fmt"
	"time"

	database "github.com/alexmorbo/seasonfill/internal/shared/db"
)

// MarkAllMoviesChanged stamps movies.tmdb_changed_at = now on EVERY row with a
// non-NULL tmdb_id, unconditionally. It is the one-shot backfill lever (audit
// F-Ф1-07): the ~411 movies enriched BEFORE the Ф1.1 section writers + the
// movie-i18n ru-RU path existed carry a FRESH enrichment_tmdb_synced_at, so
// PickMovieRefreshCandidates does not re-pick them until TTL expiry. Bumping
// tmdb_changed_at past their section clocks drops each movie into the picker's
// CHANGED tier (the Ф1.3-validated path); the throttled MovieRefreshScheduler
// then drains them over multiple ticks at its tier LIMIT + 15m race guard. This
// method neither bypasses the rate limiter nor enriches inline — it only marks.
//
// Unlike MarkStaleForReenrich this carries NO "not-already-queued" anti-double-
// bump guard: the backfill deliberately re-stamps ALL tmdb_id movies (even ones
// already changed-pending) so a single call guarantees every movie is
// re-enriched once. A second call is idempotent in the operational sense — it
// succeeds and re-advances tmdb_changed_at; the scheduler still drains each
// movie once per bump and the 15m race guard prevents a concurrent
// double-refresh.
//
// UpdateColumn on an explicit Model targets exactly one column and skips GORM
// autoUpdateTime — movies.updated_at is NOT bumped (mirror of the L-06 note on
// MarkChangedByTMDBIDs / MarkStaleForReenrich). tmdb_changed_at is the sole
// writer here; the column is absent from movieUpsertAssignments so no Radarr/
// TMDB canon write can null it. Rows with tmdb_id IS NULL are left untouched
// (they can never be enriched from TMDB). Returns RowsAffected (rows marked).
func (r *MovieRepository) MarkAllMoviesChanged(ctx context.Context, now time.Time) (int64, error) {
	res := dbFromContext(ctx, r.db).WithContext(ctx).
		Model(&database.MovieModel{}).
		Where("tmdb_id IS NOT NULL").
		UpdateColumn("tmdb_changed_at", now.UTC())
	if res.Error != nil {
		return 0, fmt.Errorf("mark all movies changed: %w", res.Error)
	}
	return res.RowsAffected, nil
}
