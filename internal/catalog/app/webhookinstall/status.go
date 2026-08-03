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

	// Attempts counts consecutive failed install/reconcile attempts for
	// this instance. Reset to 0 on the first success. Written by
	// recordFailure (S2).
	Attempts int
	// FirstAttemptAt is the timestamp of the first failure in the current
	// failure streak — the anchor for the grace window. Zero when no
	// failure streak is outstanding. Written by recordFailure, cleared on
	// success (successStatus rebuilds a fresh literal).
	FirstAttemptAt time.Time
	// Installing is the derived pending state (S2): a fresh instance whose
	// webhook has not installed yet but is still inside the grace window
	// (GraceWindow / GraceMaxAttempts). The UI renders a loader instead of
	// a red error while this is true; LastError still carries the
	// underlying cause. Computed exclusively in recordFailure — success,
	// disabled and public_url-undetermined paths leave it false.
	Installing bool
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

// PublicURLWithFallback returns a PublicURLFunc that resolves to the
// configured base URL (SEASONFILL_WEBHOOK_BASE_URL). It deliberately does
// NOT consult the per-request X-Forwarded value: Sonarr calls seasonfill
// in-cluster (http://seasonfill:8080, env always set), so a public
// X-Forwarded host derived from a browser request would be an actively
// wrong install URL for an internal caller (SI-3).
//
// Precedence, top to bottom: the per-instance WebhookURLOverride is applied
// on top by snap.WebhookBaseURL inside the reconciler, then this configured
// fallback. When both are empty the reconciler keeps its existing
// "public_url undetermined" sentinel.
func PublicURLWithFallback(fallback string) PublicURLFunc {
	return func(context.Context) string {
		return fallback
	}
}
