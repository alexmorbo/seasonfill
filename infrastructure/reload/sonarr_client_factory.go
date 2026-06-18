package reload

import (
	"log/slog"
	"sync/atomic"

	"github.com/alexmorbo/seasonfill/application/ports"
	"github.com/alexmorbo/seasonfill/infrastructure/ratelimit"
	"github.com/alexmorbo/seasonfill/infrastructure/sonarr"
	"github.com/alexmorbo/seasonfill/internal/observability"
	"github.com/alexmorbo/seasonfill/internal/runtime"
	"github.com/alexmorbo/seasonfill/internal/shared/domain"
)

// NewSonarrClientFactory returns the production SonarrClientFactory
// used by both cmd/server (live reload-bus subscriber wiring) and
// cmd/server/commands (one-shot CLI flows). Each call returns a
// fresh closure that captures globalLimiterPtr + log; invoking the
// closure builds a per-instance Sonarr client with its own request
// limiter.
//
// Moved here from cmd/server/reload_wiring.go (Story 324, B-11 step 2)
// so the commands sub-package can call it without importing its
// parent main package.
func NewSonarrClientFactory(globalLimiterPtr *atomic.Pointer[ratelimit.Limiter], log *slog.Logger) SonarrClientFactory {
	return func(s runtime.InstanceSnapshot) ports.SonarrClient {
		instanceName := domain.InstanceName(s.Name)
		instLimiter := ratelimit.NewFromRPMWithOptions(
			s.RateLimit.RPM, s.RateLimit.Burst,
			ratelimit.WithObserver("per_instance", func(scope string) {
				observability.IncRateLimitThrottled(instanceName, scope)
			}))
		return sonarr.NewWithOptions(instanceName, s.URL, s.APIKey, s.Timeout,
			instLimiter, log,
			sonarr.WithGlobalLimiterPointer(globalLimiterPtr),
			sonarr.WithSearchTimeout(s.SearchTimeout))
	}
}
