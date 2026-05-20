package domain

import "errors"

// Sentinel errors propagated across layer boundaries. Wrap with %w when adding
// context. Story 004 will add ErrInstanceUnauthorized alongside these.
var (
	// ErrCooldownActive indicates a guid or series is currently in cooldown.
	ErrCooldownActive = errors.New("cooldown active")

	// ErrGrabFailed indicates POST /api/v3/release failed after all retries.
	ErrGrabFailed = errors.New("grab failed after retries")

	// ErrTransientSonarr classifies an upstream error as retry-eligible
	// (5xx, network/DNS, timeout).
	ErrTransientSonarr = errors.New("transient sonarr error")

	// ErrPermanentSonarr classifies an upstream error as terminal (4xx).
	ErrPermanentSonarr = errors.New("permanent sonarr error")
)
