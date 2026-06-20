package adapters

import (
	"github.com/alexmorbo/seasonfill/application/people"
	appenrich "github.com/alexmorbo/seasonfill/internal/enrichment/app"
)

// PersonEnqueuerHolder late-binds the enrichment dispatcher into
// the H-2 people use case. The dispatcher is constructed inside
// wireEnrichment, but the people use case is built earlier (so it
// can be passed to httpserver.NewServer). The holder is wired
// after enrichBundle is assembled; until then Enqueue no-ops
// (nil-OK by contract — the use case still returns 200 + degraded
// for stub persons on cold boot / disabled enrichment).
//
// The holder doubles as a seriesrefresh.Deps.Dispatcher value (it
// satisfies the appenrich.Dispatcher interface — Enqueue + Close),
// so one instance serves both H-2 and the refresh path.
type PersonEnqueuerHolder struct {
	inner appenrich.Dispatcher
}

// NewPersonEnqueuerHolder returns a holder with no inner dispatcher.
// Call Set(d) once the real dispatcher is available.
func NewPersonEnqueuerHolder() *PersonEnqueuerHolder {
	return &PersonEnqueuerHolder{}
}

// Compile-time assertions: the holder must satisfy both the
// PersonEnqueuer port (Enqueue-only) and the full Dispatcher
// (Enqueue + Close, consumed by seriesrefresh.Deps.Dispatcher).
var (
	_ people.PersonEnqueuer = (*PersonEnqueuerHolder)(nil)
	_ appenrich.Dispatcher  = (*PersonEnqueuerHolder)(nil)
)

// Set wires the inner dispatcher. Idempotent — the production
// boot path calls Set at most once, after wireEnrichment returns.
func (h *PersonEnqueuerHolder) Set(d appenrich.Dispatcher) { h.inner = d }

// Enqueue forwards to the inner dispatcher when one is wired;
// no-ops otherwise.
func (h *PersonEnqueuerHolder) Enqueue(kind appenrich.EntityKind, id int64, p appenrich.Priority) {
	if h.inner == nil {
		return
	}
	h.inner.Enqueue(kind, id, p)
}

// Close satisfies appenrich.Dispatcher so the same holder serves
// both people.PersonEnqueuer (Enqueue-only) and
// seriesrefresh.Deps.Dispatcher (Enqueue + Close). The dispatcher's
// actual Close runs via enrichBundle.Dispatcher.Close() at shutdown,
// so this holder no-ops.
func (h *PersonEnqueuerHolder) Close() {}
