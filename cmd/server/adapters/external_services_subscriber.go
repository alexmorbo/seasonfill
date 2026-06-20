package adapters

import (
	"context"
	"log/slog"
	"maps"
	"sync"

	appext "github.com/alexmorbo/seasonfill/internal/enrichment/app/externalservices"
	"github.com/alexmorbo/seasonfill/internal/runtime"
	infra "github.com/alexmorbo/seasonfill/internal/shared/clients/externalservices"
	sharedports "github.com/alexmorbo/seasonfill/internal/shared/ports"
)

// ExternalServicesSubscriber re-decrypts the three external_service_settings
// rows on every bus publish and exposes the merged (env > DB) plaintext
// settings via Get(). Phase C/D clients will subscribe by calling Get()
// each time they construct a fresh *http.Client — there is no
// subscription fan-out in this story; downstream stories add the cached-
// client wiring.
//
// The subscriber implements appext.Publisher so the use case's Upsert()
// path can republish synchronously after a write — this guarantees the
// next Get() reflects the operator's change without waiting for the next
// nightly publish.
//
// Bootstrap ordering: NewExternalServicesSubscriber may be constructed
// with a nil use case (so it can be injected as Publisher into the use
// case). SetUseCase MUST be called before Start. Get() before SetUseCase
// returns a zero-value Settings for any service.
//
// Story 352 — RegisterListener lets downstream client subscribers
// (OMDb/TMDB client holders) react to a settings change synchronously
// after the cache has been refreshed. Listeners are invoked from apply()
// while no internal lock is held, so callees may call back into Get()
// without deadlocking. Listeners MUST be cheap (atomic.Pointer.Store,
// short factory call); they run on the bus goroutine.
type ExternalServicesSubscriber struct {
	bus    *runtime.Bus
	logger *slog.Logger

	ucMu sync.RWMutex
	uc   *appext.UseCase

	mu      sync.RWMutex
	current map[infra.Service]infra.Settings

	listenersMu sync.RWMutex
	listeners   map[infra.Service][]SettingsListener
}

// SettingsListener is the callback shape RegisterListener consumes.
// Invoked synchronously after each apply() with the post-merge Settings
// for the watched service. Implementations are expected to compare the
// received settings against their own cached state and rebuild only on a
// material change.
type SettingsListener func(ctx context.Context, s infra.Settings)

func NewExternalServicesSubscriber(bus *runtime.Bus, logger *slog.Logger) *ExternalServicesSubscriber {
	if logger == nil {
		logger = sharedports.DomainLogger(slog.Default(), "boot")
	}
	return &ExternalServicesSubscriber{
		bus:       bus,
		logger:    logger,
		current:   make(map[infra.Service]infra.Settings),
		listeners: make(map[infra.Service][]SettingsListener),
	}
}

// RegisterListener appends fn to the listener list for svc. Listeners
// fire AFTER every successful apply() with the post-merge Settings.
// Multiple listeners per service are supported (insertion order is
// preserved). nil-fn is silently dropped. Safe to call before Start.
func (s *ExternalServicesSubscriber) RegisterListener(svc infra.Service, fn SettingsListener) {
	if fn == nil {
		return
	}
	s.listenersMu.Lock()
	s.listeners[svc] = append(s.listeners[svc], fn)
	s.listenersMu.Unlock()
}

// SetUseCase wires the use case after construction. Required to break
// the construction cycle (the use case needs the subscriber as
// Publisher; the subscriber needs the use case to read EffectiveSettings).
// Must be called before Start.
func (s *ExternalServicesSubscriber) SetUseCase(uc *appext.UseCase) {
	s.ucMu.Lock()
	s.uc = uc
	s.ucMu.Unlock()
}

// Start subscribes to the bus under the name "external-services" and
// drives apply() on every publish. The ready hook fires after Subscribe
// registers — main passes a closer so the boot barrier can wait. Start
// then primes the cache eagerly so the first downstream Get() works
// before any bus publish.
func (s *ExternalServicesSubscriber) Start(ctx context.Context, ready func()) {
	var opts []runtime.SubscribeOption
	if ready != nil {
		opts = append(opts, runtime.WithReady(ready))
	}
	ch := s.bus.Subscribe("external-services", opts...)
	go func() {
		for {
			select {
			case <-ctx.Done():
				s.bus.Unsubscribe("external-services")
				return
			case _, ok := <-ch:
				if !ok {
					return
				}
				s.apply(ctx)
			}
		}
	}()
	// Prime the cache eagerly. apply is a no-op if SetUseCase hasn't
	// been called yet (defensive — production wiring always calls
	// SetUseCase first).
	s.apply(ctx)
}

// Publish satisfies appext.Publisher. The use case calls Publish() after
// an Upsert; we re-read straight from the use case rather than waiting
// for a bus event so the operator's request returns a post-write masked
// view.
func (s *ExternalServicesSubscriber) Publish(ctx context.Context) {
	s.apply(ctx)
}

func (s *ExternalServicesSubscriber) apply(ctx context.Context) {
	s.ucMu.RLock()
	uc := s.uc
	s.ucMu.RUnlock()
	if uc == nil {
		return
	}
	next := make(map[infra.Service]infra.Settings, len(infra.AllServices))
	for _, svc := range infra.AllServices {
		eff, src, err := uc.EffectiveSettingsWithSource(ctx, svc)
		if err != nil {
			s.logger.WarnContext(ctx, "external_services.subscriber.apply_failed",
				slog.String("service", string(svc)), slog.Any("err", err))
			next[svc] = infra.Settings{Service: svc}
			continue
		}
		next[svc] = eff
		// FIX-007: surface the resolved priority so the operator can
		// confirm a fresh-install env fallback path is active without
		// greping enrichment.disabled after the fact. NEVER log
		// plaintext — only the FieldSource enum + the cosmetic last4.
		s.logger.InfoContext(ctx, "extsvc.source",
			slog.String("service", string(svc)),
			slog.Bool("enabled", eff.Enabled),
			slog.String("api_key", string(src.APIKey)),
			slog.String("proxy_url", string(src.ProxyURL)),
			slog.String("proxy_user", string(src.ProxyUsername)),
			slog.String("proxy_pass", string(src.ProxyPassword)),
			slog.String("last4", eff.APIKeyLast4),
		)
	}
	s.mu.Lock()
	s.current = next
	s.mu.Unlock()

	s.fanOut(ctx, next)
}

// fanOut invokes every per-service listener with the post-merge Settings.
// Snapshots the listener slice under the read lock so a concurrent
// RegisterListener cannot race with the call; the actual callbacks run
// UNLOCKED so they may call back into Get() / RegisterListener without
// self-deadlock.
func (s *ExternalServicesSubscriber) fanOut(ctx context.Context, next map[infra.Service]infra.Settings) {
	s.listenersMu.RLock()
	fans := make(map[infra.Service][]SettingsListener, len(next))
	for svc, fns := range s.listeners {
		if len(fns) == 0 {
			continue
		}
		cp := make([]SettingsListener, len(fns))
		copy(cp, fns)
		fans[svc] = cp
	}
	s.listenersMu.RUnlock()
	for svc, fns := range fans {
		settings := next[svc]
		for _, fn := range fns {
			fn(ctx, settings)
		}
	}
}

// SetCurrentForTest hand-primes the internal cache; test-only helper
// for the Story 352 listener fan-out tests so they don't need to spin
// up a use case + bus.
func (s *ExternalServicesSubscriber) SetCurrentForTest(m map[infra.Service]infra.Settings) {
	s.mu.Lock()
	s.current = make(map[infra.Service]infra.Settings, len(m))
	maps.Copy(s.current, m)
	s.mu.Unlock()
}

// FanOutForTest invokes the listener fan-out path directly; test-only.
// Mirrors the tail of apply() — production callers should let apply()
// drive this.
func (s *ExternalServicesSubscriber) FanOutForTest(ctx context.Context) {
	s.mu.RLock()
	next := make(map[infra.Service]infra.Settings, len(s.current))
	maps.Copy(next, s.current)
	s.mu.RUnlock()
	s.fanOut(ctx, next)
}

// Get returns the latest merged Settings for svc. Returns a zero-value
// Settings (with Service populated) when the cache hasn't been primed
// for that service. Callers must NOT mutate the returned value.
func (s *ExternalServicesSubscriber) Get(svc infra.Service) infra.Settings {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if v, ok := s.current[svc]; ok {
		return v
	}
	return infra.Settings{Service: svc}
}
