// movie_worker_taxonomy.go — Ф1.1b movie taxonomy trio writers (genres / keywords /
// companies). Each mirrors the series applyTaxonomyForLanguage seed pattern
// (series_worker.go:1617) but seeds i18n under the SINGLE base language (movies fetch once
// at w.baseLang; /movie?language=baseLang returns genre/keyword names already localized), so
// no per-language TMDB round-trips are invented. Each writer runs in its own Transactor tx
// (parent dict seed + base-lang i18n + DELETE+INSERT join + keywords stamp), mirroring the
// Ф1.1a writeCast Transactor pattern.
package enrichment

import (
	"context"
	"fmt"
	"slices"

	"github.com/alexmorbo/seasonfill/internal/enrichment/domain/taxonomy"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
)

// writeGenres seeds the `genres` dict + base-lang genres_i18n for each genre, then replaces
// the movie_genres join. Genres are sorted by tmdb_id ASC before the seed loop so concurrent
// movie txes acquire row locks on the shared `genres` table in a global order (B-26). No
// dedicated stamp — genres have no section-sync column (documented, matches Ф1.1a unwired
// stamps). An empty slice clears the join (authoritative refresh).
func (w *MovieWorker) writeGenres(ctx context.Context, movieID domain.MovieID, genres []taxonomy.Genre) error {
	slices.SortStableFunc(genres, func(a, b taxonomy.Genre) int {
		return compareTMDBID(a.TMDBID, b.TMDBID)
	})
	return w.deps.Tx.Transaction(ctx, func(txCtx context.Context) error {
		ids := make([]int64, 0, len(genres))
		for _, g := range genres {
			id, err := w.deps.Genres.Upsert(txCtx, g)
			if err != nil {
				return fmt.Errorf("upsert movie genre: %w", err)
			}
			if g.Name != "" {
				if err := w.deps.Genres.UpsertI18n(txCtx, id, w.baseLang, g.Name); err != nil {
					return fmt.Errorf("upsert genres_i18n: %w", err)
				}
			}
			ids = append(ids, id)
		}
		return w.deps.Genres.SetMovie(txCtx, movieID, ids)
	})
}

// writeKeywords seeds the `keywords` dict + base-lang keywords_i18n, replaces the
// movie_keywords join, then stamps enrichment_keywords_synced_at — all atomic in one tx.
// Sorted by tmdb_id ASC for the same global-lock-order reason as genres.
func (w *MovieWorker) writeKeywords(ctx context.Context, movieID domain.MovieID, keywords []taxonomy.Keyword) error {
	slices.SortStableFunc(keywords, func(a, b taxonomy.Keyword) int {
		return compareTMDBID(a.TMDBID, b.TMDBID)
	})
	return w.deps.Tx.Transaction(ctx, func(txCtx context.Context) error {
		ids := make([]int64, 0, len(keywords))
		for _, k := range keywords {
			id, err := w.deps.Keywords.Upsert(txCtx, k)
			if err != nil {
				return fmt.Errorf("upsert movie keyword: %w", err)
			}
			if k.Name != "" {
				if err := w.deps.Keywords.UpsertI18n(txCtx, id, w.baseLang, k.Name); err != nil {
					return fmt.Errorf("upsert keywords_i18n: %w", err)
				}
			}
			ids = append(ids, id)
		}
		if err := w.deps.Keywords.SetMovie(txCtx, movieID, ids); err != nil {
			return err
		}
		return w.deps.Movies.MarkKeywordsSynced(txCtx, movieID, w.deps.Clock())
	})
}

// writeCompanies seeds the `production_companies` dict (name/logo/origin live on the dict
// row — no i18n table) and replaces the movie_companies join. No dedicated stamp.
func (w *MovieWorker) writeCompanies(ctx context.Context, movieID domain.MovieID, companies []taxonomy.ProductionCompany) error {
	return w.deps.Tx.Transaction(ctx, func(txCtx context.Context) error {
		ids := make([]int64, 0, len(companies))
		for _, c := range companies {
			id, err := w.deps.Companies.Upsert(txCtx, c)
			if err != nil {
				return fmt.Errorf("upsert movie company: %w", err)
			}
			ids = append(ids, id)
		}
		return w.deps.Companies.SetMovie(txCtx, movieID, ids)
	})
}
