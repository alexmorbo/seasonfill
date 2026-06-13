// Package quota owns the QuotaCounter port and an in-process fallback
// suitable for unit tests / single-process deployments.
//
// QuotaCounter is the substitution seam between external-service
// clients (OMDb today, potentially TMDB / SimKL / Trakt later) and a
// durable counter store. The production impl lives in
// infrastructure/database/repositories/quota_counter_repository.go
// (DB-backed via GORM upsert). The InMemoryCounter is the test
// double / single-process fallback.
//
// Concurrency: every method must be safe for concurrent callers
// across goroutines.
//
// Window semantics: `window` is an opaque caller-derived time.Time
// (typically truncated to a day or month boundary via the helpers
// in window.go). The store uses (service, window) as a composite
// key — different windows for the same service are independent rows.
//
// The package owns NO business logic. Cap enforcement (Reserve vs
// hard-deny) lives at the client layer; the counter only counts.
package quota

import (
	"context"
	"sync"
	"time"
)

// QuotaCounter is the port. Implementations:
//   - *repositories.QuotaCounterRepository (production, DB-backed)
//   - *InMemoryCounter (unit tests, single-process fallback)
type QuotaCounter interface {
	// Increment atomically bumps the (service, window) row by 1
	// (or inserts a fresh row with count=1 on first contact) and
	// returns the new count. Lock-free at the DB layer via
	// INSERT ... ON CONFLICT DO UPDATE.
	Increment(ctx context.Context, service string, window time.Time) (int, error)

	// Get reads the current count for (service, window). Returns
	// 0 (no error) when the row does not yet exist — that's a
	// fresh window for which nobody has Increment'd yet.
	Get(ctx context.Context, service string, window time.Time) (int, error)

	// Reset deletes every row where window_start < before. Used
	// by the daily GC sweeper to keep the table tiny. Returns the
	// number of rows deleted for observability.
	Reset(ctx context.Context, before time.Time) (int64, error)
}

// InMemoryCounter is the test double + single-process fallback. Safe
// for concurrent callers (one mutex; no lock-free CAS because the
// hot path here is tests, not production traffic).
//
// Construct via NewInMemoryCounter — the zero value is NOT usable
// because the map is nil.
type InMemoryCounter struct {
	mu    sync.Mutex
	rows  map[inMemoryKey]int
	clock func() time.Time
}

type inMemoryKey struct {
	service string
	window  time.Time
}

// NewInMemoryCounter constructs an empty in-memory counter. The
// clock parameter is optional — pass nil to use time.Now().UTC()
// for the updated_at side-channel (not currently surfaced, but
// kept symmetric with the DB-backed impl).
func NewInMemoryCounter(clock func() time.Time) *InMemoryCounter {
	if clock == nil {
		clock = func() time.Time { return time.Now().UTC() }
	}
	return &InMemoryCounter{
		rows:  make(map[inMemoryKey]int),
		clock: clock,
	}
}

// Increment bumps (service, window) by 1 and returns the new count.
func (c *InMemoryCounter) Increment(_ context.Context, service string, window time.Time) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	k := inMemoryKey{service: service, window: window.UTC()}
	c.rows[k]++
	return c.rows[k], nil
}

// Get returns the current count for (service, window). 0 when the
// row does not exist.
func (c *InMemoryCounter) Get(_ context.Context, service string, window time.Time) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.rows[inMemoryKey{service: service, window: window.UTC()}], nil
}

// Reset deletes every row whose window is strictly older than
// `before`. Returns the number of rows deleted.
func (c *InMemoryCounter) Reset(_ context.Context, before time.Time) (int64, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	beforeUTC := before.UTC()
	var deleted int64
	for k := range c.rows {
		if k.window.Before(beforeUTC) {
			delete(c.rows, k)
			deleted++
		}
	}
	return deleted, nil
}
