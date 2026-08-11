// movie_repository_omdb.go — Ф6-R-4a (L3-3) OMDb-owned rating columns writer.
package persistence

import (
	"context"
	"fmt"
	"time"

	"github.com/alexmorbo/seasonfill/internal/shared/clients/omdb"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
)

// UpdateMovieOMDbColumns writes the four OMDb-owned columns (imdb_rating,
// imdb_votes, omdb_rated, omdb_awards) as PLAIN values — INCLUDING NULL — onto
// the existing movies row, keyed by id, then folds MarkOMDBSynced so the
// column write + freshness stamp land together. The movie OMDb worker is the
// SOLE owner of these four columns: every OTHER writer (TMDB/Radarr canon
// Upsert) goes through movieUpsertAssignments() which COALESCE-preserves them,
// so only this method can set OR clear them.
//
// Plain assignment (map[string]any binds a nil pointer as SQL NULL, unlike
// struct-based Updates which skips zero values) is the whole point: an OMDb
// "N/A" response — omdb.Map yields a nil pointer for that field — actively
// CLEARS a previously-stored rating rather than preserving a stale value. This
// is the exact inverse of the COALESCE Upsert path. Mirror of
// SeriesRepository.UpdateOMDbColumns (series_repository.go:552).
//
// It touches ONLY the four OMDb columns + updated_at; the TMDB-owned columns
// (tmdb_rating/tmdb_votes/title/…) are never in the assignment map so a rating
// refresh can never disturb TMDB canon. Participates in the caller's tx via
// dbFromContext when one is active.
func (r *MovieRepository) UpdateMovieOMDbColumns(
	ctx context.Context,
	id domain.MovieID,
	e omdb.Enrichment,
	now time.Time,
) error {
	if id == 0 {
		return fmt.Errorf("update movie omdb columns: movie_id must be non-zero")
	}
	vals := map[string]any{
		"imdb_rating": e.IMDBRating,
		"imdb_votes":  int64PtrToIntPtr(e.IMDBVotes), // movies.imdb_votes is *int
		"omdb_rated":  e.OMDbRated,
		"omdb_awards": e.OMDbAwards,
		"updated_at":  now.UTC(),
	}
	if err := dbFromContext(ctx, r.db).WithContext(ctx).
		Table("movies").Where("id = ?", id).Updates(vals).Error; err != nil {
		return fmt.Errorf("update movie omdb columns: %w", err)
	}
	return r.MarkOMDBSynced(ctx, id, now)
}

// int64PtrToIntPtr narrows *int64 → *int, preserving nil. omdb.Enrichment
// carries IMDBVotes as *int64 (upstream vote counts) but the movies canon
// column (MovieModel.IMDBVotes / movie.Canon.IMDBVotes) is *int — R-3 chose int
// to match the series column. Real IMDb vote counts are always well within int
// range on every supported platform, so the narrowing is lossless; the series
// OMDb worker does the identical cast inline (omdb_worker.go:279).
func int64PtrToIntPtr(p *int64) *int {
	if p == nil {
		return nil
	}
	v := int(*p)
	return &v
}
