package instance

import "time"

// Health is the current health state for one Sonarr instance.
type Health string

const (
	HealthAvailable          Health = "Available"
	HealthSelfThrottled      Health = "SelfThrottled"
	HealthUnavailableAuth    Health = "UnavailableAuth"
	HealthUnavailableNetwork Health = "UnavailableNetwork"
	HealthUnavailableUnknown Health = "UnavailableUnknown"
)

// IsAvailable reports whether scans may proceed for this instance.
// SelfThrottled counts as available — the backend itself is reachable,
// we just hit our own rate-limiter queue. Scans/probes continue; the
// watchdog short-circuits on this state via an explicit branch.
func (h Health) IsAvailable() bool { return h == HealthAvailable || h == HealthSelfThrottled }

// IsUnavailable is the inverse of IsAvailable.
func (h Health) IsUnavailable() bool { return !h.IsAvailable() }

// Snapshot is the externally-visible per-instance state.
type Snapshot struct {
	Name             string
	Health           Health
	LastCheckAt      time.Time
	LastError        string
	TransitionsCount int
}

// HealthRecord is a back-compat alias for Snapshot. The old type was a
// struct with `Name`, `Status`, `LastError`, `CheckedAt`; on the Snapshot
// shape these map to Name/Health/LastError/LastCheckAt.
type HealthRecord = Snapshot
