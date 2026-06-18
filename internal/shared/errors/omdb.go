package errors

import (
	"fmt"
	"time"
)

// OMDbQuotaExhaustedError signals the daily OMDb quota was depleted.
// Maps to HTTP 503 — quota resets daily, so a same-day retry is futile;
// Retriable() returns false to discourage tight retry loops.
type OMDbQuotaExhaustedError struct {
	ResetAt time.Time
}

func (e *OMDbQuotaExhaustedError) Error() string {
	return fmt.Sprintf("omdb quota exhausted; resets at %s", e.ResetAt.UTC().Format(time.RFC3339))
}

func (e *OMDbQuotaExhaustedError) Code() string { return "omdb_quota_exhausted" }

func (e *OMDbQuotaExhaustedError) Retriable() bool { return false }

// OMDbNotFoundError signals OMDb returned no record for the IMDB id.
// Maps to HTTP 404.
type OMDbNotFoundError struct {
	IMDBID string
}

func (e *OMDbNotFoundError) Error() string {
	return fmt.Sprintf("omdb imdb id %q not found", e.IMDBID)
}

func (e *OMDbNotFoundError) Code() string { return "omdb_not_found" }

func (e *OMDbNotFoundError) Retriable() bool { return false }

// OMDbAuthError signals an OMDb API key rejection. Maps to HTTP 502.
type OMDbAuthError struct{}

func (e *OMDbAuthError) Error() string { return "omdb authentication failed" }

func (e *OMDbAuthError) Code() string { return "omdb_auth" }

func (e *OMDbAuthError) Retriable() bool { return false }
