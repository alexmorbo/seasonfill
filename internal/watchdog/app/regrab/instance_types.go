package regrab

import (
	"slices"

	"github.com/alexmorbo/seasonfill/internal/catalog/app/scan"
)

// SupportedInstanceTypes is regrab's EXPLICIT declaration of the
// arr_instance.type values it can run against (ADR-0025 F2, invariant
// loop_declares_types).
//
// Why this lives in the application layer and not in cmd/server/loops:
// "regrab only works on sonarr instances" is domain knowledge of the
// regrab use case, not a property of the polling goroutine. The use case
// resolves instances through InstanceRegistry.Get → scan.Instance, whose
// Client is a ports.SonarrClient (internal/catalog/app/scan/scan_usecase.go:88-91),
// and keys its state by (instance, sonarr_series_id, season_number). The
// loop merely CONSUMES this declaration to decide whether to spawn.
//
// Movies are deliberately absent and this is NOT an oversight: there is no
// movie-grab infrastructure in seasonfill at all (grab.Record is keyed by
// SeriesID + SeasonNumber, internal/grab/domain/grab.go:58,60), so
// movie-regrab would mean building movie-grabbing from scratch. Tracked as
// backlog item B-41; the vertical-parity registry records it as
// StateDeferred, not as a Gap (internal/shared/verticals/verticals.go,
// cell regrab_supported/movie).
//
// Adding a type here is the ONE edit needed to let regrab run against a new
// arr flavour — that is the point of declaring instead of failing.
var SupportedInstanceTypes = []string{scan.InstanceTypeSonarr}

// SupportsInstanceType reports whether regrab can run against an instance
// of the given arr_instance.type.
//
// The predicate is PURE and fail-CLOSED on purpose: the empty string and
// any unknown value report false. The fail-OPEN policy ("unknown type =>
// run anyway") is a loop-lifecycle decision and lives at the single call
// site in cmd/server/loops/regrab.go, where it can be read, reasoned about
// and tested as one rule instead of being smeared across a predicate that
// silently says "yes" to garbage.
func SupportsInstanceType(instanceType string) bool {
	return slices.Contains(SupportedInstanceTypes, instanceType)
}
