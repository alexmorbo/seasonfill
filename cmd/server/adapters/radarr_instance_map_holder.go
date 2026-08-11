package adapters

import (
	"maps"
	"sync"

	"github.com/alexmorbo/seasonfill/internal/catalog/app/scan"
)

// RadarrInstanceMapHolder is the radarr-side twin of InstanceMapHolder: the
// mutex-protected container the OnApplied fanout writes into (Replace) and the
// radarr REST lookups (add-to-radarr, collection-monitor) read from (Load). The
// sonarr InstanceMapHolder deliberately excludes radarr rows, so radarr needs
// its own holder. Load returns a defensive copy.
type RadarrInstanceMapHolder struct {
	mu sync.RWMutex
	m  map[string]scan.RadarrInstance
}

// NewRadarrInstanceMapHolder seeds the holder with a defensive copy of initial.
func NewRadarrInstanceMapHolder(initial map[string]scan.RadarrInstance) *RadarrInstanceMapHolder {
	cp := make(map[string]scan.RadarrInstance, len(initial))
	maps.Copy(cp, initial)
	return &RadarrInstanceMapHolder{m: cp}
}

// Replace swaps in a new snapshot atomically. Called from the OnApplied fanout
// under the SonarrClientsSubscriber lock (writes serialised upstream).
func (h *RadarrInstanceMapHolder) Replace(next map[string]scan.RadarrInstance) {
	h.mu.Lock()
	h.m = next
	h.mu.Unlock()
}

// Load returns a defensive copy of the current snapshot.
func (h *RadarrInstanceMapHolder) Load() map[string]scan.RadarrInstance {
	h.mu.RLock()
	defer h.mu.RUnlock()
	out := make(map[string]scan.RadarrInstance, len(h.m))
	maps.Copy(out, h.m)
	return out
}
