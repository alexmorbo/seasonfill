package adapters

import (
	"context"
	"log/slog"
	"sync"
	"time"

	infraextsvc "github.com/alexmorbo/seasonfill/internal/shared/clients/externalservices"
	"github.com/alexmorbo/seasonfill/internal/shared/clients/tmdb"
	sharedports "github.com/alexmorbo/seasonfill/internal/shared/ports"
)

// TMDBClientSubscriber rebuilds the live *tmdb.Client (held in
// TMDBClientHolder) when the operator changes the TMDB settings row via
// the UI. Story 352. Story 470 (B-7) — also fires OnFirstActivation
// once per nil→non-nil client transition so the cold-start sweep runs
// the moment a key is saved (no pod restart, no 60s wait).
//
// Subscription model: registered as a listener on
// ExternalServicesSubscriber (ServiceTMDB). The listener fires
// synchronously inside the parent subscriber's apply() — once on every
// bus publish AND once on every use case Upsert. The subscriber
// compares the incoming Settings against its own cached "last seen"
// row; only a MATERIAL change triggers a rebuild.
//
// Lifecycle: a successful rebuild Swap()s the new client onto the
// holder and asynchronously Close()s the previous client AFTER a drain
// delay (default 30s). The drain guards against in-flight requests
// against the old client's *http.Client (which is still in use until
// the worker goroutine releases its last reference). Tests can shrink
// the delay via WithCloseDelay.
//
// Story 470 (B-7): post-470 wiring ALWAYS allocates the holder (even
// when TMDB is unconfigured at boot). The defensive nil-holder branch
// in Apply is therefore a programming-error log only — production
// wiring guarantees a non-nil holder.
type TMDBClientSubscriber struct {
	holder    *TMDBClientHolder
	factoryFn func(infraextsvc.Settings) (*tmdb.Client, error)
	logger    *slog.Logger

	closeDelay time.Duration
	closeFn    func(*tmdb.Client) // closed after closeDelay; tests override
	wg         sync.WaitGroup

	// 470 (B-7): one-shot activation callback. Fires on every
	// (prev empty key → settings non-empty key) transition. Nil-OK.
	onFirstActivation func(context.Context)

	mu       sync.Mutex
	lastSeen infraextsvc.Settings
	primed   bool
	rebuilds int
	// 470 (B-7): tracks whether the holder has ever been populated since
	// boot OR since the last clear. Set to true on a successful Set with
	// a non-nil client; flipped back to false in the clear branch. Used
	// to fire OnFirstActivation exactly once per "no client" → "client"
	// transition.
	activated bool
}

// NewTMDBClientSubscriber wires the holder + factory + logger. holder
// MAY be nil — Apply short-circuits and logs a single WARN per
// invocation in that case (signalling boot-disabled state).
func NewTMDBClientSubscriber(
	holder *TMDBClientHolder,
	factoryCfg TMDBClientFactoryConfig,
	logger *slog.Logger,
) *TMDBClientSubscriber {
	if logger == nil {
		logger = sharedports.DomainLogger(slog.Default(), "tmdb")
	}
	return &TMDBClientSubscriber{
		holder: holder,
		factoryFn: func(s infraextsvc.Settings) (*tmdb.Client, error) {
			return BuildTMDBClient(s, factoryCfg)
		},
		logger:     logger,
		closeDelay: defaultTMDBCloseDelay,
		closeFn:    func(c *tmdb.Client) { c.Close() },
	}
}

// defaultTMDBCloseDelay is the wall-clock window the subscriber waits
// before Close()ing the previous *tmdb.Client. 30s matches the sonarr
// drain delay (drain old in-flight requests + per-attempt retries).
const defaultTMDBCloseDelay = 30 * time.Second

// WithCloseDelay overrides the post-swap close delay. Tests use this to
// drive Close synchronously inside the test goroutine.
func (s *TMDBClientSubscriber) WithCloseDelay(d time.Duration) *TMDBClientSubscriber {
	s.closeDelay = d
	return s
}

// WithCloseFn lets tests intercept Close calls. Production wiring leaves
// the default (c.Close()).
func (s *TMDBClientSubscriber) WithCloseFn(fn func(*tmdb.Client)) *TMDBClientSubscriber {
	if fn != nil {
		s.closeFn = fn
	}
	return s
}

// WithFactoryFn overrides the factory used to rebuild clients. Tests
// substitute a stub that returns a fixture *tmdb.Client (or an error).
func (s *TMDBClientSubscriber) WithFactoryFn(fn func(infraextsvc.Settings) (*tmdb.Client, error)) *TMDBClientSubscriber {
	if fn != nil {
		s.factoryFn = fn
	}
	return s
}

// WithOnFirstActivation registers a callback invoked once per
// nil→non-nil client transition (boot-disabled→enabled, OR
// clear-then-set). The callback runs on the subscriber goroutine after
// the holder swap; it MUST be non-blocking (cold-start enqueue loop is
// O(rows) but each Enqueue is non-blocking). nil resets the callback.
//
// Story 470 (B-7) — production wiring passes the EnrichmentBundle's
// OnFirstActivation closure, which runs BackfillSeries once so the
// operator sees enrichment converging within seconds of save.
func (s *TMDBClientSubscriber) WithOnFirstActivation(fn func(context.Context)) *TMDBClientSubscriber {
	s.onFirstActivation = fn
	return s
}

// Apply is the SettingsListener entrypoint. See file-level doc.
func (s *TMDBClientSubscriber) Apply(ctx context.Context, settings infraextsvc.Settings) {
	if s == nil {
		return
	}
	if s.holder == nil {
		// 470 (B-7): post-470 wiring always supplies a non-nil holder.
		// A nil holder here is a programming error — log loudly so it
		// surfaces in tests + ops.
		s.logger.ErrorContext(ctx, "tmdb.subscriber.nil_holder",
			slog.String("service", string(infraextsvc.ServiceTMDB)),
			slog.String("note", "wiring must always allocate the holder; this is a Story 470 invariant"),
		)
		return
	}

	s.mu.Lock()
	primed := s.primed
	prev := s.lastSeen
	wasActivated := s.activated
	s.mu.Unlock()

	if primed && !materialTMDBChange(prev, settings) {
		return
	}

	if !settings.Enabled || settings.APIKey == "" {
		previous := s.holder.Set(nil)
		s.commitWithActivated(settings, false) // 470: clear activated so a re-set fires the hook again
		s.logger.InfoContext(ctx, "external_service.client.cleared",
			slog.String("service", string(infraextsvc.ServiceTMDB)),
			slog.Bool("enabled", settings.Enabled),
			slog.Bool("api_key", settings.APIKey != ""),
		)
		s.scheduleClose(previous)
		return
	}

	client, err := s.factoryFn(settings)
	if err != nil {
		s.logger.WarnContext(ctx, "external_service.client.rebuild_failed",
			slog.String("service", string(infraextsvc.ServiceTMDB)),
			slog.String("error", err.Error()),
		)
		// 470: commit but preserve activated flag — a factory failure
		// must NOT pretend we activated. wasActivated propagates verbatim.
		s.commitWithActivated(settings, wasActivated)
		return
	}

	previous := s.holder.Set(client)
	s.commitWithActivated(settings, true) // 470: mark activated
	s.logger.InfoContext(ctx, "external_service.client.rebuilt",
		slog.String("service", string(infraextsvc.ServiceTMDB)),
		slog.String("last4", settings.APIKeyLast4),
		slog.String("proxy_scheme", proxySchemeFor(settings.ProxyURL)),
	)
	s.scheduleClose(previous)

	// 470 (B-7): fire the first-activation hook exactly once per
	// transition from "no client" → "client present". wasActivated
	// captured under the mutex above — race-free against a concurrent
	// second Apply on the same subscriber goroutine (the
	// SettingsListener fan-out is single-threaded per subscriber).
	if !wasActivated && s.onFirstActivation != nil {
		s.logger.InfoContext(ctx, "tmdb.subscriber.first_activation",
			slog.String("trigger", "key_added_at_runtime"),
		)
		s.onFirstActivation(ctx)
	}
}

// scheduleClose drains the previous client off the goroutine after
// closeDelay so in-flight requests have time to complete. wg lets
// Wait() block tests for the drain timer.
func (s *TMDBClientSubscriber) scheduleClose(previous *tmdb.Client) {
	if previous == nil {
		return
	}
	s.wg.Go(func() {
		if s.closeDelay > 0 {
			time.Sleep(s.closeDelay)
		}
		s.closeFn(previous)
	})
}

// Wait blocks until all scheduled Close goroutines have completed.
// Exported for tests; production wiring relies on the OS killing the
// process eventually.
func (s *TMDBClientSubscriber) Wait() { s.wg.Wait() }

// RebuildCount returns the number of successful Apply operations that
// materially changed the cached settings.
func (s *TMDBClientSubscriber) RebuildCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.rebuilds
}

// Current returns the live *tmdb.Client (or nil) for inspection.
func (s *TMDBClientSubscriber) Current() *tmdb.Client {
	if s == nil || s.holder == nil {
		return nil
	}
	return s.holder.Load()
}

// LoadLastSeen lets tests peek at the cached "last seen" Settings.
func (s *TMDBClientSubscriber) LoadLastSeen() (infraextsvc.Settings, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lastSeen, s.primed
}

// commitWithActivated extends the prior commit() with the activated
// flag setter. Story 470 (B-7) — activated participates in the
// OnFirstActivation gate.
func (s *TMDBClientSubscriber) commitWithActivated(settings infraextsvc.Settings, activated bool) {
	s.mu.Lock()
	s.lastSeen = settings
	s.primed = true
	s.rebuilds++
	s.activated = activated
	s.mu.Unlock()
}

// materialTMDBChange mirrors materialOMDbChange for TMDB. Test verdict
// columns are ignored for the same reason.
func materialTMDBChange(a, b infraextsvc.Settings) bool {
	return a.Enabled != b.Enabled ||
		a.APIKey != b.APIKey ||
		a.ProxyURL != b.ProxyURL ||
		a.ProxyUsername != b.ProxyUsername ||
		a.ProxyPassword != b.ProxyPassword
}
