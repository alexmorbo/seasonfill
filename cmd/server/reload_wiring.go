package main

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/alexmorbo/seasonfill/application/ports"
	"github.com/alexmorbo/seasonfill/application/regrab"
	"github.com/alexmorbo/seasonfill/application/scan"
	"github.com/alexmorbo/seasonfill/infrastructure/ratelimit"
	"github.com/alexmorbo/seasonfill/infrastructure/reload"
	"github.com/alexmorbo/seasonfill/infrastructure/scheduler"
	"github.com/alexmorbo/seasonfill/infrastructure/sonarr"
	"github.com/alexmorbo/seasonfill/infrastructure/watchdog"
	"github.com/alexmorbo/seasonfill/interface/http/middleware"
	"github.com/alexmorbo/seasonfill/internal/config"
	"github.com/alexmorbo/seasonfill/internal/observability"
	"github.com/alexmorbo/seasonfill/internal/runtime"
)

// subscriberReadyTimeout bounds how long startSubscribers will wait
// for every subscriber to register its bus.Subscribe call. Defensive
// only — in normal operation each goroutine reaches Subscribe in
// microseconds. If we hit the timeout, the process is broken: main
// exits non-zero with a clear log line.
const subscriberReadyTimeout = 2 * time.Second

// instanceMapHolder is the shared, mutex-protected container the
// OnApplied fan-out writes into and rescanUC reads from. A plain map
// would race; using sync.Map loses the by-name shape the caller needs.
type instanceMapHolder struct {
	mu sync.RWMutex
	m  map[string]scan.Instance
}

func newInstanceMapHolder(initial map[string]scan.Instance) *instanceMapHolder {
	cp := make(map[string]scan.Instance, len(initial))
	for k, v := range initial {
		cp[k] = v
	}
	return &instanceMapHolder{m: cp}
}

func (h *instanceMapHolder) replace(next map[string]scan.Instance) {
	h.mu.Lock()
	h.m = next
	h.mu.Unlock()
}

func (h *instanceMapHolder) load() map[string]scan.Instance {
	h.mu.RLock()
	defer h.mu.RUnlock()
	out := make(map[string]scan.Instance, len(h.m))
	for k, v := range h.m {
		out[k] = v
	}
	return out
}

func buildSonarrClientFactory(globalLimiterPtr *atomic.Pointer[ratelimit.Limiter], log *slog.Logger) reload.SonarrClientFactory {
	return func(s runtime.InstanceSnapshot) ports.SonarrClient {
		instanceName := s.Name
		instLimiter := ratelimit.NewFromRPMWithOptions(
			s.RateLimit.RPM, s.RateLimit.Burst,
			ratelimit.WithObserver("per_instance", func(scope string) {
				observability.IncRateLimitThrottled(instanceName, scope)
			}))
		// Dedicated MediaCover limiter. Hard-coded at 200 rpm / burst 60
		// — the frontend grid pulls ~60 posters at once; this lets a
		// page load drain instantly but caps sustained throughput so
		// runaway clients can't crater upstream Sonarr. Independent
		// from the global limiter so /system/status is never starved
		// by poster traffic.
		posterLimiter := ratelimit.NewFromRPMWithOptions(
			runtime.PosterLimitRPM, runtime.PosterLimitBurst,
			ratelimit.WithObserver("poster", func(scope string) {
				observability.IncRateLimitThrottled(instanceName, scope)
			}))
		return sonarr.NewWithOptions(s.Name, s.URL, s.APIKey, s.Timeout,
			instLimiter, log,
			sonarr.WithGlobalLimiterPointer(globalLimiterPtr),
			sonarr.WithPosterLimiter(posterLimiter),
			sonarr.WithSearchTimeout(s.SearchTimeout))
	}
}

// sweepIntervalSetter is the narrow contract buildOnAppliedFanout
// needs from the cooldown sweeper. Keeping it as an interface lets the
// fan-out be unit-tested without spinning up a real sweepLoop.
type sweepIntervalSetter interface {
	SetInterval(d time.Duration)
}

// regrabSwapper is the narrow contract buildOnAppliedFanout needs from
// the regrab loop. Decoupled into an interface so the fanout test can
// stub it without spinning up a real per-instance goroutine.
type regrabSwapper interface {
	SwapSettings(map[string]regrab.Settings)
}

// qbitSettingsLoader is the narrow contract buildOnAppliedFanout needs
// to project the qbit-settings repo into a map keyed by instance name.
// In production it's a closure over QbitSettingsRepository.List +
// instance lookup; in tests it's a fake that returns a fixed map.
type qbitSettingsLoader interface {
	Load(ctx context.Context) map[string]regrab.Settings
}

// buildOnAppliedFanout wires the OnApplied hook that updates everything
// that depends on the freshly-rebuilt sonarr-client set: the scan UC
// instance list, the holder map HTTP handlers iterate, the health
// checker's registry membership + preflight client list, and the
// cooldown sweep cadence. Running ALL of that inside
// SonarrClientsSubscriber.apply (under its lock) closes the
// cross-subscriber race that would otherwise let one fan-out observer
// (e.g. the old HealthRegistrySubscriber) read a stale View().All()
// before the live set was rebuilt.
func buildOnAppliedFanout(rootCtx context.Context, scanUC *scan.UseCase, holder *instanceMapHolder, checker reload.HealthChecker, wd *watchdog.Watchdog, sweeper sweepIntervalSetter, regrabLoop regrabSwapper, qbitLoader qbitSettingsLoader, log *slog.Logger) reload.OnAppliedFunc {
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
		holder.replace(nextMap)
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
		if regrabLoop != nil && qbitLoader != nil {
			qbitMap := qbitLoader.Load(rootCtx)
			regrabLoop.SwapSettings(qbitMap)
		}

		go checker.Preflight(rootCtx)
	}
}

// startSubscribers launches every subscriber under bgWG and blocks
// until each has registered on the bus, then returns. ctx is the
// long-lived rootCtx — SIGTERM cancels it and every subscriber
// drains. Returns the SchedulerSubscriber + SonarrClientsSubscriber
// so main.go can hand them off to shutdown and to other callers
// (rescanUC, healthcheck).
//
// If any subscriber fails to register within subscriberReadyTimeout
// the function returns an error and main is expected to exit
// non-zero. In normal operation this returns within microseconds.
func startSubscribers(
	ctx context.Context,
	bgWG *sync.WaitGroup,
	bus *runtime.Bus,
	log *slog.Logger,

	bootScheduler *scheduler.Scheduler,
	scanUC *scan.UseCase,
	bootClients map[string]ports.SonarrClient,
	clientFactory reload.SonarrClientFactory,
	checker reload.HealthChecker,
	wd *watchdog.Watchdog,
	holder *instanceMapHolder,
	sweeper sweepIntervalSetter,
	regrabLoop regrabSwapper,
	qbitLoader qbitSettingsLoader,
	globalLimiterPtr *atomic.Pointer[ratelimit.Limiter],
	bootGlobalRateLimit runtime.RateLimitSnapshot,
	authRuntimePtr *middleware.AuthRuntimePointer,
	engine *gin.Engine,
	runtimeRepo ports.RuntimeConfigRepository,
	clientSecretEnv string,
) (*reload.SchedulerSubscriber, *reload.SonarrClientsSubscriber, error) {
	subSched := reload.NewSchedulerSubscriber(ctx, bootScheduler, scanUC,
		reload.SchedulerFactory(scheduler.New), log)
	subClients := reload.NewSonarrClientsSubscriber(bootClients, clientFactory, log).
		WithWaitGroup(bgWG).
		WithOnApplied(buildOnAppliedFanout(ctx, scanUC, holder, checker, wd, sweeper, regrabLoop, qbitLoader, log))

	subRate := reload.NewGlobalRateLimiterSubscriber(globalLimiterPtr,
		reload.DefaultGlobalLimiterFactory, bootGlobalRateLimit, log)
	subAuth := reload.NewAuthMiddlewareSubscriber(authRuntimePtr, engine, log,
		runtimeRepo, clientSecretEnv)

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
		log.Error("startSubscribers: timeout waiting for subscribers to register",
			slog.Duration("timeout", timeout),
			slog.Any("stuck", stuck))
		return fmt.Errorf("startSubscribers: timeout waiting for subscribers to register: %v", stuck)
	}
}
