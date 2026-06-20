package adapters

import (
	"context"
	"log/slog"
	"sync"

	infraextsvc "github.com/alexmorbo/seasonfill/internal/shared/clients/externalservices"
	infraomdb "github.com/alexmorbo/seasonfill/internal/shared/clients/omdb"
	sharedports "github.com/alexmorbo/seasonfill/internal/shared/ports"
)

// OMDbClientSubscriber rebuilds the live *omdb.Client (held in
// wiring.OMDbClientHolder) when the operator changes the OMDb settings
// row via the UI. Story 352.
//
// Subscription model: registered as a listener on
// ExternalServicesSubscriber (ServiceOMDB). The listener fires
// synchronously inside the parent subscriber's apply() — once on every
// bus publish AND once on every use case Upsert. The subscriber
// compares the incoming Settings against its own cached "last seen"
// row; only a MATERIAL change (api key, proxy URL / user / pass, or
// enabled flip) triggers a rebuild. Same-settings publishes are no-ops.
//
// Failure mode: a factory error keeps the previous client live and
// increments a metrics counter. The operator's Test() flow surfaces the
// real auth/proxy failure separately; this subscriber's job is to
// "swap on change", not "validate settings".
type OMDbClientSubscriber struct {
	holder *OMDbClientHolder
	logger *slog.Logger

	mu       sync.Mutex
	lastSeen infraextsvc.Settings
	primed   bool
	// rebuilds is bumped on every successful Set; exported via
	// RebuildCount for tests.
	rebuilds int
}

// NewOMDbClientSubscriber wires the holder + logger. The caller is
// expected to call (ExternalServicesSubscriber).RegisterListener
// (ServiceOMDB, sub.Apply) at boot.
func NewOMDbClientSubscriber(holder *OMDbClientHolder, logger *slog.Logger) *OMDbClientSubscriber {
	if logger == nil {
		logger = sharedports.DomainLogger(slog.Default(), "omdb")
	}
	return &OMDbClientSubscriber{
		holder: holder,
		logger: logger,
	}
}

// Apply is the SettingsListener entrypoint. Compares against the cached
// "last seen" row; on a material change rebuilds the client + atomically
// swaps it onto the holder. Logs INFO on rebuild with the redacted
// last4 suffix; logs WARN on factory failure (previous client stays
// live).
func (s *OMDbClientSubscriber) Apply(ctx context.Context, settings infraextsvc.Settings) {
	if s == nil || s.holder == nil {
		return
	}

	s.mu.Lock()
	primed := s.primed
	prev := s.lastSeen
	s.mu.Unlock()

	if primed && !materialOMDbChange(prev, settings) {
		return
	}

	// Materially changed (or first call). Disabled → clear the holder
	// so the worker dequeues "client_nil" until the operator re-enables.
	if !settings.Enabled || settings.APIKey == "" {
		previous := s.holder.Set(nil)
		s.commit(settings)
		if previous != nil {
			// Best-effort cleanup of the prior client. OMDb has no
			// background goroutines so Close is a no-op today — kept
			// here as a sentinel so a future Close hook lands without
			// touching this file again.
			_ = previous
			s.logger.InfoContext(ctx, "external_service.client.cleared",
				slog.String("service", string(infraextsvc.ServiceOMDB)),
				slog.Bool("enabled", settings.Enabled),
				slog.Bool("api_key", settings.APIKey != ""),
			)
		}
		return
	}

	client, err := BuildOMDbClient(settings)
	if err != nil {
		s.logger.WarnContext(ctx, "external_service.client.rebuild_failed",
			slog.String("service", string(infraextsvc.ServiceOMDB)),
			slog.String("error", err.Error()),
		)
		// Cache the lastSeen so a follow-up apply with the same broken
		// settings doesn't spam the warn log. A subsequent change still
		// triggers a fresh attempt.
		s.commit(settings)
		return
	}

	previous := s.holder.Set(client)
	s.commit(settings)
	s.logger.InfoContext(ctx, "external_service.client.rebuilt",
		slog.String("service", string(infraextsvc.ServiceOMDB)),
		slog.String("last4", settings.APIKeyLast4),
		slog.String("proxy_scheme", proxySchemeFor(settings.ProxyURL)),
	)
	// OMDb client has no rate-limiter goroutine / background work; the
	// previous *omdb.Client is GC'd once the worker drops its last in-
	// flight reference. No explicit Close needed today.
	_ = previous
}

// RebuildCount returns the number of successful Set operations the
// subscriber has performed. Exported for tests.
func (s *OMDbClientSubscriber) RebuildCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.rebuilds
}

// Current returns the live *omdb.Client (or nil) for inspection.
// Exported for tests.
func (s *OMDbClientSubscriber) Current() *infraomdb.Client {
	if s == nil || s.holder == nil {
		return nil
	}
	c := s.holder.Load()
	return c
}

// Load lets test code peek at the cached "last seen" Settings.
func (s *OMDbClientSubscriber) Load() (infraextsvc.Settings, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lastSeen, s.primed
}

func (s *OMDbClientSubscriber) commit(settings infraextsvc.Settings) {
	s.mu.Lock()
	s.lastSeen = settings
	s.primed = true
	s.rebuilds++
	s.mu.Unlock()
}

// materialOMDbChange returns true when at least one rebuild-relevant
// field differs. last_test_at / last_test_outcome / last_test_message
// are explicitly ignored so a Test() persistence does NOT trigger a
// rebuild (the Test flow doesn't change connectivity, just records the
// verdict).
func materialOMDbChange(a, b infraextsvc.Settings) bool {
	return a.Enabled != b.Enabled ||
		a.APIKey != b.APIKey ||
		a.ProxyURL != b.ProxyURL ||
		a.ProxyUsername != b.ProxyUsername ||
		a.ProxyPassword != b.ProxyPassword
}
