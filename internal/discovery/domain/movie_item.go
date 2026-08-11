package domain

import shareddomain "github.com/alexmorbo/seasonfill/internal/shared/domain"

// MovieItem is the discovery-domain movie row (movie analog of Item). A
// discover/trending/popular/search response materialises each TMDB movie into
// one MovieItem carrying only the fields a discovery list needs; the enriched
// canon columns are hydrated later by the movie refresh pipeline.
//
// Pointer fields encode "not present on the TMDB list row" (TMDBID is a
// pointer for symmetry with Item even though discovery always sets it).
type MovieItem struct {
	MovieID          shareddomain.MovieID
	TMDBID           *shareddomain.TMDBID
	Title            string
	Year             *int
	TMDBRating       *float64
	PosterPath       *string
	BackdropPath     *string
	OriginalLanguage *string
}
