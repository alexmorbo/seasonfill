package dataports

import (
	"context"

	admin "github.com/alexmorbo/seasonfill/internal/admin/domain"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
)

// UserInstanceTagRepository persists the per-(user, instance) arr tag
// cache used by the discovery TagResolver (sf-<user> labels). Arr-neutral
// since R-6: arr_instance.name is unique across Sonarr + Radarr, so one
// row per (user, instance) covers both verticals.
//
// Get returns ports.ErrNotFound joined with sharedErrors.UserNotFoundError
// when the row does not exist (chosen so the wire envelope follows the
// same "user_not_found" code as the parent users path).
type UserInstanceTagRepository interface {
	Get(ctx context.Context, userID uint, instanceName domain.InstanceName) (admin.UserInstanceTag, error)
	Upsert(ctx context.Context, t admin.UserInstanceTag) error
	DeleteByUser(ctx context.Context, userID uint) error
}
