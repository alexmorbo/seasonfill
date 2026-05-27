// Package reload — shared types used across multiple subscribers.
package reload

import "github.com/alexmorbo/seasonfill/application/ports"

// HealthChecker is the (small) subset of healthcheck.Checker the
// reload fan-out needs. Names drive registry membership; clients drive
// the periodic preflight loop. The caller MUST pass both derived from
// the same source so they cannot disagree.
type HealthChecker interface {
	ReplaceClients(clients []ports.SonarrClient, names []string)
}
