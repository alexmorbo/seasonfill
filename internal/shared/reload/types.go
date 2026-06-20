// Package reload — shared types used across multiple subscribers.
package reload

import (
	"context"

	ports "github.com/alexmorbo/seasonfill/internal/shared/dataports"
)

// HealthChecker is the (small) subset of healthcheck.Checker the
// reload fan-out needs. Names drive registry membership; clients drive
// the periodic preflight loop. The caller MUST pass both derived from
// the same source so they cannot disagree. Preflight is invoked from
// the OnApplied closure so a CRUD-added instance does not wait for the
// next periodic tick before its LastCheckAt is populated.
type HealthChecker interface {
	ReplaceClients(clients []ports.SonarrClient, names []string)
	Preflight(ctx context.Context)
}
