package errors

import (
	"fmt"

	"github.com/alexmorbo/seasonfill/internal/shared/domain"
)

// UserNotFoundError signals a missing row in the `users` table.
// Reachable from auth handlers, OIDC callback, and password reset CLI.
// Maps to HTTP 404. Greenfield D-5 rename of AdminUserNotFoundError.
type UserNotFoundError struct{}

func (e *UserNotFoundError) Error() string { return "user not found" }

func (e *UserNotFoundError) Code() string { return "user_not_found" }

func (e *UserNotFoundError) Retriable() bool { return false }

// InstanceNotFoundError signals an unknown Sonarr instance row. Distinct
// from SonarrInstanceInvalidError (400 — caller named an instance not in
// runtime config). InstanceNotFound is "name resolves but the row was
// already deleted between read and write" — 404.
type InstanceNotFoundError struct {
	Name domain.InstanceName
}

func (e *InstanceNotFoundError) Error() string {
	return fmt.Sprintf("instance %q not found", e.Name)
}

func (e *InstanceNotFoundError) Code() string { return "instance_not_found" }

func (e *InstanceNotFoundError) Retriable() bool { return false }
