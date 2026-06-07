package grab

// StatusGroup labels the counter buckets surfaced by the B6 aggregation
// endpoint. Taxonomy documented in documentation/03-phase3-deltas.md §B6:
//   - GroupGrabs   — every row (COUNT(*)).
//   - GroupImports — status = StatusImported.
//   - GroupFails   — status IN (StatusImportFailed, StatusGrabFailed).
//
// StatusGrabbed is transient and counted only under GroupGrabs.
type StatusGroup int

const (
	GroupGrabs StatusGroup = iota
	GroupImports
	GroupFails
)

// FailStatuses returns the statuses that roll up under "fails". Callers
// must not mutate the returned slice; it is shared.
func FailStatuses() []Status {
	return failStatuses
}

var failStatuses = []Status{StatusImportFailed, StatusGrabFailed}
