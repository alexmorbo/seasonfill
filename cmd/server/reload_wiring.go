package main

import (
	"context"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/alexmorbo/seasonfill/application/ports"
	"github.com/alexmorbo/seasonfill/application/scan"
	"github.com/alexmorbo/seasonfill/infrastructure/ratelimit"
	"github.com/alexmorbo/seasonfill/infrastructure/reload"
	"github.com/alexmorbo/seasonfill/infrastructure/scheduler"
	"github.com/alexmorbo/seasonfill/infrastructure/sonarr"
	"github.com/alexmorbo/seasonfill/interface/http/middleware"
	"github.com/alexmorbo/seasonfill/internal/observability"
	"github.com/alexmorbo/seasonfill/internal/runtime"
)

// instanceMapHolder is the shared, mutex-protected container the
// scanInstances subscriber writes into and rescanUC reads from. A
// plain map would race; using sync.Map loses the by-name shape the
// caller needs.
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

// buildSonarrClientFactory captures the global-limiter atomic +
// log into a closure used by both boot construction and reload-time
// rebuilds.
func buildSonarrClientFactory(globalLimiterPtr *atomic.Pointer[ratelimit.Limiter], log *slog.Logger) reload.SonarrClientFactory {
	return func(s runtime.InstanceSnapshot) ports.SonarrClient {
		instanceName := s.Name
		instLimiter := ratelimit.NewFromRPMWithOptions(
			s.RateLimit.RPM, s.RateLimit.Burst,
			ratelimit.WithObserver("per_instance", func(scope string) {
				observability.IncRateLimitThrottled(instanceName, scope)
			}))
		return sonarr.NewWithOptions(s.Name, s.URL, s.APIKey, s.Timeout,
			instLimiter, log,
			sonarr.WithGlobalLimiterPointer(globalLimiterPtr),
			sonarr.WithSearchTimeout(s.SearchTimeout))
	}
}

// startSubscribers launches all six subscribers under bgWG. ctx is
// the long-lived rootCtx — SIGTERM cancels it and every subscriber
// drains. Returns the SchedulerSubscriber + SonarrClientsSubscriber
// so main.go can hand them off to shutdown and to other callers
// (rescanUC, healthcheck).
func startSubscribers(
	ctx context.Context,
	bgWG *sync.WaitGroup,
	bus *runtime.Bus,
	log *slog.Logger,

	bootScheduler *scheduler.Scheduler,
	scanUC *scan.UseCase,
	bootClients map[string]ports.SonarrClient,
	bootCfgs map[string]runtime.InstanceSnapshot,
	clientFactory reload.SonarrClientFactory,
	checker reload.HealthChecker,
	holder *instanceMapHolder,
	globalLimiterPtr *atomic.Pointer[ratelimit.Limiter],
	authRuntimePtr *middleware.AuthRuntimePointer,
	engine *gin.Engine,
) (*reload.SchedulerSubscriber, *reload.SonarrClientsSubscriber) {
	subSched := reload.NewSchedulerSubscriber(ctx, bootScheduler, scanUC,
		reload.SchedulerFactory(scheduler.New), log)
	subClients := reload.NewSonarrClientsSubscriber(bootClients, bootCfgs, clientFactory, log)

	clientLister := func() []ports.SonarrClient { return subClients.View().All() }
	clientForName := func(n string) (ports.SonarrClient, bool) { return subClients.View().ByName(n) }

	subHealth := reload.NewHealthRegistrySubscriber(checker, clientLister, log)
	subScan := reload.NewScanInstancesSubscriber(scanUC, clientForName, holder.replace, log)
	subRate := reload.NewGlobalRateLimiterSubscriber(globalLimiterPtr,
		reload.DefaultGlobalLimiterFactory, log)
	subAuth := reload.NewAuthMiddlewareSubscriber(authRuntimePtr, engine, log)

	for _, run := range []func(context.Context, *runtime.Bus){
		subSched.Run, subClients.Run, subHealth.Run,
		subScan.Run, subRate.Run, subAuth.Run,
	} {
		bgWG.Add(1)
		runFn := run
		go func() {
			defer bgWG.Done()
			runFn(ctx, bus)
		}()
	}

	// Allow all six goroutines to register their bus.Subscribe call
	// before the caller issues the boot publish. This mirrors the
	// pattern used in subscriber unit tests (see e.g.
	// scheduler_subscriber_test.go:startSub).
	time.Sleep(10 * time.Millisecond)

	return subSched, subClients
}
