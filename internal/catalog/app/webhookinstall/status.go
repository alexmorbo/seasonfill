// Package webhookinstall owns the reconciliation loop that keeps the
// Sonarr-side Webhook notification in sync with seasonfill's instance
// row. It is split from application/webhook (which processes incoming
// webhook events) because the two share nothing beyond the wire
// protocol and conflating them in one package would muddle the
// dependency graph: webhookinstall depends on a Sonarr-mutation port,
// webhook only on grab/cooldown repositories.
package webhookinstall

import (
	"context"
	"sync"
	"time"
)

// Status is the last reconcile outcome for one instance. Pointer
// fields are nil when the state is absent — NotificationID is nil
// when Installed=false; LastError is nil after a successful pass.
type Status struct {
	Installed      bool
	NotificationID *int
	InstalledURL   *string
	LastError      *string
	LastCheckedAt  time.Time
	NextRetryAt    *time.Time
}

// StatusCache is the in-memory store the reconciler writes after every
// attempt. Reads from GET /webhook/status hit this directly so the
// dashboard does not stall on a Sonarr outage. Lifecycle is process-
// scoped: empty at pod start, fills lazily.
type StatusCache struct {
	mu     sync.RWMutex
	byName map[string]Status
}

func NewStatusCache() *StatusCache {
	return &StatusCache{byName: make(map[string]Status)}
}

func (c *StatusCache) Get(name string) (Status, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	s, ok := c.byName[name]
	return s, ok
}

func (c *StatusCache) Set(name string, s Status) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.byName[name] = s
}

func (c *StatusCache) Delete(name string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.byName, name)
}

// RequestPublicURLKey is the typed context key used by HTTP middleware
// to stash the resolved seasonfill public URL for the in-flight
// request. The reconciler's PublicURLFunc reads it. Defined here (not
// in the http package) so both writer (interface/http middleware) and
// reader (cmd/server's PublicURLFromContext closure) agree on type
// identity without either package depending on the other.
type RequestPublicURLKey struct{}

// PublicURLFromContext returns the value stashed under
// RequestPublicURLKey, or "" when absent. The background reconciler
// loop (041d) runs without a request context — it gets "" and falls
// through to the snapshot WebhookURLOverride.
func PublicURLFromContext(ctx context.Context) string {
	if v, ok := ctx.Value(RequestPublicURLKey{}).(string); ok {
		return v
	}
	return ""
}

// PublicURLWithFallback returns a PublicURLFunc that resolves the
// per-request X-Forwarded public URL from context, falling back to the
// configured base URL (SEASONFILL_WEBHOOK_BASE_URL) when the context
// value is empty. This lets the context-less background 5-min reconcile
// and the pod-restart lazy reconcile resolve a base URL out of the box.
//
// Precedence, top to bottom: the per-instance WebhookURLOverride is
// applied on top by snap.WebhookBaseURL inside the reconciler, then the
// per-request context value returned here, then the configured
// fallback. When all three are empty the reconciler keeps its existing
// "public_url undetermined" sentinel.
func PublicURLWithFallback(fallback string) PublicURLFunc {
	return func(ctx context.Context) string {
		if v := PublicURLFromContext(ctx); v != "" {
			return v
		}
		return fallback
	}
}
