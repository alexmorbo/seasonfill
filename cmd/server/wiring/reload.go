package wiring

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/alexmorbo/seasonfill/application/ports"
	"github.com/alexmorbo/seasonfill/application/regrab"
	"github.com/alexmorbo/seasonfill/application/scan"
	"github.com/alexmorbo/seasonfill/cmd/server/adapters"
	"github.com/alexmorbo/seasonfill/infrastructure/reload"
	"github.com/alexmorbo/seasonfill/infrastructure/watchdog"
	"github.com/alexmorbo/seasonfill/interface/http/middleware"
	"github.com/alexmorbo/seasonfill/internal/config"
	"github.com/alexmorbo/seasonfill/internal/runtime"
)

// subscriberReadyTimeout bounds how long StartSubscribers will wait
// for every subscriber to register its bus.Subscribe call. Defensive
// only — in normal operation each goroutine reaches Subscribe in
// microseconds. If we hit the timeout, the process is broken: main
// exits non-zero with a clear log line.
const subscriberReadyTimeout = 2 * time.Second

// sweepIntervalSetter is the narrow contract BuildOnAppliedFanout
// needs from the cooldown sweeper. Keeping it as an interface lets the
// fan-out be unit-tested without spinning up a real sweepLoop.
type sweepIntervalSetter interface {
	SetInterval(d time.Duration)
}

// regrabSwapper is the narrow contract BuildOnAppliedFanout needs from
// the regrab loop. Decoupled into an interface so the fanout test can
// stub it without spinning up a real per-instance goroutine.
type regrabSwapper interface {
	SwapSettings(map[string]regrab.Settings)
}

// torrentsyncSwapper is the narrow contract BuildOnAppliedFanout needs
// from the torrentsync loop. Identical SwapSettings shape as regrab
// — both loops consume the same qbit-settings projection from the
// reload-bus snapshot.
type torrentsyncSwapper interface {
	SwapSettings(map[string]regrab.Settings)
}

// qbitSettingsLoader is the narrow contract BuildOnAppliedFanout needs
// to project the qbit-settings repo into a map keyed by instance name.
// In production it's a closure over QbitSettingsRepository.List +
// instance lookup; in tests it's a fake that returns a fixed map.
type qbitSettingsLoader interface {
	Load(ctx context.Context) map[string]regrab.Settings
}

// BuildOnAppliedFanout wires the OnApplied hook that updates everything
// that depends on the freshly-rebuilt sonarr-client set: the scan UC
// instance list, the holder map HTTP handlers iterate, the health
// checker's registry membership + preflight client list, and the
// cooldown sweep cadence. Running ALL of that inside
// SonarrClientsSubscriber.apply (under its lock) closes the
// cross-subscriber race that would otherwise let one fan-out observer
// (e.g. the old HealthRegistrySubscriber) read a stale View().All()
// before the live set was rebuilt.
func BuildOnAppliedFanout(rootCtx context.Context, scanUC *scan.UseCase, holder *adapters.InstanceMapHolder, checker reload.HealthChecker, wd *watchdog.Watchdog, sweeper sweepIntervalSetter, regrabLoop regrabSwapper, torrentsyncLoop torrentsyncSwapper, qbitLoader qbitSettingsLoader, log *slog.Logger) reload.OnAppliedFunc {
	return func(snap runtime.Snapshot, clients map[string]ports.SonarrClient) {
		nextSlice := make([]scan.Instance, 0, len(snap.Instances))
		nextMap := make(map[string]scan.Instance, len(snap.Instances))
		clientSlice := make([]ports.SonarrClient, 0, len(snap.Instances))
		names := make([]string, 0, len(snap.Instances))
		cfgByName := make(map[string]config.HealthCheckConfig, len(snap.Instances))
		for _, inst := range snap.Instances {
			client, ok := clients[inst.Name]
			if !ok || client == nil {
				// Should be impossible: clients is built by the same
				// apply iterating the same snap.Instances. Log and
				// skip rather than mishandle.
				log.Warn("onApplied fanout: client missing for instance",
					slog.String("instance", inst.Name))
				continue
			}
			si := scan.Instance{Config: inst, Client: client}
			nextSlice = append(nextSlice, si)
			nextMap[inst.Name] = si
			clientSlice = append(clientSlice, client)
			names = append(names, inst.Name)
			cfgByName[inst.Name] = config.NewHealthCheckConfig(inst.HealthCheck)
		}
		scanUC.SwapInstances(nextSlice)
		holder.Replace(nextMap)
		checker.ReplaceClients(clientSlice, names)
		wd.SwapConfigs(cfgByName)
		if sweeper != nil {
			sweeper.SetInterval(snap.Scan.CooldownSweep)
		}
		scanUC.SwapDryRun(snap.DryRun)

		// Phase 10 — Watchdog regrab loop fanout. Loaded fresh from the
		// repo on every publish so the loop sees the latest qBit settings
		// without a separate subscriber. The lookup runs under the
		// SonarrClientsSubscriber lock (we're inside its fanout closure)
		// so concurrent SwapSettings calls cannot interleave.
		//
		// Story 220 (A-2) — torrentsync loop consumes the same projection;
		// we reuse the qbitMap we already fetched rather than calling
		// qbitLoader.Load a second time per publish.
		if (regrabLoop != nil || torrentsyncLoop != nil) && qbitLoader != nil {
			qbitMap := qbitLoader.Load(rootCtx)
			if regrabLoop != nil {
				regrabLoop.SwapSettings(qbitMap)
			}
			if torrentsyncLoop != nil {
				torrentsyncLoop.SwapSettings(qbitMap)
			}
		}

		go checker.Preflight(rootCtx)
	}
}

// SubscriberDeps groups the cross-bundle inputs StartSubscribers needs
// that are not themselves bundles. Kept as a struct so the signature
// stays readable even as new subscribers join over time.
type SubscriberDeps struct {
	// Snap is the boot snapshot — its GlobalRateLimit field seeds the
	// GlobalRateLimiterSubscriber.
	Snap runtime.Snapshot
	// Engine is the gin engine the AuthMiddlewareSubscriber rewires on
	// each apply. Read from the http server: httpServer.Engine().
	Engine *gin.Engine
	// AuthRuntimePtr is the live AuthRuntime pointer the
	// AuthMiddlewareSubscriber atomically swaps. Read from the http
	// server: httpServer.AuthHandler().AuthRuntime() (nil-OK when auth
	// disabled).
	AuthRuntimePtr *middleware.AuthRuntimePointer
	// ClientSecretEnv is the OIDC client_secret env override forwarded to
	// the AuthMiddlewareSubscriber. From bootCfg.Auth.OIDCClientSecret.
	ClientSecretEnv string
}

// StartSubscribers launches every reload subscriber under bgWG and
// blocks until each has registered on the bus, then returns. ctx is
// the long-lived rootCtx — SIGTERM cancels it and every subscriber
// drains. Returns the SchedulerSubscriber + SonarrClientsSubscriber
// so server.go can hand them off to shutdown and to the testcontext
// hook (notifyTestContext).
//
// If any subscriber fails to register within subscriberReadyTimeout
// the function returns an error and the caller is expected to exit
// non-zero. In normal operation this returns within microseconds.
//
// The function takes wired bundles instead of the pre-344 raw-deps
// list. Each bundle field consumed below preserves its pre-344 name
// verbatim so the behavior is byte-equivalent — only the
// argument-construction site (server.go) changed.
func StartSubscribers(
	ctx context.Context,
	bgWG *sync.WaitGroup,
	bus *runtime.Bus,
	persistence *PersistenceBundle,
	sonarr *SonarrBundle,
	scan *ScanBundle,
	watchdogBundle *WatchdogBundle,
	regrab *RegrabBundle,
	torrentsync *TorrentsyncBundle,
	scheduler *SchedulerBundle,
	deps SubscriberDeps,
	log *slog.Logger,
) (*reload.SchedulerSubscriber, *reload.SonarrClientsSubscriber, error) {
	subSched := reload.NewSchedulerSubscriber(ctx, scheduler.BootScheduler, scan.ScanUC,
		scheduler.Factory, log)
	subClients := reload.NewSonarrClientsSubscriber(sonarr.ClientsByName, sonarr.ClientFactory, log).
		WithWaitGroup(bgWG).
		WithOnApplied(BuildOnAppliedFanout(
			ctx,
			scan.ScanUC,
			sonarr.Holder,
			watchdogBundle.Checker,
			watchdogBundle.Watchdog,
			scan.Sweeper,
			regrab.RegrabLoop,
			torrentsync.Loop,
			regrab.QbitLoader,
			log,
		))

	subRate := reload.NewGlobalRateLimiterSubscriber(sonarr.GlobalLimiterPtr,
		reload.DefaultGlobalLimiterFactory, deps.Snap.GlobalRateLimit, log)
	subAuth := reload.NewAuthMiddlewareSubscriber(deps.AuthRuntimePtr, deps.Engine, log,
		persistence.RuntimeRepo, deps.ClientSecretEnv)
	// Note: NewAuthMiddlewareSubscriber positional order is
	// (ptr, engine, logger, runtimeRepo, clientSecretEnv) per
	// infrastructure/reload/auth_middleware_subscriber.go — preserved
	// verbatim from the pre-344 call site.

	runners := []func(context.Context, *runtime.Bus, func()){
		subSched.Run, subClients.Run, subRate.Run, subAuth.Run,
	}
	names := []string{"scheduler", "sonarrClients", "globalRateLimiter", "authMiddleware"}

	ready := make([]chan struct{}, len(runners))
	for i := range ready {
		ready[i] = make(chan struct{})
	}

	for i, run := range runners {
		bgWG.Add(1)
		runFn := run
		readyCh := ready[i]
		go func() {
			defer bgWG.Done()
			runFn(ctx, bus, func() { close(readyCh) })
		}()
	}

	if err := waitAllReady(ready, subscriberReadyTimeout, names, log); err != nil {
		return nil, nil, err
	}

	return subSched, subClients, nil
}

// waitAllReady blocks until every channel in `ready` is closed, or
// until `timeout` elapses. On timeout it returns an error naming the
// subscribers that failed to register; their goroutines remain
// running (they'll get cleaned up when ctx is cancelled) but main
// is expected to log + exit.
func waitAllReady(ready []chan struct{}, timeout time.Duration, names []string, log *slog.Logger) error {
	allReady := make(chan struct{})
	go func() {
		var wg sync.WaitGroup
		wg.Add(len(ready))
		for _, r := range ready {
			r := r
			go func() {
				defer wg.Done()
				<-r
			}()
		}
		wg.Wait()
		close(allReady)
	}()

	select {
	case <-allReady:
		return nil
	case <-time.After(timeout):
		var stuck []string
		for i, r := range ready {
			select {
			case <-r:
			default:
				stuck = append(stuck, names[i])
			}
		}
		log.Error("StartSubscribers: timeout waiting for subscribers to register",
			slog.Duration("timeout", timeout),
			slog.Any("stuck", stuck))
		return fmt.Errorf("StartSubscribers: timeout waiting for subscribers to register: %v", stuck)
	}
}
