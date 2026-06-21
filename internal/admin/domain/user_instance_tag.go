package admin

import (
	"time"

	"github.com/alexmorbo/seasonfill/internal/shared/domain"
)

// UserInstanceTag is the (user, instance) → sf-<user> Sonarr tag
// mapping cached by the discovery resolver (N-4 future consumer).
// D-5 (466a) ships the entity + repository with no production
// callers yet; the schema is exercised by tests only.
type UserInstanceTag struct {
	UserID         uint
	InstanceName   domain.InstanceName
	SonarrTagID    int
	SonarrTagLabel string
	CreatedAt      time.Time
	UpdatedAt      time.Time
}
