package adapters

import (
	"context"
	"log/slog"
	"sync"

	appext "github.com/alexmorbo/seasonfill/application/externalservices"
	infra "github.com/alexmorbo/seasonfill/infrastructure/externalservices"
	"github.com/alexmorbo/seasonfill/internal/runtime"
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
type ExternalServicesSubscriber struct {
	bus    *runtime.Bus
	logger *slog.Logger

	ucMu sync.RWMutex
	uc   *appext.UseCase

	mu      sync.RWMutex
	current map[infra.Service]infra.Settings
}

func NewExternalServicesSubscriber(bus *runtime.Bus, logger *slog.Logger) *ExternalServicesSubscriber {
	if logger == nil {
		logger = slog.Default()
	}
	return &ExternalServicesSubscriber{
		bus:     bus,
		logger:  logger,
		current: make(map[infra.Service]infra.Settings),
	}
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
		eff, err := uc.EffectiveSettings(ctx, svc)
		if err != nil {
			s.logger.WarnContext(ctx, "external_services.subscriber.apply_failed",
				slog.String("service", string(svc)), slog.Any("err", err))
			next[svc] = infra.Settings{Service: svc}
			continue
		}
		next[svc] = eff
	}
	s.mu.Lock()
	s.current = next
	s.mu.Unlock()
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
