package dataports

import (
	"context"

	admin "github.com/alexmorbo/seasonfill/internal/admin/domain"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
)

// UserInstanceAccessRepository persists the per-(user, instance)
// request-ACL used by the Ф8 request-workflow. U-1 ships the repository
// as FOUNDATION with no production caller yet — U-2/U-5 wire consumers.
//
// Get returns ports.ErrNotFound joined with sharedErrors.UserNotFoundError
// when the row does not exist (same wire envelope as the parent users
// path; a dedicated not-found type is deferred until a consumer picks
// the wire shape).
type UserInstanceAccessRepository interface {
	Get(ctx context.Context, userID uint, instanceName domain.InstanceName) (admin.UserInstanceAccess, error)
	Upsert(ctx context.Context, a admin.UserInstanceAccess) error
	ListByUser(ctx context.Context, userID uint) ([]admin.UserInstanceAccess, error)
	DeleteByUser(ctx context.Context, userID uint) error
}
