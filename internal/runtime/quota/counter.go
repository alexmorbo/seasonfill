// Package quota owns the QuotaCounter port and an in-process fallback
// suitable for unit tests / single-process deployments.
//
// QuotaCounter is the substitution seam between external-service
// clients (OMDb today, potentially TMDB / SimKL / Trakt later) and a
// durable counter store. The production impl lives in
// internal/admin/persistence/quota_counter_repository.go (DB-backed
// via GORM upsert). The InMemoryCounter is the test double /
// single-process fallback.
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
//   - *adminpersistence.QuotaCounterRepository (production, DB-backed)
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

	// SetQuota stamps the upstream-known cap for the (service,
	// window) row (e.g. OMDb's X-Quota-Limit). No-op when the
	// row does not yet exist (Increment is always called first
	// by the guard); InMemoryCounter mirrors that semantic.
	SetQuota(ctx context.Context, service string, window time.Time, quota int) error

	// MarkExhausted stamps the boundary-cross timestamp for the
	// (service, window) row. Idempotent — second call no-ops on
	// existing non-NULL exhausted_at; InMemoryCounter mirrors.
	MarkExhausted(ctx context.Context, service string, window time.Time) error
}

// InMemoryCounter is the test double + single-process fallback. Safe
// for concurrent callers (one mutex; no lock-free CAS because the
// hot path here is tests, not production traffic).
//
// Construct via NewInMemoryCounter — the zero value is NOT usable
// because the map is nil.
type InMemoryCounter struct {
	mu    sync.Mutex
	rows  map[inMemoryKey]*inMemoryValue
	clock func() time.Time
}

type inMemoryKey struct {
	service string
	window  time.Time
}

// inMemoryValue mirrors the DB row's three quota-state columns: count
// (requests_made), cap (requests_quota), exhaustedAt. D-5 (466c) lifted
// this from a bare int to a small struct so SetQuota + MarkExhausted
// have somewhere to store state in the in-process fallback path.
type inMemoryValue struct {
	count       int
	cap         int
	exhaustedAt *time.Time
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
		rows:  make(map[inMemoryKey]*inMemoryValue),
		clock: clock,
	}
}

// Increment bumps (service, window) by 1 and returns the new count.
func (c *InMemoryCounter) Increment(_ context.Context, service string, window time.Time) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	k := inMemoryKey{service: service, window: window.UTC()}
	v, ok := c.rows[k]
	if !ok {
		v = &inMemoryValue{}
		c.rows[k] = v
	}
	v.count++
	return v.count, nil
}

// Get returns the current count for (service, window). 0 when the
// row does not exist.
func (c *InMemoryCounter) Get(_ context.Context, service string, window time.Time) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if v, ok := c.rows[inMemoryKey{service: service, window: window.UTC()}]; ok {
		return v.count, nil
	}
	return 0, nil
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

// SetQuota stamps the upstream cap. No-op when the row is absent
// (mirrors the DB UPDATE WHERE rowcount=0 semantic).
func (c *InMemoryCounter) SetQuota(_ context.Context, service string, window time.Time, quotaCap int) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if v, ok := c.rows[inMemoryKey{service: service, window: window.UTC()}]; ok {
		v.cap = quotaCap
	}
	return nil
}

// MarkExhausted stamps exhaustedAt to clock() on the first call;
// subsequent calls are no-ops (preserves the original boundary cross).
// No-op when the row is absent.
func (c *InMemoryCounter) MarkExhausted(_ context.Context, service string, window time.Time) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if v, ok := c.rows[inMemoryKey{service: service, window: window.UTC()}]; ok {
		if v.exhaustedAt == nil {
			now := c.clock()
			v.exhaustedAt = &now
		}
	}
	return nil
}

// QuotaCapForTest is a test-only inspector returning the cap recorded
// by SetQuota. Returns 0 when no row exists.
func (c *InMemoryCounter) QuotaCapForTest(service string, window time.Time) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	if v, ok := c.rows[inMemoryKey{service: service, window: window.UTC()}]; ok {
		return v.cap
	}
	return 0
}

// ExhaustedAtForTest is a test-only inspector returning the boundary
// cross timestamp recorded by MarkExhausted, or nil when not set.
func (c *InMemoryCounter) ExhaustedAtForTest(service string, window time.Time) *time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	if v, ok := c.rows[inMemoryKey{service: service, window: window.UTC()}]; ok {
		return v.exhaustedAt
	}
	return nil
}
