package instance

import "time"

// Health is the current health state for one Sonarr instance.
type Health string

const (
	HealthAvailable          Health = "Available"
	HealthUnavailableAuth    Health = "UnavailableAuth"
	HealthUnavailableNetwork Health = "UnavailableNetwork"
	HealthUnavailableUnknown Health = "UnavailableUnknown"
)

// Legacy aliases — kept so any straggler caller still compiles. New code uses
// the typed Health* constants directly.
type Status = Health

const (
	StatusUnknown     = HealthUnavailableUnknown
	StatusAvailable   = HealthAvailable
	StatusUnavailable = HealthUnavailableUnknown
)

// IsAvailable reports whether scans may proceed for this instance.
func (h Health) IsAvailable() bool { return h == HealthAvailable }

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
