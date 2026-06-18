package errors

import "fmt"

// SeriesNotFoundError signals a missing series in the canonical store.
// Maps to HTTP 404.
type SeriesNotFoundError struct {
	ID int64
}

func (e *SeriesNotFoundError) Error() string {
	return fmt.Sprintf("series %d not found", e.ID)
}

func (e *SeriesNotFoundError) Code() string { return "series_not_found" }

func (e *SeriesNotFoundError) Retriable() bool { return false }

// SeriesCanonicalLoadError signals a transient failure loading canonical
// series data (DB hiccup, cache miss with backing store error, etc.).
// Maps to HTTP 500; callers should retry.
type SeriesCanonicalLoadError struct {
	ID    int64
	Cause error
}

func (e *SeriesCanonicalLoadError) Error() string {
	if e.Cause == nil {
		return fmt.Sprintf("series %d canonical load failed", e.ID)
	}
	return fmt.Sprintf("series %d canonical load failed: %v", e.ID, e.Cause)
}

func (e *SeriesCanonicalLoadError) Code() string { return "series_canonical_load" }

func (e *SeriesCanonicalLoadError) Retriable() bool { return true }

func (e *SeriesCanonicalLoadError) Unwrap() error { return e.Cause }
