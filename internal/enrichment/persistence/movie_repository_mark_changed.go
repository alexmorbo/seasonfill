package persistence

import (
	"context"
	"fmt"
	"time"

	database "github.com/alexmorbo/seasonfill/internal/shared/db"
)

// movieMarkChangedBatchSize is the IN-chunk size for the movie changes marker
// (mirror of markChangedBatchSize). TMDB repeats ids across firehose pages, so
// the poller may hand large id slices; chunking keeps the IN() list bounded.
const movieMarkChangedBatchSize = 500

// MarkChangedByTMDBIDs stamps movies.tmdb_changed_at = markedAt on rows whose
// tmdb_id ∈ ids AND (tmdb_changed_at IS NULL OR tmdb_changed_at < dedupBoundary).
// Movie analog of SeriesRepository.MarkChangedByTMDBIDs; satisfies the generic
// enrichment.ChangedSeriesMarker port so the shared ChangesPoller drives the
// /movie/changes firehose with movie deps (zero TV edits).
//
// grep-AC: this is the ONLY writer of movies.tmdb_changed_at. The column is
// ABSENT from movieUpsertAssignments() and every OnConflict.DoUpdates, so a
// Radarr/TMDB canon write can never null it (mirror of the series invariant).
//
// The write uses UpdateColumn on an explicit Model so it targets exactly one
// column and skips GORM autoUpdateTime — movies.updated_at is NOT bumped by a
// changes mark (mirror of the series marker's L-06 note).
//
// ids are deduped in-memory and chunked at movieMarkChangedBatchSize. Returns
// the summed RowsAffected (rows actually marked). Empty ids → (0, nil).
func (r *MovieRepository) MarkChangedByTMDBIDs(
	ctx context.Context,
	ids []int64,
	markedAt, dedupBoundary time.Time,
) (int64, error) {
	if len(ids) == 0 {
		return 0, nil
	}
	uniq := dedupInt64Preserve(ids)
	markedUTC := markedAt.UTC()
	boundaryUTC := dedupBoundary.UTC()

	var total int64
	for start := 0; start < len(uniq); start += movieMarkChangedBatchSize {
		end := min(start+movieMarkChangedBatchSize, len(uniq))
		chunk := uniq[start:end]

		res := dbFromContext(ctx, r.db).WithContext(ctx).
			Model(&database.MovieModel{}).
			Where("tmdb_id IN ?", chunk).
			Where("tmdb_changed_at IS NULL OR tmdb_changed_at < ?", boundaryUTC).
			UpdateColumn("tmdb_changed_at", markedUTC)
		if res.Error != nil {
			return total, fmt.Errorf("mark movie tmdb changed: %w", res.Error)
		}
		total += res.RowsAffected
	}
	return total, nil
}
