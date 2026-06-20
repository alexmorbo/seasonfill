package auth

import (
	"sync"
	"time"

	"golang.org/x/time/rate"
)

// IPLimiterMaxEntries caps the in-memory key→bucket map. Reaching this
// triggers a synchronous prune of entries idle longer than IdleTTL. The
// cap is intentionally generous (10k) — under normal traffic the map
// stays an order of magnitude smaller; the cap exists only to bound
// worst-case memory under IP-rotation flooding (HIGH-S3).
const (
	IPLimiterMaxEntries = 10_000
	IPLimiterIdleTTL    = time.Hour
)

// IPLimiter is a thread-safe map of key → token bucket. Used by the
// login handler (IP-keyed) and webhook receiver (instance-keyed; 021a-2).
//
// Eviction policy (HIGH-S3): when len(limiters) exceeds
// IPLimiterMaxEntries, Allow() prunes any entry whose last-seen
// timestamp is older than IPLimiterIdleTTL before inserting a new
// bucket. This bounds memory under IP-rotation attacks at a small
// constant cost on the hot path.
type IPLimiter struct {
	mu       sync.Mutex
	limiters map[string]*ipEntry
	r        rate.Limit
	burst    int
	now      func() time.Time
}

type ipEntry struct {
	lim      *rate.Limiter
	lastSeen time.Time
}

func NewIPLimiter(r rate.Limit, burst int) *IPLimiter {
	return &IPLimiter{
		limiters: make(map[string]*ipEntry),
		r:        r,
		burst:    burst,
		now:      time.Now,
	}
}

func (l *IPLimiter) Allow(key string) bool {
	l.mu.Lock()
	now := l.now()
	ent, ok := l.limiters[key]
	if !ok {
		if len(l.limiters) >= IPLimiterMaxEntries {
			l.pruneLocked(now)
		}
		ent = &ipEntry{lim: rate.NewLimiter(l.r, l.burst)}
		l.limiters[key] = ent
	}
	ent.lastSeen = now
	l.mu.Unlock()
	return ent.lim.Allow()
}

// pruneLocked drops entries idle longer than IPLimiterIdleTTL. Caller
// must hold l.mu. Runs in O(N) over the map; cheap relative to the
// bcrypt-on-login burn it shields.
func (l *IPLimiter) pruneLocked(now time.Time) {
	cutoff := now.Add(-IPLimiterIdleTTL)
	for k, e := range l.limiters {
		if e.lastSeen.Before(cutoff) {
			delete(l.limiters, k)
		}
	}
}

// Len returns the current map size. Test/observability aid.
func (l *IPLimiter) Len() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.limiters)
}

// SetClock swaps the time source. Test-only — production keeps time.Now.
func (l *IPLimiter) SetClock(now func() time.Time) {
	if now == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.now = now
}

// LoginLimit is "5 attempts per 15min": refill 1 token / 3min; burst 5
// (configured at call site).
func LoginLimit() rate.Limit { return rate.Every(15 * time.Minute / 5) }

// WebhookLimit is "60 req per minute": 1 token / sec; burst 60.
func WebhookLimit() rate.Limit { return rate.Every(time.Second) }

// PasswordChangeLimit is "3 attempts per 15min": refill 1 token / 5min;
// burst 3. Stricter than LoginLimit because the caller is already
// authenticated — a cookie-thief brute-forcing the current password has
// no business burning more than a handful of guesses per quarter hour.
func PasswordChangeLimit() rate.Limit { return rate.Every(15 * time.Minute / 3) }
