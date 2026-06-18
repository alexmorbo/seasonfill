package errors

import (
	"fmt"
	"time"
)

// TMDBRateLimitedError signals the TMDB API returned 429 with a
// Retry-After hint. Maps to HTTP 429; callers should back off and retry
// after RetryAfter elapses.
type TMDBRateLimitedError struct {
	RetryAfter time.Duration
}

func (e *TMDBRateLimitedError) Error() string {
	secs := int64(e.RetryAfter.Round(time.Second).Seconds())
	return fmt.Sprintf("tmdb rate limited; retry after %ds", secs)
}

func (e *TMDBRateLimitedError) Code() string { return "tmdb_rate_limited" }

func (e *TMDBRateLimitedError) Retriable() bool { return true }

// TMDBAuthError signals a TMDB API key rejection (401/403). Maps to
// HTTP 502 (upstream auth misconfiguration, not the caller's fault).
type TMDBAuthError struct{}

func (e *TMDBAuthError) Error() string { return "tmdb authentication failed" }

func (e *TMDBAuthError) Code() string { return "tmdb_auth" }

func (e *TMDBAuthError) Retriable() bool { return false }

// TMDBNotFoundError signals TMDB returned 404 for the requested ID.
// Maps to HTTP 404.
type TMDBNotFoundError struct {
	ID int
}

func (e *TMDBNotFoundError) Error() string {
	return fmt.Sprintf("tmdb id %d not found", e.ID)
}

func (e *TMDBNotFoundError) Code() string { return "tmdb_not_found" }

func (e *TMDBNotFoundError) Retriable() bool { return false }
