package movie

import "time"

// Video is the persistence-neutral movie trailer value (Ф1.1c). One "best" trailer is
// selected per movie by the tmdb mapper (pickBestTrailer) and persisted by
// MovieVideosRepository.ReplaceBestTrailer. Fields mirror the movie_videos columns
// (mig 60); nil pointers map to SQL NULL. Kept in the movie catalog domain so BOTH the
// tmdb mapper and the enrichment persistence writer can name it without a cross-package
// DTO or a wiring adapter (the port method takes *movie.Video directly).
type Video struct {
	TMDBVideoID *string
	Name        string
	Site        *string
	Key         *string
	Type        *string
	Official    bool
	Language    *string
	PublishedAt *time.Time
}
