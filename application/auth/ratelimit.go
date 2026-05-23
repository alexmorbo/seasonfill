package auth

import (
	"sync"
	"time"

	"golang.org/x/time/rate"
)

// IPLimiter is a thread-safe map of key → token bucket. Used by the
// login handler (IP-keyed) and webhook receiver (instance-keyed; 021a-2).
// No eviction — Phase 7 deployments don't see enough distinct keys to
// matter. LRU eviction is Phase 8+.
type IPLimiter struct {
	mu       sync.Mutex
	limiters map[string]*rate.Limiter
	r        rate.Limit
	burst    int
}

func NewIPLimiter(r rate.Limit, burst int) *IPLimiter {
	return &IPLimiter{limiters: make(map[string]*rate.Limiter), r: r, burst: burst}
}

func (l *IPLimiter) Allow(key string) bool {
	l.mu.Lock()
	lim, ok := l.limiters[key]
	if !ok {
		lim = rate.NewLimiter(l.r, l.burst)
		l.limiters[key] = lim
	}
	l.mu.Unlock()
	return lim.Allow()
}

// LoginLimit is "5 attempts per 15min": refill 1 token / 3min; burst 5
// (configured at call site).
func LoginLimit() rate.Limit { return rate.Every(15 * time.Minute / 5) }

// WebhookLimit is "60 req per minute": 1 token / sec; burst 60.
func WebhookLimit() rate.Limit { return rate.Every(time.Second) }
