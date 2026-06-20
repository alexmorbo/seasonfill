package webhook

import (
	"context"
	"errors"

	grab "github.com/alexmorbo/seasonfill/internal/grab/domain"
	ports "github.com/alexmorbo/seasonfill/internal/shared/dataports"
	sharedErrors "github.com/alexmorbo/seasonfill/internal/shared/errors"
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

// ErrorKind returns a stable, low-cardinality Prometheus label. Typed
// not-found errors get specific labels (instance_not_found / grab_not_found)
// so the operator can attribute volume by domain; everything else still
// folds into "not_found" to keep cardinality bounded.
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
	}
	var (
		instanceNF *sharedErrors.InstanceNotFoundError
		grabNF     *sharedErrors.GrabNotFoundError
	)
	switch {
	case errors.As(err, &instanceNF):
		return "instance_not_found"
	case errors.As(err, &grabNF):
		return "grab_not_found"
	case errors.Is(err, ports.ErrNotFound):
		return "not_found"
	case errors.Is(err, grab.ErrInvalidStatusTransition):
		return "invalid_transition"
	}
	return "other"
}
