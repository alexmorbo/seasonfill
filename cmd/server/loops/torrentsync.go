package loops

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/alexmorbo/seasonfill/application/regrab"
	"github.com/alexmorbo/seasonfill/application/torrentsync"
	"github.com/alexmorbo/seasonfill/infrastructure/qbit"
	sharedports "github.com/alexmorbo/seasonfill/internal/shared/ports"
)

// DefaultTorrentsyncCadence is the wall-clock fallback when the
// operator has not customised poll_interval. PRD §4.4 calls for
// 30s. The instance_qbit_settings.poll_interval column is
// minutes-grained (it was modelled for regrab, see PRD §4.7);
// we reinterpret values <2 (minutes) as "use the default 30s"
// so an operator who has left the default has live cadence,
// while explicit values 2..N minutes lock the torrentsync loop
// to the same cadence as regrab (operator override).
//
// Rationale: PRD §4.7 explicitly asks for a future
// `sync_interval_seconds` (default 30) column but story 220
// punts on the schema change and re-uses the existing
// minutes-grained `poll_interval` for now. Story 221 (or a
// later polish story) will add the explicit column once
// operators have run the loop in production.
const DefaultTorrentsyncCadence = 30 * time.Second

// TorrentsyncRunner is the narrow Loop-construction surface the
// per-instance launcher needs. Production: torrentsync.NewLoop +
// torrentsync.UseCase.Hydrate + Loop.Run. Tests stub it.
type TorrentsyncRunner interface {
	Hydrate(ctx context.Context, instance string) error
	NewLoop(instance string, configured time.Duration) TorrentsyncRunningLoop
}

// TorrentsyncRunningLoop is the trimmed Loop surface — only the
// methods the launcher invokes. *torrentsync.Loop satisfies it
// implicitly.
type TorrentsyncRunningLoop interface {
	Run(ctx context.Context)
	SetInterval(d time.Duration)
}

// TorrentsyncLoop owns one polling goroutine per qBit-enabled
// Sonarr instance. Modelled 1:1 on RegrabLoop; the shape diverges
// only in the cadence translation (see torrentsyncCadence) and
// the Hydrate-before-Run step that loads `qbit_torrents` into the
// memory store on each fresh spawn.
type TorrentsyncLoop struct {
	runner TorrentsyncRunner
	bgWG   *sync.WaitGroup
	logger *slog.Logger

	mu     sync.Mutex
	parent context.Context
	loops  map[string]*torrentsyncInstance
}

type torrentsyncInstance struct {
	name    string
	loop    TorrentsyncRunningLoop
	cancel  context.CancelFunc
	cadence time.Duration
}

// NewTorrentsyncLoop wires the launcher. Constructor mirrors
// NewRegrabLoop intentionally.
func NewTorrentsyncLoop(runner TorrentsyncRunner, bgWG *sync.WaitGroup, log *slog.Logger) *TorrentsyncLoop {
	if log == nil {
		log = sharedports.DomainLogger(slog.Default(), "qbit")
	}
	return &TorrentsyncLoop{
		runner: runner,
		bgWG:   bgWG,
		logger: log,
		loops:  make(map[string]*torrentsyncInstance),
	}
}

// Start records the parent ctx. SwapSettings is the actual
// spawn entrypoint.
func (l *TorrentsyncLoop) Start(ctx context.Context) {
	l.mu.Lock()
	l.parent = ctx
	l.mu.Unlock()
}

// SwapSettings is the reload-bus entrypoint. Diff semantics
// identical to RegrabLoop.SwapSettings:
//   - name not in next, was in loops → cancel + remove
//   - name not in loops, was in next + enabled → spawn
//   - name in both, cadence changed → SetInterval (signals wake)
//
// Hydrate runs once per (instance, lifetime-of-this-pod) before
// Loop.Run so the read endpoint has the last-known snapshot
// available from t=0.
func (l *TorrentsyncLoop) SwapSettings(settings map[string]regrab.Settings) {
	l.mu.Lock()
	defer l.mu.Unlock()

	if l.parent == nil {
		return
	}

	for name, ll := range l.loops {
		s, ok := settings[name]
		if !ok || !s.Enabled {
			ll.cancel()
			delete(l.loops, name)
			l.logger.InfoContext(l.parent, "torrentsync_loop_stopped",
				slog.String("instance_name", name))
		}
	}

	for name, s := range settings {
		if !s.Enabled {
			continue
		}
		cadence := torrentsyncCadence(s.PollInterval)
		if existing, ok := l.loops[name]; ok {
			if existing.cadence != cadence {
				existing.cadence = cadence
				existing.loop.SetInterval(cadence)
			}
			continue
		}
		il := l.runner.NewLoop(name, cadence)
		ctx, cancel := context.WithCancel(l.parent)
		l.loops[name] = &torrentsyncInstance{
			name: name, loop: il, cancel: cancel, cadence: cadence,
		}
		if l.bgWG != nil {
			l.bgWG.Add(1)
		}
		go func(name string, il TorrentsyncRunningLoop, runCtx context.Context) {
			defer func() {
				if l.bgWG != nil {
					l.bgWG.Done()
				}
			}()
			if err := l.runner.Hydrate(runCtx, name); err != nil {
				l.logger.WarnContext(runCtx, "torrentsync_hydrate_failed",
					slog.String("instance_name", name),
					slog.String("error", err.Error()))
				// Even on hydrate failure we run — the store
				// will populate from the first successful
				// Refresh, just without the cold-start head start.
			}
			il.Run(runCtx)
		}(name, il, ctx)
		l.logger.InfoContext(l.parent, "torrentsync_loop_started",
			slog.String("instance_name", name),
			slog.Duration("cadence", cadence))
	}
}

// torrentsyncCadence translates the regrab-grained minutes
// poll_interval into the live-grained torrentsync cadence per
// the rule documented above.
func torrentsyncCadence(regrabPoll time.Duration) time.Duration {
	if regrabPoll < 2*time.Minute {
		return DefaultTorrentsyncCadence
	}
	return regrabPoll
}

// active is a diagnostic accessor — count of running per-instance
// loops at this moment. Test-only; mirrors RegrabLoop.active().
func (l *TorrentsyncLoop) active() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.loops)
}

// cadenceOf returns the current cadence for the named instance,
// or 0 if no loop is running for it. Test helper.
func (l *TorrentsyncLoop) cadenceOf(name string) time.Duration {
	l.mu.Lock()
	defer l.mu.Unlock()
	if ll, ok := l.loops[name]; ok {
		return ll.cadence
	}
	return 0
}

// productionTorrentsyncRunner wraps the application use case so
// SwapSettings can spawn loops without importing torrentsync's
// constructors inline.
type productionTorrentsyncRunner struct {
	uc     *torrentsync.UseCase
	logger *slog.Logger
}

// NewProductionTorrentsyncRunner is the public constructor for the
// production runner. server.go uses it; tests roll their own
// TorrentsyncRunner.
func NewProductionTorrentsyncRunner(uc *torrentsync.UseCase, log *slog.Logger) TorrentsyncRunner {
	return productionTorrentsyncRunner{uc: uc, logger: log}
}

func (r productionTorrentsyncRunner) Hydrate(ctx context.Context, instance string) error {
	return r.uc.Hydrate(ctx, instance)
}

func (r productionTorrentsyncRunner) NewLoop(instance string, configured time.Duration) TorrentsyncRunningLoop {
	return torrentsync.NewLoop(instance, r.uc, configured, r.logger)
}

// TorrentsyncSettingsLookup is the narrow Settings projection
// the session factory needs — implemented in production by
// *regrab.SettingsUseCase (Lookup decrypts the password and
// returns the resolved Settings).
type TorrentsyncSettingsLookup interface {
	Lookup(ctx context.Context, instanceName string) (regrab.Settings, error)
}

// TorrentsyncQbitClientFactory is the narrow client constructor
// surface the adapter calls. Implemented by
// infraregrab.QbitClientFactoryFunc.
type TorrentsyncQbitClientFactory interface {
	NewClient(s regrab.Settings) (qbit.Client, error)
}

// torrentsyncSessionFactoryAdapter bridges the production
// QbitClientFactory + Settings lookup into the application-layer
// torrentsync.SyncSessionFactory shape. NewSyncSession looks up
// the decrypted settings, builds a qbit.Client, opens the sync
// session, and hands it back. The application layer never sees
// the password or the qbit.Client.
type torrentsyncSessionFactoryAdapter struct {
	factory TorrentsyncQbitClientFactory
	lookup  TorrentsyncSettingsLookup
}

// NewTorrentsyncSessionFactoryAdapter wires the production
// session factory. Returned as torrentsync.SyncSessionFactory so
// callers (server.go) can hand it straight to torrentsync.NewUseCase
// without an interface cast.
func NewTorrentsyncSessionFactoryAdapter(factory TorrentsyncQbitClientFactory, lookup TorrentsyncSettingsLookup) torrentsync.SyncSessionFactory {
	return torrentsyncSessionFactoryAdapter{factory: factory, lookup: lookup}
}

// NewSyncSession implements torrentsync.SyncSessionFactory.
func (a torrentsyncSessionFactoryAdapter) NewSyncSession(ctx context.Context, instance string) (qbit.SyncSession, error) {
	sett, err := a.lookup.Lookup(ctx, instance)
	if err != nil {
		return nil, fmt.Errorf("torrentsync session lookup %s: %w", instance, err)
	}
	if !sett.Enabled {
		return nil, fmt.Errorf("torrentsync session %s: instance disabled", instance)
	}
	client, err := a.factory.NewClient(sett)
	if err != nil {
		return nil, fmt.Errorf("torrentsync session %s: build qbit client: %w", instance, err)
	}
	sess, err := client.NewSyncSession(ctx)
	if err != nil {
		_ = client.Close()
		return nil, fmt.Errorf("torrentsync session %s: open sync session: %w", instance, err)
	}
	return sess, nil
}
