package app

import (
	"context"

	"github.com/alexmorbo/seasonfill/internal/shared/domain"
)

// MovieRatingsPage is the assembled ratings slice for a movie, read straight off
// the canon row (no live TMDB/OMDb). Every field is a nil-able pointer: nil ⇒
// the source carries no value for it. There is no lang/served_language envelope —
// ratings are not localized. No Rotten Tomatoes / Metacritic (not a movies-row
// column; parity with the series ratings endpoint).
type MovieRatingsPage struct {
	TMDBRating *float64
	TMDBVotes  *int
	IMDBRating *float64
	IMDBVotes  *int
	Rated      *string // movies.omdb_rated
	Awards     *string // movies.omdb_awards
}

// RatingsUseCase assembles the ratings slice for a movie from the local canon
// read port. Read-only: no SWR, no refresher — the movie vertical has no ratings
// refresher wired (unlike the series SWR usecase). The handler derives the
// per-source freshness (fresh/unavailable) from the presence of each value.
type RatingsUseCase struct {
	canon CanonReader
}

// NewRatingsUseCase constructs the ratings usecase over the canon read port. In
// the live wiring canon = *enrichpersistence.MovieRepository.
func NewRatingsUseCase(canon CanonReader) *RatingsUseCase {
	return &RatingsUseCase{canon: canon}
}

// Get loads the canon row for a tmdb id and returns its ratings slice.
// ports.ErrNotFound bubbles when no canon row exists (→ handler 404). All values
// are copied as-is (nil-preserving) from the canon row.
func (uc *RatingsUseCase) Get(ctx context.Context, tmdbID domain.TMDBID) (*MovieRatingsPage, error) {
	canon, err := uc.canon.GetByTMDBID(ctx, tmdbID)
	if err != nil {
		return nil, err // ports.ErrNotFound bubbles
	}
	return &MovieRatingsPage{
		TMDBRating: canon.TMDBRating,
		TMDBVotes:  canon.TMDBVotes,
		IMDBRating: canon.IMDBRating,
		IMDBVotes:  canon.IMDBVotes,
		Rated:      canon.OMDBRated,
		Awards:     canon.OMDBAwards,
	}, nil
}
