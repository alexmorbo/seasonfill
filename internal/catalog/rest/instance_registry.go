package rest

import (
	"github.com/alexmorbo/seasonfill/internal/catalog/app/scan"
)

// InstanceRegistry — reload-aware snapshot accessor shared by handlers
// that need to look up runtime-mutable Sonarr instances by name. Load
// MUST return a fresh map copy on every call so callers can iterate
// without holding any external lock; the reload bus is the single
// writer behind the implementation. Zero value (Load == nil) is the
// "no instances known" mode used by route-shape-only tests; production
// wires Load=holder.load from cmd/server.
type InstanceRegistry struct {
	Load func() map[string]scan.Instance
}

// snapshot returns the current registry contents or an empty map if
// Load is nil. Centralised so every handler reads through the same
// nil-safe path.
func (r InstanceRegistry) snapshot() map[string]scan.Instance {
	return r.Snapshot()
}

// Snapshot is the exported variant of snapshot — story 431 (A-1-5)
// added it so the new internal/grab/rest handlers can resolve the
// runtime-mutable Sonarr instance map through the same nil-safe path
// without duplicating the helper. The unexported alias stays so the
// catch-all handlers package call sites don't churn.
func (r InstanceRegistry) Snapshot() map[string]scan.Instance {
	if r.Load == nil {
		return map[string]scan.Instance{}
	}
	return r.Load()
}
