package wiring

import (
	"log/slog"
	"sync/atomic"

	"github.com/alexmorbo/seasonfill/application/ports"
	"github.com/alexmorbo/seasonfill/application/scan"
	"github.com/alexmorbo/seasonfill/cmd/server/adapters"
	"github.com/alexmorbo/seasonfill/infrastructure/ratelimit"
	"github.com/alexmorbo/seasonfill/infrastructure/reload"
	"github.com/alexmorbo/seasonfill/interface/http/handlers"
	"github.com/alexmorbo/seasonfill/internal/config"
	"github.com/alexmorbo/seasonfill/internal/runtime"
)

// SonarrBundle holds the per-instance Sonarr wiring shared between the
// HTTP layer, the scan / watchdog / regrab use cases, and the reload
// bus. Constructed once at boot by BuildSonarr; mutated downstream
// only via the Holder (the reload OnApplied fanout calls Replace).
//
// Field-level invariants — see story 332 §Risks for the rationale:
//
//   - Holder is a pointer-typed handle. Its identity is preserved
//     across reload — every reload-aware closure (4 call sites in
//     scan.Syncer.Lookup, webhook UC GUIDCooldownLookup /
//     SonarrClientFor / InstanceFor, seriesdetail SonarrFor,
//     torrentsync sonarrFor) reads through Holder.Load and observes
//     whichever snapshot the fanout published.
//
//   - GlobalLimiterPtr is a pointer to a heap-allocated atomic. The
//     ClientFactory captures it at construction; the
//     GlobalRateLimiterSubscriber Stores into it on reload; the
//     testcontext hook reads from it. Same atomic cell everywhere.
//
//   - InstanceRegistry is a value (not pointer) per design doc §3.3.
//     It is a thin adapter struct that captures InstanceReg and
//     delegates Get() through InstanceReg.Load().
type SonarrBundle struct {
	ClientFactory       reload.SonarrClientFactory
	ClientsByName       map[string]ports.SonarrClient
	SonarrClients       []ports.SonarrClient
	ScanInstances       []scan.Instance
	ScanInstancesByName map[string]scan.Instance
	CfgByName           map[string]config.HealthCheckConfig
	Holder              *adapters.InstanceMapHolder
	InstanceReg         handlers.InstanceRegistry
	InstanceRegistry    adapters.RegrabInstanceRegistry
	GlobalLimiterPtr    *atomic.Pointer[ratelimit.Limiter]
}

// BuildSonarr seeds the global rate-limiter pointer, builds the
// production SonarrClientFactory closure, instantiates the boot client
// set, materialises the scan.Instance views (slice + by-name map +
// HealthCheck cfg by-name), wraps everything in an InstanceMapHolder
// for reload-aware lookups, and adapts the holder to the handler-side
// InstanceRegistry + regrab-side RegrabInstanceRegistry.
//
// The returned bundle is the single source of truth for per-instance
// Sonarr wiring; every downstream consumer (scan UC, watchdog,
// regrab, webhook UC, healthcheck, seriesdetail composer, torrentsync
// reconciler, HTTP handlers) reads from the bundle's handles instead
// of re-deriving them.
//
// snap is the runtime configuration snapshot from BuildRuntimeConfig.
// The bundle reads snap.GlobalRateLimit (to seed the limiter) and
// snap.Instances (to enumerate per-instance clients).
//
// No error path — every step is in-memory construction. The signature
// returns error to leave room for future seed-or-validate logic
// without a downstream signature churn.
func BuildSonarr(snap runtime.Snapshot, log *slog.Logger) (*SonarrBundle, error) {
	// Single shared global limiter pointer (live-reloaded). Heap-
	// allocate the atomic so its address is stable across the
	// function return — the ClientFactory captures &limiterPtr and
	// the GlobalRateLimiterSubscriber Stores into it on reload.
	// Seed from the boot snapshot so the first publish's subscriber
	// diff-skip works.
	limiterPtr := new(atomic.Pointer[ratelimit.Limiter])
	limiterPtr.Store(reload.DefaultGlobalLimiterFactory(
		snap.GlobalRateLimit.RPM, snap.GlobalRateLimit.Burst))

	clientFactory := reload.NewSonarrClientFactory(limiterPtr, log)

	n := len(snap.Instances)
	clientsByName := make(map[string]ports.SonarrClient, n)
	for _, sc := range snap.Instances {
		clientsByName[sc.Name] = clientFactory(sc)
	}

	sonarrClients := make([]ports.SonarrClient, 0, n)
	scanInstances := make([]scan.Instance, 0, n)
	scanInstancesByName := make(map[string]scan.Instance, n)
	cfgByName := make(map[string]config.HealthCheckConfig, n)
	for _, sc := range snap.Instances {
		c := clientsByName[sc.Name]
		sonarrClients = append(sonarrClients, c)
		si := scan.Instance{Config: sc, Client: c}
		scanInstances = append(scanInstances, si)
		scanInstancesByName[sc.Name] = si
		cfgByName[sc.Name] = config.NewHealthCheckConfig(sc.HealthCheck)
	}

	holder := adapters.NewInstanceMapHolder(scanInstancesByName)

	// InstanceReg is a handler-side accessor: its Load closure is
	// reload-aware via the holder. Build it once here so every
	// downstream caller (webhookReconciler, qbitSettingsUC,
	// httpServer.NewServer, regrabUC) reads through the same
	// value — no per-site reconstruction.
	instanceReg := handlers.InstanceRegistry{Load: holder.Load}

	return &SonarrBundle{
		ClientFactory:       clientFactory,
		ClientsByName:       clientsByName,
		SonarrClients:       sonarrClients,
		ScanInstances:       scanInstances,
		ScanInstancesByName: scanInstancesByName,
		CfgByName:           cfgByName,
		Holder:              holder,
		InstanceReg:         instanceReg,
		InstanceRegistry:    adapters.NewRegrabInstanceRegistry(instanceReg),
		GlobalLimiterPtr:    limiterPtr,
	}, nil
}
