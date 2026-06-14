package adapters

import (
	"sync"

	"github.com/alexmorbo/seasonfill/application/scan"
)

// InstanceMapHolder is the shared, mutex-protected container the
// OnApplied fan-out writes into and rescanUC / webhookUC /
// handler closures read from. A plain map would race; using
// sync.Map loses the by-name shape the callers need.
//
// Replace publishes a new snapshot atomically; Load returns a defensive
// copy so callers can iterate / index without holding the read lock.
type InstanceMapHolder struct {
	mu sync.RWMutex
	m  map[string]scan.Instance
}

// NewInstanceMapHolder seeds the holder with a defensive copy of
// `initial` so the caller can drop their reference without affecting
// subsequent Replace calls.
func NewInstanceMapHolder(initial map[string]scan.Instance) *InstanceMapHolder {
	cp := make(map[string]scan.Instance, len(initial))
	for k, v := range initial {
		cp[k] = v
	}
	return &InstanceMapHolder{m: cp}
}

// Replace swaps in a new snapshot. The fanout calls Replace under the
// SonarrClientsSubscriber lock, so concurrent writes are serialised
// upstream — the holder's mutex only guards reader/writer alignment.
func (h *InstanceMapHolder) Replace(next map[string]scan.Instance) {
	h.mu.Lock()
	h.m = next
	h.mu.Unlock()
}

// Load returns a defensive copy of the current snapshot. Callers may
// iterate / index the returned map without holding any holder lock.
func (h *InstanceMapHolder) Load() map[string]scan.Instance {
	h.mu.RLock()
	defer h.mu.RUnlock()
	out := make(map[string]scan.Instance, len(h.m))
	for k, v := range h.m {
		out[k] = v
	}
	return out
}
