package admin

import "github.com/alexmorbo/seasonfill/internal/shared/domain"

// UserInstanceAccess is a per-(user, instance) request-ACL row. Ф8-U-1
// ships the schema + repo as FOUNDATION with no production consumer yet
// — U-2 (request-workflow) and U-5 (per-user retrofit) wire the callers.
//
// Distinct from UserInstanceTag (audit F-03): that is the sf-<user> tag
// cache for the discovery TagResolver; THIS is authorization ("may this
// user request on this instance"). Keeping them separate prevents a tag
// rename in Sonarr from silently mutating access rights.
//
// InstanceName is a plain logical name (TEXT), NOT an FK to arr_instance
// — an access row may reference an instance name that is not (yet) a live
// arr_instance row. Only user_id is FK-constrained (CASCADE on user
// delete). See story DEVIATION D-1.
type UserInstanceAccess struct {
	UserID       uint
	InstanceName domain.InstanceName
	CanRequest   bool
}
