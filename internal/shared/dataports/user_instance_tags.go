package dataports

import (
	"context"

	admin "github.com/alexmorbo/seasonfill/internal/admin/domain"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
)

// UserInstanceTagRepository persists the per-(user, instance) Sonarr
// tag cache used by the N-4 sf-<user> discovery TagResolver. D-5
// (466a) ships the repository with no production callers yet — the
// schema is exercised by tests and the consumer is wired by N-4.
//
// Get returns ports.ErrNotFound joined with sharedErrors.UserNotFoundError
// when the row does not exist (chosen so the wire envelope follows the
// same "user_not_found" code as the parent users path; a dedicated
// UserInstanceTagNotFoundError is deferred until N-4 picks the wire
// shape).
type UserInstanceTagRepository interface {
	Get(ctx context.Context, userID uint, instanceName domain.InstanceName) (admin.UserInstanceTag, error)
	Upsert(ctx context.Context, t admin.UserInstanceTag) error
	DeleteByUser(ctx context.Context, userID uint) error
}
