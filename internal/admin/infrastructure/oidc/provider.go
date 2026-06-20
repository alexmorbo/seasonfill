// Package oidc wraps github.com/coreos/go-oidc/v3 with an issuer-keyed
// provider cache so changing the issuer URL in runtime_config triggers
// fresh discovery while a stable issuer stays warm. The cache key is the
// issuer URL string itself — go-oidc internally caches JWKS per provider,
// so reusing a Provider instance also reuses its JWKS HTTP client.
package oidc

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	gooidc "github.com/coreos/go-oidc/v3/oidc"
)

// ProviderCache holds at most one *gooidc.Provider per issuer URL. The
// cache is bounded by operator intent (you'd typically have one issuer
// at a time); we don't evict, but the cache is per-process so a restart
// drops everything anyway. Concurrent NewProvider calls for the same
// issuer share a single in-flight discovery via singleflight-like
// semantics implemented with a per-issuer mutex.
type ProviderCache struct {
	mu      sync.Mutex
	entries map[string]*entry
}

type entry struct {
	provider *gooidc.Provider
	cachedAt time.Time
}

func NewProviderCache() *ProviderCache {
	return &ProviderCache{entries: map[string]*entry{}}
}

// Get returns the cached provider for issuer, performing OIDC discovery
// on first call. ctx applies to the discovery HTTP request only. Returns
// an error if discovery fails or the issuer is empty.
func (c *ProviderCache) Get(ctx context.Context, issuer string) (*gooidc.Provider, error) {
	if issuer == "" {
		return nil, errors.New("oidc: empty issuer")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if e, ok := c.entries[issuer]; ok {
		return e.provider, nil
	}
	p, err := gooidc.NewProvider(ctx, issuer)
	if err != nil {
		return nil, fmt.Errorf("oidc: discover %q: %w", issuer, err)
	}
	c.entries[issuer] = &entry{provider: p, cachedAt: time.Now()}
	return p, nil
}

// Invalidate drops the cached entry for issuer (or all entries when
// issuer is empty). Called when runtime_config OIDC settings change so
// the next request triggers fresh discovery against the (potentially)
// new issuer.
func (c *ProviderCache) Invalidate(issuer string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if issuer == "" {
		c.entries = map[string]*entry{}
		return
	}
	delete(c.entries, issuer)
}

// Verifier builds an ID-token verifier configured for clientID. Reuses
// the provider's JWKS cache. Caller verifies the result against nonce
// + audience as part of the callback flow.
func Verifier(provider *gooidc.Provider, clientID string) *gooidc.IDTokenVerifier {
	return provider.Verifier(&gooidc.Config{ClientID: clientID})
}
