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

// Pagination bounds. The handler returns 400 on an out-of-range ?limit; the
// usecase re-clamps defensively so direct/internal callers stay safe.
const (
	MovieRecommendationsLimitDefault = 20
	MovieRecommendationsLimitMax     = 50
	MovieRecommendationsLimitMin     = 1
)

// MovieRecommendationItem is one resolved rec: the canonical movie row. The REST
// layer projects tmdb_id/title/year/rating and resolves the poster path. Title is
// staged here (canon.Title) so the shape mirrors seriesdetail.RecommendationDetail.
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
}

// NewRecommendationsUseCase constructs the usecase. In the live wiring canon and
// movies are the same *enrichpersistence.MovieRepository (GetByTMDBID + ListByIDs)
// and recs is *enrichpersistence.MovieRecommendationsRepository.
func NewRecommendationsUseCase(canon CanonReader, recs MovieRecsReader, movies MovieCanonBatchReader) *RecommendationsUseCase {
	return &RecommendationsUseCase{canon: canon, recs: recs, movies: movies}
}

// Get returns the paginated recs page for a tmdb id. ports.ErrNotFound bubbles
// when the base movie has no canon row (→ handler 404). A recs-list failure
// degrades (Degraded=["tmdb_movie"]) with an empty page rather than failing the
// response — a cold/slow local recs table is the same UX as a slow blurb (series
// §3). A canon batch failure degrades quietly to an empty page (mirrors the series
// batch-fail path, which does NOT over-report a tmdb tag for a local lookup).
//
// limit/offset are re-clamped here so the method is safe to call directly.
func (uc *RecommendationsUseCase) Get(
	ctx context.Context,
	tmdbID domain.TMDBID,
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

	out.TotalCount = len(resolved)
	if offset >= len(resolved) {
		return out, nil
	}
	end := min(offset+limit, len(resolved))
	out.Items = resolved[offset:end]
	out.HasMore = end < len(resolved)
	return out, nil
}
