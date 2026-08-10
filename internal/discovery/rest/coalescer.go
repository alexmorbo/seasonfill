package rest

import (
	"golang.org/x/sync/singleflight"

	"github.com/alexmorbo/seasonfill/internal/discovery/app"
)

// refreshCoalescer collapses concurrent identical on-demand refresh
// calls onto a single execution and shares the result with every
// caller. Prod uses singleflightCoalescer (real x/sync/singleflight);
// tests inject a deterministic implementation via WithRefreshCoalescer.
type refreshCoalescer interface {
	Do(key string, fn func() (any, error)) (v any, err error, shared bool)
}

// singleflightCoalescer is the production adapter. It delegates 1:1 to
// x/sync/singleflight so runtime behavior is byte-identical to the
// pre-seam code.
type singleflightCoalescer struct {
	g singleflight.Group
}

func (s *singleflightCoalescer) Do(key string, fn func() (any, error)) (any, error, bool) {
	return s.g.Do(key, fn)
}

// DiscoveryOption customizes a DiscoveryHandler at construction time.
type DiscoveryOption func(*DiscoveryHandler)

// WithRefreshCoalescer overrides the default singleflight coalescer.
// Test-only seam: lets a unit test inject a deterministic coalescer so
// the concurrent-collapse assertion does not race the singleflight
// hook window. Prod never calls this and keeps the singleflight adapter.
func WithRefreshCoalescer(c refreshCoalescer) DiscoveryOption {
	return func(h *DiscoveryHandler) { h.sfGroup = c }
}

// SetBlocklist injects the discovery blocklist cache (nil-OK). Wiring calls
// this after NewDiscoveryHandler so the constructor signature stays stable.
// ADR-0017 Ф5 S3.
func (h *DiscoveryHandler) SetBlocklist(bc *app.BlocklistCache) { h.blocklist = bc }

// WithBlocklist is the functional-option form of SetBlocklist.
func WithBlocklist(bc *app.BlocklistCache) DiscoveryOption {
	return func(h *DiscoveryHandler) { h.blocklist = bc }
}
