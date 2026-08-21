package admin

import (
	"time"

	"github.com/alexmorbo/seasonfill/internal/shared/domain"
)

// UserInstanceTag is the (user, instance) → sf-<user> arr tag mapping
// cached by the discovery TagResolver.
//
// ArrTagID/ArrTagLabel are arr-NEUTRAL (R-6): arr_instance.name is a
// globally unique PK carrying a `type` discriminator, so an instance is
// either Sonarr or Radarr and (user_id, instance_name) already pins the
// arr kind. One row per (user, instance) serves both verticals — no
// per-arr column pair (which would also break the NOT NULL +
// UNIQUE (instance_name, label) guard on the other arr's rows).
type UserInstanceTag struct {
	UserID       uint
	InstanceName domain.InstanceName
	ArrTagID     int
	ArrTagLabel  string
	CreatedAt    time.Time
	UpdatedAt    time.Time
}
