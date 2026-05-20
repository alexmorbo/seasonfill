package domain

import "errors"

// Sentinel errors propagated across layer boundaries. Wrap with %w when adding
// context.
var (
	// ErrCooldownActive indicates a guid or series is currently in cooldown.
	ErrCooldownActive = errors.New("cooldown active")

	// ErrGrabFailed indicates POST /api/v3/release failed after all retries.
	ErrGrabFailed = errors.New("grab failed after retries")

	// ErrTransientSonarr classifies an upstream error as retry-eligible
	// (5xx, 408, 429, network/DNS, timeout).
	ErrTransientSonarr = errors.New("transient sonarr error")

	// ErrPermanentSonarr classifies an upstream error as terminal (4xx
	// excluding 408/429).
	ErrPermanentSonarr = errors.New("permanent sonarr error")

	// ErrInstanceUnauthorized wraps 401/403 responses from Sonarr. The scan
	// loop in 004c aborts on this and the watchdog transitions the instance
	// to UnavailableAuth.
	ErrInstanceUnauthorized = errors.New("sonarr instance unauthorized")

	// ErrInstanceNetwork wraps DNS / connect / timeout / persistent network
	// errors. 004b/004c promote instances seeing this to UnavailableNetwork.
	ErrInstanceNetwork = errors.New("sonarr instance network error")

	// ErrInstanceUnavailable is returned by the scan loop (004c) when a scan
	// is requested for an instance in an Unavailable* state.
	ErrInstanceUnavailable = errors.New("sonarr instance unavailable")
)
