package enrichment

import (
	"context"
	"fmt"
	"log/slog"
	"slices"

	"github.com/alexmorbo/seasonfill/internal/catalog/domain/movie"
	"github.com/alexmorbo/seasonfill/internal/shared/clients/tmdb"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
)

// RefreshRecommendations fetches /movie/{id}?language={lang} and writes the
// per-language recommendation TITLES (movie_i18n.title) for each recommended movie —
// the gap writeRecommendations (HandleForced) leaves. writeRecommendations upserts
// the rec stubs + the movie_recommendations join + enrichment_recs_synced_at, but
// movie titles for a NON-base language live in the movie_i18n side-table, which it
// never touches. GATE-ZERO F-05 proved TMDB localizes recommendations.results[].title
// (movie 787 ru-RU → «Мистер и миссис Смит» + Cyrillic rec titles), so we persist
// them so the next /movies/{tmdb}/recommendations?lang=X cold read serves localized
// rail titles without a per-rec fetch. This is the movie mirror of
// SeriesWorker.RefreshRecommendations' series_texts.Upsert side-effect.
//
// ONE TMDB call. The tx re-upserts each rec stub (idempotent, sorted tmdb_id ASC for
// a global lock order on `movies` — B-26 deadlock avoidance) to resolve the rec's
// movie_id (the movie_i18n FK), writes movie_i18n.{rec_movie_id, lang}.title from the
// localized rec title (blank → skip, COALESCE-safe so a nil never wipes a stored
// value; overview/tagline empty → NULL preserves; poster/backdrop untouched per
// F-06), replaces the movie_recommendations join, and stamps enrichment_recs_synced_at
// — ALL in one Transactor tx. On any failure the tx rolls back so the stamp is NEVER
// written without a successful title-drain.
//
// A movie with no tmdb id, or a worker wired without I18n/Recs/Tx, is a clean no-op.
// Empty recs clears the join + stamps (anti-storm: prevents Probe/engine re-fire for a
// movie TMDB genuinely has no recommendations for). Driven by the ADR-0022 engine recs
// plugin on a movie recommendations open for a non-base lang; idempotent + COALESCE-safe
// so the engine may coalesce/retry freely. Self-references (TMDB occasionally lists the
// parent among its own recs) are dropped from both the title drain AND the join set.
func (w *MovieWorker) RefreshRecommendations(ctx context.Context, movieID domain.MovieID, lang string) error {
	if w.deps.I18n == nil || w.deps.Recs == nil || w.deps.Tx == nil {
		// Rec-title drain not wired (cold-boot / opt-out tests) — no-op.
		return nil
	}
	log := w.deps.Logger.With(
		slog.String("op", "movie_refresh_recommendations"),
		slog.Int64("movie_id", int64(movieID)),
		slog.String("language", lang),
	)

	canon, err := w.deps.Movies.Get(ctx, movieID)
	if err != nil {
		return fmt.Errorf("movie refresh_recommendations: load canon %d: %w", movieID, err)
	}
	if canon.TMDBID == nil {
		log.DebugContext(ctx, "enrichment.movie.refresh_recommendations.no_tmdb_id_skip")
		return nil
	}

	resp, err := w.deps.TMDB.GetMovie(ctx, int64(*canon.TMDBID), lang)
	if err != nil {
		return fmt.Errorf("movie refresh_recommendations: GetMovie(lang=%s): %w", lang, err)
	}

	// stubs (title dropped by MapMovieRecommendations — S-E3a) + TMDB-rank order,
	// reusing the SAME mapper writeRecommendations uses. Sorted stubs give the global
	// lock order; recOrder preserves TMDB rank for the join. The localized title per
	// rec is read straight from the response (the mapper does not carry it).
	stubs, recOrder := tmdb.MapMovieRecommendations(resp)
	titleByTMDB := make(map[domain.TMDBID]string, len(recOrder))
	if resp != nil && resp.Recommendations != nil {
		for _, r := range resp.Recommendations.Results {
			titleByTMDB[domain.TMDBID(r.ID)] = r.Title
		}
	}

	sortedStubs := make([]movie.Canon, len(stubs))
	copy(sortedStubs, stubs)
	slices.SortStableFunc(sortedStubs, func(a, b movie.Canon) int {
		return compareTMDBID(a.TMDBID, b.TMDBID)
	})

	now := w.deps.Clock()
	var titlesWritten int
	err = w.deps.Tx.Transaction(ctx, func(txCtx context.Context) error {
		idByTMDB := make(map[domain.TMDBID]domain.MovieID, len(sortedStubs))
		for _, stub := range sortedStubs {
			id, uerr := w.deps.Movies.UpsertStub(txCtx, stub)
			if uerr != nil {
				return fmt.Errorf("upsert recommendation stub: %w", uerr)
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
				continue // self-ref — drop from BOTH title drain AND join slice
			}
			// TITLE-only per-lang drain. Blank title → skip so the COALESCE upsert
			// never wipes a stored value. overview/tagline empty → NULL (COALESCE
			// preserves); poster/backdrop nil (rec posters untouched — F-06).
			if title := titleByTMDB[recTMDBID]; title != "" {
				if uerr := w.deps.I18n.UpsertEnriched(txCtx, recMovieID, lang, title, "", "", nil, nil, now); uerr != nil {
					return fmt.Errorf("upsert movie_i18n rec title (rec_movie_id=%d): %w", recMovieID, uerr)
				}
				titlesWritten++
			}
			recIDs = append(recIDs, recMovieID)
		}

		if serr := w.deps.Recs.Set(txCtx, movieID, recIDs); serr != nil {
			return fmt.Errorf("set movie_recommendations: %w", serr)
		}
		// Stamp even for empty/all-blank recs: "checked, empty" records a timestamp so
		// the engine recs plugin's recheck window gates the next open (anti-storm),
		// mirroring writeRecommendations' unconditional MarkRecsSynced.
		return w.deps.Movies.MarkRecsSynced(txCtx, movieID, now)
	})
	if err != nil {
		return fmt.Errorf("movie refresh_recommendations: tx: %w", err)
	}

	log.InfoContext(ctx, "enrichment.movie.refresh_recommendations.ok",
		slog.Int("recs_count", len(recOrder)),
		slog.Int("titles_written", titlesWritten))
	return nil
}
