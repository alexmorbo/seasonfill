// movie_worker_media_recs.go — Ф1.1c movie media (best trailer) + recommendations writers.
// writeVideos mirrors the "authoritative replace + section stamp" taxonomy pattern; the movie
// card shows a single hero trailer so only the best pick is persisted. writeRecommendations
// mirrors the series RefreshRecommendations tx shape (series_worker_refresh_recommendations.go)
// EXACTLY for the F-Ф1-12 cold-FK path: stub-upsert every recommended movie BEFORE the join
// insert (both movie_recommendations FKs → movies(id) CASCADE), sorting stubs by tmdb_id ASC
// for a global lock order. Movie titles live on the movies canon row (not a side-table), so —
// unlike series — there is NO per-language texts side-effect here.
package enrichment

import (
	"context"
	"fmt"
	"slices"

	"github.com/alexmorbo/seasonfill/internal/catalog/domain/movie"
	"github.com/alexmorbo/seasonfill/internal/shared/clients/tmdb"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
)

// writeVideos authoritatively replaces the movie's movie_videos rows with the chosen best
// trailer (nil clears) and stamps enrichment_media_synced_at — atomic in one Transactor tx.
func (w *MovieWorker) writeVideos(ctx context.Context, movieID domain.MovieID, trailer *movie.Video) error {
	return w.deps.Tx.Transaction(ctx, func(txCtx context.Context) error {
		if err := w.deps.Videos.ReplaceBestTrailer(txCtx, movieID, trailer); err != nil {
			return fmt.Errorf("replace best trailer: %w", err)
		}
		return w.deps.Movies.MarkMediaSynced(txCtx, movieID, w.deps.Clock())
	})
}

// writeRecommendations upserts each recommended movie as a stub (sorted tmdb_id ASC), resolves
// the parent's rec id slice in ORIGINAL TMDB-rank order (dropping self-refs), replaces the
// movie_recommendations join, and stamps enrichment_recs_synced_at — ALL in one tx. The stub
// upserts run BEFORE MovieRecsWriter.Set so the join's recommended_movie_id FK is always
// satisfied (F-Ф1-12; no 23503). Empty recs clears the set + stamps (prevents re-fire storms).
func (w *MovieWorker) writeRecommendations(ctx context.Context, movieID domain.MovieID, resp *tmdb.MovieResponse) error {
	stubs, recOrder := tmdb.MapMovieRecommendations(resp)

	// Sort a COPY by tmdb_id ASC for a global lock order on the shared `movies` table (B-26
	// deadlock-avoidance). The recID slice is built from recOrder (original TMDB rank) so the
	// join positions stay TMDB-ranked.
	sortedStubs := make([]movie.Canon, len(stubs))
	copy(sortedStubs, stubs)
	slices.SortStableFunc(sortedStubs, func(a, b movie.Canon) int {
		return compareTMDBID(a.TMDBID, b.TMDBID)
	})

	now := w.deps.Clock()
	return w.deps.Tx.Transaction(ctx, func(txCtx context.Context) error {
		idByTMDB := make(map[domain.TMDBID]domain.MovieID, len(sortedStubs))
		for _, stub := range sortedStubs {
			id, err := w.deps.Movies.UpsertStub(txCtx, stub)
			if err != nil {
				return fmt.Errorf("upsert recommendation stub: %w", err)
			}
			if stub.TMDBID != nil {
				idByTMDB[*stub.TMDBID] = id
			}
		}

		recIDs := make([]domain.MovieID, 0, len(recOrder))
		for _, recTMDBID := range recOrder {
			recMovieID, ok := idByTMDB[recTMDBID]
			if !ok {
				continue
			}
			if recMovieID == movieID {
				continue // self-ref — TMDB occasionally lists the parent in its own recs
			}
			recIDs = append(recIDs, recMovieID)
		}

		if err := w.deps.Recs.Set(txCtx, movieID, recIDs); err != nil {
			return fmt.Errorf("set movie_recommendations: %w", err)
		}
		return w.deps.Movies.MarkRecsSynced(txCtx, movieID, now)
	})
}
