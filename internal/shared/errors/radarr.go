package errors

import (
	"fmt"

	"github.com/alexmorbo/seasonfill/internal/shared/domain"
)

// RadarrUnreachableError signals a transport-layer failure talking to a
// Radarr instance (DNS, dial, TLS, 5xx upstream). Maps to HTTP 502.
// Mirrors SonarrUnreachableError (Ф6-R-3).
type RadarrUnreachableError struct {
	Instance domain.InstanceName
	Cause    error
}

func (e *RadarrUnreachableError) Error() string {
	if e.Cause == nil {
		return fmt.Sprintf("radarr instance %q unreachable", e.Instance)
	}
	return fmt.Sprintf("radarr instance %q unreachable: %v", e.Instance, e.Cause)
}

func (e *RadarrUnreachableError) Code() string { return "radarr_unreachable" }

func (e *RadarrUnreachableError) Retriable() bool { return true }

func (e *RadarrUnreachableError) Unwrap() error { return e.Cause }
