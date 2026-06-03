package reload

import (
	"context"
	"log/slog"

	infraoidc "github.com/alexmorbo/seasonfill/infrastructure/oidc"
	"github.com/alexmorbo/seasonfill/internal/runtime"
)

// OIDCProviderSubscriber drops the cached *gooidc.Provider entry for the
// PREVIOUS issuer URL whenever a snapshot publishes a different one, so
// the next OIDC start triggers fresh discovery. Keeps stable-issuer
// snapshots warm (no eviction).
type OIDCProviderSubscriber struct {
	cache      *infraoidc.ProviderCache
	lastIssuer string
	logger     *slog.Logger
}

func NewOIDCProviderSubscriber(cache *infraoidc.ProviderCache, logger *slog.Logger) *OIDCProviderSubscriber {
	if logger == nil {
		logger = slog.Default()
	}
	return &OIDCProviderSubscriber{cache: cache, logger: logger}
}

func (s *OIDCProviderSubscriber) Run(ctx context.Context, bus *runtime.Bus, ready func()) {
	runLoop(ctx, bus, "oidcProvider", s.logger, s.apply, ready)
}

func (s *OIDCProviderSubscriber) apply(ctx context.Context, snap runtime.Snapshot) error {
	next := snap.Auth.OIDC.Issuer
	if next == s.lastIssuer {
		return nil
	}
	s.cache.Invalidate(s.lastIssuer)
	s.lastIssuer = next
	return nil
}
