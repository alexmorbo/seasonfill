package errors

import (
	"fmt"

	"github.com/alexmorbo/seasonfill/internal/shared/domain"
)

// SonarrUnreachableError signals a transport-layer failure talking to a
// Sonarr instance (DNS, dial, TLS, 5xx upstream). Maps to HTTP 502.
type SonarrUnreachableError struct {
	Instance domain.InstanceName
	Cause    error
}

func (e *SonarrUnreachableError) Error() string {
	if e.Cause == nil {
		return fmt.Sprintf("sonarr instance %q unreachable", e.Instance)
	}
	return fmt.Sprintf("sonarr instance %q unreachable: %v", e.Instance, e.Cause)
}

func (e *SonarrUnreachableError) Code() string { return "sonarr_unreachable" }

func (e *SonarrUnreachableError) Retriable() bool { return true }

func (e *SonarrUnreachableError) Unwrap() error { return e.Cause }

// SonarrInstanceInvalidError signals an unknown / misconfigured Sonarr
// instance reference (caller-supplied name doesn't match runtime config).
// Maps to HTTP 400.
type SonarrInstanceInvalidError struct {
	Instance domain.InstanceName
}

func (e *SonarrInstanceInvalidError) Error() string {
	return fmt.Sprintf("sonarr instance %q invalid", e.Instance)
}

func (e *SonarrInstanceInvalidError) Code() string { return "sonarr_instance_invalid" }

func (e *SonarrInstanceInvalidError) Retriable() bool { return false }
