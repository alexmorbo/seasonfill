package app

import (
	"context"

	"github.com/alexmorbo/seasonfill/internal/catalog/domain/movie"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
)

// MovieRecsReader lists the recommended canonical movie ids for a parent movie
// in TMDB rank order (position ASC). Impl:
// *enrichpersistence.MovieRecommendationsRepository.
type MovieRecsReader interface {
	ListByMovie(ctx context.Context, movieID domain.MovieID) ([]domain.MovieID, error)
}

// MovieCanonBatchReader resolves canon rows for a set of canonical movie ids in
// ONE query (no N+1). The returned slice order is NOT relied upon — the usecase
// reorders by the caller-supplied id sequence. Impl:
// *enrichpersistence.MovieRepository (ListByIDs, added in Ф2.4).
type MovieCanonBatchReader interface {
	ListByIDs(ctx context.Context, ids []domain.MovieID) ([]movie.Canon, error)
}

// MovieRecTitleLocalizer batch-reads localized (non-empty) movie titles keyed by
// tmdb_id for a set of tmdb ids in ONE query (no N+1), via the requested → en-US
// → any-language ladder. Ids with no localized title are absent from the map (the
// usecase then keeps the canon EN title). Impl:
// *enrichpersistence.MovieI18nReadRepository.ListTitlesByTMDBIDsWithFallback.
// nil-OK — an unwired localizer leaves every rec title as canon (Story U-3).
type MovieRecTitleLocalizer interface {
	ListTitlesByTMDBIDsWithFallback(ctx context.Context, tmdbIDs []int, lang string) (map[int]string, error)
}

// Pagination bounds. The handler returns 400 on an out-of-range ?limit; the
// usecase re-clamps defensively so direct/internal callers stay safe.
const (
	MovieRecommendationsLimitDefault = 20
	MovieRecommendationsLimitMax     = 50
	MovieRecommendationsLimitMin     = 1
)

// MovieRecommendationItem is one resolved rec: the canonical movie row. The REST
// layer projects tmdb_id/title/year/rating and resolves the poster path. Title is
// staged here (canon.Title, or the localized title when a ?lang= request resolves
// a movie_i18n row) so the shape mirrors seriesdetail.RecommendationDetail.
type MovieRecommendationItem struct {
	Canon movie.Canon
	Title string
}

// MovieRecommendationsPage is the assembled, paginated recs slice for a movie.
// TotalCount is the count of RENDERABLE recs (canon resolvable AND tmdb-linkable)
// — stubs that never materialised, or rows with no tmdb_id, are silently skipped
// (stub-skip parity with seriesdetail.GetRecommendations).
type MovieRecommendationsPage struct {
	TMDBID     domain.TMDBID
	MovieID    domain.MovieID
	Items      []MovieRecommendationItem
	TotalCount int
	HasMore    bool
	Degraded   []string
}

// RecommendationsUseCase assembles the movie recs slice from local read ports.
// Read-only: no live TMDB, no SWR. Mirrors RatingsUseCase (no logger) + the
// series recommendations composer's stub-hydration/order-preservation branch.
type RecommendationsUseCase struct {
	canon  CanonReader
	recs   MovieRecsReader
	movies MovieCanonBatchReader
	titles MovieRecTitleLocalizer
}

// NewRecommendationsUseCase constructs the usecase. In the live wiring canon and
// movies are the same *enrichpersistence.MovieRepository (GetByTMDBID + ListByIDs),
// recs is *enrichpersistence.MovieRecommendationsRepository, and titles is
// *enrichpersistence.MovieI18nReadRepository (the same reader that drives the
// movie detail/cast/overview localization). titles nil-OK — unwired leaves canon
// titles untouched.
func NewRecommendationsUseCase(
	canon CanonReader,
	recs MovieRecsReader,
	movies MovieCanonBatchReader,
	titles MovieRecTitleLocalizer,
) *RecommendationsUseCase {
	return &RecommendationsUseCase{canon: canon, recs: recs, movies: movies, titles: titles}
}

// Get returns the paginated recs page for a tmdb id. ports.ErrNotFound bubbles
// when the base movie has no canon row (→ handler 404). A recs-list failure
// degrades (Degraded=["tmdb_movie"]) with an empty page rather than failing the
// response — a cold/slow local recs table is the same UX as a slow blurb (series
// §3). A canon batch failure degrades quietly to an empty page (mirrors the series
// batch-fail path, which does NOT over-report a tmdb tag for a local lookup).
//
// lang (Story U-3) — BCP-47 tag used to override each rec's canon EN Title with
// the localized movie_i18n row (requested → en-US → any ladder) when present.
// Empty lang skips localization entirely (internal callers get canon titles). A
// localizer read failure degrades QUIETLY to canon titles — no 500, no new
// degraded tag — mirroring the silent local canon-batch-fail branch below (this
// usecase carries no logger, same as RatingsUseCase).
//
// limit/offset are re-clamped here so the method is safe to call directly.
func (uc *RecommendationsUseCase) Get(
	ctx context.Context,
	tmdbID domain.TMDBID,
	lang string,
	limit, offset int,
) (*MovieRecommendationsPage, error) {
	if limit <= 0 {
		limit = MovieRecommendationsLimitDefault
	}
	if limit > MovieRecommendationsLimitMax {
		limit = MovieRecommendationsLimitMax
	}
	if offset < 0 {
		offset = 0
	}

	base, err := uc.canon.GetByTMDBID(ctx, tmdbID)
	if err != nil {
		return nil, err // ports.ErrNotFound bubbles → 404
	}

	out := &MovieRecommendationsPage{
		TMDBID:   tmdbID,
		MovieID:  base.ID,
		Items:    []MovieRecommendationItem{},
		Degraded: []string{},
	}

	ids, err := uc.recs.ListByMovie(ctx, base.ID)
	if err != nil {
		out.Degraded = append(out.Degraded, "tmdb_movie")
		return out, nil //nolint:nilerr // intentional: recs-list failure degrades to an empty page (tmdb_movie flag), never 500
	}
	if len(ids) == 0 {
		return out, nil
	}

	canons, err := uc.movies.ListByIDs(ctx, ids)
	if err != nil {
		// Local canon lookup failed — degrade quietly to an empty page (the
		// series batch-fail branch swallows the error the same way). No
		// tmdb_movie tag: the failing read is the local canon, not TMDB.
		return out, nil //nolint:nilerr // intentional: local canon batch failure degrades silently to an empty page
	}

	byID := make(map[domain.MovieID]movie.Canon, len(canons))
	for _, c := range canons {
		byID[c.ID] = c
	}

	// Order preservation: iterate ids in TMDB-rank sequence; skip unresolved
	// stubs and rows with no tmdb_id (unlinkable → not renderable).
	resolved := make([]MovieRecommendationItem, 0, len(ids))
	for _, id := range ids {
		c, ok := byID[id]
		if !ok || c.TMDBID == nil {
			continue
		}
		resolved = append(resolved, MovieRecommendationItem{Canon: c, Title: c.Title})
	}

	// Story U-3 — localize rec titles by thread language. Batch-read the localized
	// (non-empty) titles for the resolved tmdb ids in ONE query (requested → en-US
	// → any ladder) and override canon EN where a localized title exists. Empty
	// lang or an unwired localizer skips the query (canon titles). A read failure
	// is swallowed: keep canon titles, add NO degraded tag (this usecase has no
	// logger — same silent handling as the local canon-batch-fail branch above).
	uc.localizeTitles(ctx, lang, resolved)

	out.TotalCount = len(resolved)
	if offset >= len(resolved) {
		return out, nil
	}
	end := min(offset+limit, len(resolved))
	out.Items = resolved[offset:end]
	out.HasMore = end < len(resolved)
	return out, nil
}

// localizeTitles overrides each resolved rec's canon EN Title with the localized
// movie_i18n title for the requested lang when one exists. It mutates items in
// place. No-op when lang is empty, the localizer is unwired, there are no items,
// or the batch read fails (fallback to canon, never blank). Every resolved item
// carries a non-nil Canon.TMDBID (the resolve loop skips nil-tmdb rows) — the
// nil guard here is purely defensive.
func (uc *RecommendationsUseCase) localizeTitles(ctx context.Context, lang string, items []MovieRecommendationItem) {
	if lang == "" || uc.titles == nil || len(items) == 0 {
		return
	}
	tmdbIDs := make([]int, 0, len(items))
	for i := range items {
		if items[i].Canon.TMDBID != nil {
			tmdbIDs = append(tmdbIDs, int(*items[i].Canon.TMDBID))
		}
	}
	if len(tmdbIDs) == 0 {
		return
	}
	localized, err := uc.titles.ListTitlesByTMDBIDsWithFallback(ctx, tmdbIDs, lang)
	if err != nil {
		return // degrade quietly: keep canon titles, no tag (no logger on this usecase)
	}
	for i := range items {
		if items[i].Canon.TMDBID == nil {
			continue
		}
		if t, ok := localized[int(*items[i].Canon.TMDBID)]; ok && t != "" {
			items[i].Title = t
		}
	}
}
