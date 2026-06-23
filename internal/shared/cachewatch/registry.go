package cachewatch

import (
	"fmt"
	"sort"
	"sync"
)

// closeable narrows the registry contract to what we need (avoids dragging
// the generic Cache[K, V] parameter through a non-generic registry).
type closeable interface {
	// Close is required so a registry-wide shutdown helper can be added
	// later without changing the public surface.
	Close() error
}

// registry is a name-keyed singleton of every Cache constructed via New.
// Package-private — exposed only through Names / IsRegistered for
// debugging endpoints and the test reset hook.
type registry struct {
	mu     sync.RWMutex
	caches map[string]closeable
}

var defaultRegistry = &registry{caches: map[string]closeable{}}

// registerOrPanic adds c under name. Panics if name is already taken —
// duplicate cache instantiation is always a wiring bug (two distinct
// caches sharing a metric label would silently merge counters).
func registerOrPanic(name string, c closeable) {
	defaultRegistry.mu.Lock()
	defer defaultRegistry.mu.Unlock()
	if _, exists := defaultRegistry.caches[name]; exists {
		panic(fmt.Sprintf("cachewatch: cache %q is already registered (duplicate New)", name))
	}
	defaultRegistry.caches[name] = c
}

// Names returns a sorted snapshot of registered cache names. Used by
// /metrics debugging endpoints to enumerate live caches.
func Names() []string {
	defaultRegistry.mu.RLock()
	defer defaultRegistry.mu.RUnlock()
	out := make([]string, 0, len(defaultRegistry.caches))
	for n := range defaultRegistry.caches {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// IsRegistered reports whether a cache with the given name exists.
func IsRegistered(name string) bool {
	defaultRegistry.mu.RLock()
	defer defaultRegistry.mu.RUnlock()
	_, ok := defaultRegistry.caches[name]
	return ok
}
