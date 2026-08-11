package errors

import (
	"fmt"

	"github.com/alexmorbo/seasonfill/internal/shared/domain"
)

// MovieNotFoundError signals a missing movie in the canonical store
// (Ф6-R-3). Maps to HTTP 404. Mirrors SeriesNotFoundError.
type MovieNotFoundError struct {
	ID domain.MovieID
}

func (e *MovieNotFoundError) Error() string {
	if e.ID != 0 {
		return fmt.Sprintf("movie %d not found", e.ID)
	}
	return "movie not found"
}

func (e *MovieNotFoundError) Code() string { return "movie_not_found" }

func (e *MovieNotFoundError) Retriable() bool { return false }
