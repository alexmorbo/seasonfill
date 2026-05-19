package instance

import "time"

type Status string

const (
	StatusUnknown     Status = "unknown"
	StatusAvailable   Status = "available"
	StatusUnavailable Status = "unavailable"
)

type Health struct {
	Name      string
	Status    Status
	LastError string
	CheckedAt time.Time
}
