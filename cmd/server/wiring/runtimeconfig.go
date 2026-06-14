package wiring

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/alexmorbo/seasonfill/application/ports"
	"github.com/alexmorbo/seasonfill/application/runtimeconfig"
	"github.com/alexmorbo/seasonfill/interface/http/handlers"
	"github.com/alexmorbo/seasonfill/internal/config"
	"github.com/alexmorbo/seasonfill/internal/runtime"
)

// HTTPServeConfig is the on-the-stack config DTO previously inlined as
// `httpServeConfig` in package main (story 323). It carries the subset
// of bootstrap + runtime fields the HTTP server, scheduler, and
// shutdown ladder read. Returned by BuildRuntimeConfig inside
// RuntimeConfigBundle.ServeConfig.
//
// All fields are read-only after construction. Server.cfg keeps a copy
// by value so Shutdown can read cfg.Scan.ShutdownGrace from the same
// value Run was constructed with.
type HTTPServeConfig struct {
	HTTP            config.HTTPConfig
	SonarrInstances []runtime.InstanceSnapshot
	DryRun          bool
	GlobalRateLimit runtime.RateLimitSnapshot
	Scan            runtime.ScanSnapshot
	Cron            runtime.CronSnapshot
}

// RuntimeConfigBundle is the output of BuildRuntimeConfig. It groups
// the boot-time snapshot, the application use case, the HTTP handler,
// and the assembled HTTPServeConfig — together the entire "runtime
// configuration" bounded context.
//
// Snap is the full runtime.Snapshot value loaded from the singleton
// row plus the instance list (sorted, defaults applied). Consumers in
// server.go read snap.GlobalRateLimit (for the initial limiter seed),
// snap.Auth (for downstream auth wiring), and pass snap to the reload
// subscribers' boot publish.
//
// UC is the runtimeconfig application use case, already configured
// with WithClientSecretEnv(bootCfg.Auth.OIDCClientSecret).
//
// Handler is the HTTP handler that delegates to UC.
//
// ServeConfig is the assembled HTTPServeConfig the HTTP server, the
// scheduler factory, and Shutdown all read.
type RuntimeConfigBundle struct {
	Snap        runtime.Snapshot
	UC          *runtimeconfig.UseCase
	Handler     *handlers.RuntimeConfigHandler
	ServeConfig HTTPServeConfig
}

// BuildRuntimeConfig seeds the runtime_config row on a fresh install,
// composes the boot snapshot from the row + instance list, and wires
// the runtimeconfig application use case + HTTP handler + the on-stack
// HTTPServeConfig DTO.
//
// The ctx parameter is reserved for future use. The current body uses
// a background context for the DB reads to mirror the pre-refactor
// behaviour in Server.New (the seed must complete even if the parent
// ctx already carries an outer-test-harness deadline). See the same
// note on BuildPersistence.
//
// Seed-on-empty: if runtimes.Get returns ports.ErrNotFound, the wirer
// upserts runtime.Defaults() and re-reads. Any other error from Get
// (or the upsert + reload pair) is wrapped and returned.
func BuildRuntimeConfig(
	ctx context.Context,
	persistence *PersistenceBundle,
	bootCfg *config.Bootstrap,
	bus *runtime.Bus,
	log *slog.Logger,
) (*RuntimeConfigBundle, error) {
	_ = ctx
	bgCtx := context.Background()

	// Seed runtime_config from Defaults() on a truly-fresh install.
	row, err := persistence.RuntimeRepo.Get(bgCtx)
	switch {
	case err == nil:
		// happy path
	case errors.Is(err, ports.ErrNotFound):
		if err := persistence.RuntimeRepo.Upsert(bgCtx, runtime.Defaults(), nil); err != nil {
			return nil, fmt.Errorf("seed runtime_config: %w", err)
		}
		row, err = persistence.RuntimeRepo.Get(bgCtx)
		if err != nil {
			return nil, fmt.Errorf("reload runtime_config after seed: %w", err)
		}
	default:
		return nil, fmt.Errorf("read runtime_config: %w", err)
	}

	instances, err := persistence.InstanceRepo.List(bgCtx, persistence.Cipher)
	if err != nil {
		return nil, fmt.Errorf("list instances: %w", err)
	}
	for i := range instances {
		runtime.ApplyInstanceDefaults(&instances[i])
	}
	runtime.SortInstances(instances)

	snap := runtime.Snapshot{
		Cron: row.Cron, Scan: row.Scan, DryRun: row.DryRun,
		GlobalRateLimit: row.GlobalRateLimit, Auth: row.Auth,
		Instances: instances,
	}

	uc := runtimeconfig.New(persistence.RuntimeRepo, persistence.InstanceRepo,
		persistence.Cipher, bus, log).
		WithClientSecretEnv(bootCfg.Auth.OIDCClientSecret)
	handler := handlers.NewRuntimeConfigHandler(uc, log)

	// cfg reads from snap (not bootstrap) for the runtime-mutable
	// fields. APIKey embedded into authCfg comes from MasterKey
	// derived in BuildPersistence — the HTTP auth layer compares
	// against the X-Api-Key header.
	authCfg := config.Auth{
		Enabled:          true,
		APIKey:           persistence.MasterKey,
		SessionTTL:       snap.Auth.SessionTTL,
		SecureCookie:     snap.Auth.SecureCookie,
		TrustedProxies:   snap.Auth.TrustedProxies,
		OIDCClientSecret: bootCfg.Auth.OIDCClientSecret,
	}
	httpCfg := bootCfg.HTTP
	httpCfg.Auth = authCfg
	serveCfg := HTTPServeConfig{
		HTTP:            httpCfg,
		SonarrInstances: instances,
		DryRun:          snap.DryRun,
		GlobalRateLimit: snap.GlobalRateLimit,
		Scan:            snap.Scan,
		Cron:            snap.Cron,
	}

	return &RuntimeConfigBundle{
		Snap:        snap,
		UC:          uc,
		Handler:     handler,
		ServeConfig: serveCfg,
	}, nil
}
