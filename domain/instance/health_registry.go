package instance

import (
	"sync"
	"time"
)

// Registry is a thread-safe map of per-instance health snapshots. Use
// MarkAvailable / MarkUnavailable to record transitions; both fire the
// optional Listener so callers (metrics) can react without coupling.
type Registry struct {
	mu       sync.RWMutex
	entries  map[string]Snapshot
	listener Listener
}

// Listener observes registry events. Implementations should not block.
type Listener interface {
	OnTransition(name string, from, to Health, at time.Time, lastErr string)
	OnCheck(name string, h Health, at time.Time)
}

// NewRegistry returns an empty registry seeded with the given instance names
// in HealthUnavailableUnknown state.
func NewRegistry(names []string) *Registry {
	r := &Registry{entries: make(map[string]Snapshot, len(names))}
	for _, n := range names {
		r.entries[n] = Snapshot{Name: n, Health: HealthUnavailableUnknown}
	}
	return r
}

// WithListener installs a transition listener and returns r.
func (r *Registry) WithListener(l Listener) *Registry {
	r.mu.Lock()
	r.listener = l
	r.mu.Unlock()
	return r
}

// Get returns the current snapshot and whether the instance is known.
func (r *Registry) Get(name string) (Snapshot, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	s, ok := r.entries[name]
	return s, ok
}

// Snapshot returns a stable copy of every entry.
func (r *Registry) Snapshot() []Snapshot {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Snapshot, 0, len(r.entries))
	for _, s := range r.entries {
		out = append(out, s)
	}
	return out
}

// MarkAvailable transitions an instance into Available. Returns the previous
// health and whether the state actually changed.
func (r *Registry) MarkAvailable(name string, at time.Time) (Health, bool) {
	return r.set(name, HealthAvailable, "", at)
}

// MarkUnavailable transitions an instance into one of the Unavailable* states.
// If state is HealthAvailable it is coerced to HealthUnavailableUnknown so
// "mark unavailable but I don't know why" still records a meaningful state.
func (r *Registry) MarkUnavailable(name string, state Health, lastErr string, at time.Time) (Health, bool) {
	if state == HealthAvailable {
		state = HealthUnavailableUnknown
	}
	return r.set(name, state, lastErr, at)
}

func (r *Registry) set(name string, to Health, lastErr string, at time.Time) (Health, bool) {
	r.mu.Lock()
	prev, ok := r.entries[name]
	if !ok {
		prev = Snapshot{Name: name, Health: HealthUnavailableUnknown}
	}
	from := prev.Health
	prev.Health = to
	prev.LastCheckAt = at
	prev.LastError = lastErr
	changed := from != to
	if changed {
		prev.TransitionsCount++
	}
	r.entries[name] = prev
	listener := r.listener
	r.mu.Unlock()

	if listener != nil {
		listener.OnCheck(name, to, at)
		if changed {
			listener.OnTransition(name, from, to, at, lastErr)
		}
	}
	return from, changed
}

// AnyAvailable returns true if at least one entry is HealthAvailable.
func (r *Registry) AnyAvailable() bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, s := range r.entries {
		if s.Health == HealthAvailable {
			return true
		}
	}
	return false
}

// Names returns the seeded instance names.
func (r *Registry) Names() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]string, 0, len(r.entries))
	for n := range r.entries {
		out = append(out, n)
	}
	return out
}
