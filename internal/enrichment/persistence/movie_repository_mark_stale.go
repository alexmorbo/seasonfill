package persistence

import (
	"context"
	"fmt"
	"time"

	database "github.com/alexmorbo/seasonfill/internal/shared/db"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
)

// MarkStaleForReenrich bumps movies.tmdb_changed_at = now for movie id, marking
// it for re-enrichment on the next MovieRefreshScheduler tick (Ф1.2 on-read
// hydration). It is the movie analog of the on-demand "make stale" nudge —
// movies have no hot dispatcher, so the only lever is the cron picker's CHANGED
// tier, which keys off tmdb_changed_at.
//
// The WHERE clause is the EXACT negation of PickMovieRefreshCandidates' CHANGED
// "changed-pending" predicate, so we only bump when the movie is NOT already
// queued. This makes the marker idempotent under a page-view storm: a movie that
// is already changed-pending (tmdb_changed_at set, not yet re-synced) is a no-op
// (RowsAffected 0), so the clock is never re-stamped forward. A movie whose
// sections went empty but whose tmdb_changed_at is NULL (previously enriched) is
// bumped and re-picked after the 15m race window.
//
// UpdateColumn on an explicit Model targets exactly one column and skips GORM
// autoUpdateTime — updated_at is NOT bumped by a hydration nudge (mirror of
// MarkChangedByTMDBIDs / the series marker's L-06 note). tmdb_changed_at is the
// sole writer here; the column is absent from movieUpsertAssignments so no canon
// write can null it. RowsAffected 0 is a valid idempotent skip, not an error.
func (r *MovieRepository) MarkStaleForReenrich(ctx context.Context, id domain.MovieID, now time.Time) error {
	if id == 0 {
		return fmt.Errorf("mark movie stale for reenrich: movie_id must be non-zero")
	}
	res := dbFromContext(ctx, r.db).WithContext(ctx).
		Model(&database.MovieModel{}).
		Where("id = ?", id).
		Where("NOT (tmdb_changed_at IS NOT NULL AND (enrichment_tmdb_synced_at IS NULL OR enrichment_tmdb_synced_at < tmdb_changed_at))").
		UpdateColumn("tmdb_changed_at", now.UTC())
	if res.Error != nil {
		return fmt.Errorf("mark movie stale for reenrich: %w", res.Error)
	}
	return nil
}
