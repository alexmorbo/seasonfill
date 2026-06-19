package errors

import (
	"fmt"

	"github.com/alexmorbo/seasonfill/internal/shared/domain"
)

// AdminUserNotFoundError signals the singleton admin_users row is missing.
// Reachable from auth handlers, OIDC callback, and password reset CLI.
// Maps to HTTP 404.
type AdminUserNotFoundError struct{}

func (e *AdminUserNotFoundError) Error() string { return "admin user not found" }

func (e *AdminUserNotFoundError) Code() string { return "admin_user_not_found" }

func (e *AdminUserNotFoundError) Retriable() bool { return false }

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
