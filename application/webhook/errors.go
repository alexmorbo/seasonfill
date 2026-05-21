package webhook

import (
	"context"
	"errors"

	"github.com/alexmorbo/seasonfill/application/ports"
	"github.com/alexmorbo/seasonfill/domain/grab"
)

// IsTransient reports whether a UseCase.Process error maps to HTTP
// 500 (Sonarr retries) instead of HTTP 200 + metric.
//
// Transient = wrapping ports.ErrDBUnavailable, context.DeadlineExceeded,
// or context.Canceled. Everything else is logic (default — protects
// Sonarr's retry buffer per Q-4).
func IsTransient(err error) bool {
	if err == nil {
		return false
	}
	return errors.Is(err, ports.ErrDBUnavailable) ||
		errors.Is(err, context.DeadlineExceeded) ||
		errors.Is(err, context.Canceled)
}

// ErrorKind returns a stable, low-cardinality Prometheus label.
func ErrorKind(err error) string {
	switch {
	case err == nil:
		return ""
	case errors.Is(err, ports.ErrDBUnavailable):
		return "db_unavailable"
	case errors.Is(err, context.DeadlineExceeded):
		return "timeout"
	case errors.Is(err, context.Canceled):
		return "canceled"
	case errors.Is(err, ports.ErrNotFound):
		return "not_found"
	case errors.Is(err, grab.ErrInvalidStatusTransition):
		return "invalid_transition"
	default:
		return "other"
	}
}
