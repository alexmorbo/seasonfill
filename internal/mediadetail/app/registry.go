package app

import (
	"sync"

	"github.com/alexmorbo/seasonfill/internal/mediadetail/domain"
)

// SectionRegistry maps a MediaType to its ordered list of SectionPlugins. It is
// the ADR-0022 D3 substitute for `if type` branching: the engine asks the
// registry For(type) and drives whatever plugins were registered, in stable
// registration order.
//
// S1: constructed EMPTY (no plugins). Later stories register per-(type,section)
// plugins at the composition-root late-bind zone, BEFORE the HTTP server begins
// serving. Registration is single-threaded at boot; reads happen concurrently
// while serving. An RWMutex guards both so the type is race-safe even if a test
// interleaves them.
type SectionRegistry struct {
	mu      sync.RWMutex
	plugins map[domain.MediaType][]SectionPlugin
}

// NewSectionRegistry returns an empty registry.
func NewSectionRegistry() *SectionRegistry {
	return &SectionRegistry{plugins: make(map[domain.MediaType][]SectionPlugin)}
}

// Register appends p to mediaType's plugin list, preserving registration order.
// A nil plugin is ignored (no-op). An invalid mediaType is ignored (no-op) —
// registration is a boot-time wiring concern, not a request path, so it fails
// silently rather than panicking. Duplicate sections are ALLOWED (appended);
// callers are expected to register each section once per type.
func (r *SectionRegistry) Register(mediaType domain.MediaType, p SectionPlugin) {
	if p == nil || !mediaType.Valid() {
		return
	}
	r.mu.Lock()
	r.plugins[mediaType] = append(r.plugins[mediaType], p)
	r.mu.Unlock()
}

// For returns the plugins registered for mediaType in stable registration
// order. Returns an empty (non-nil) slice when nothing is registered — the
// caller iterates it uniformly. The returned slice is a defensive copy so
// concurrent Register calls cannot mutate a slice the caller is ranging.
func (r *SectionRegistry) For(mediaType domain.MediaType) []SectionPlugin {
	r.mu.RLock()
	defer r.mu.RUnlock()
	src := r.plugins[mediaType]
	out := make([]SectionPlugin, len(src))
	copy(out, src)
	return out
}
