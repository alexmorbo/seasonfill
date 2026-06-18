package wiring

import (
	"log/slog"
	"sync"

	"github.com/alexmorbo/seasonfill/application/torrentsync"
	"github.com/alexmorbo/seasonfill/cmd/server/loops"
	"github.com/alexmorbo/seasonfill/infrastructure/database/repositories"
	infraregrab "github.com/alexmorbo/seasonfill/infrastructure/regrab"
	"github.com/alexmorbo/seasonfill/infrastructure/sonarr"
	handlers "github.com/alexmorbo/seasonfill/interface/http/handlers"
	"github.com/alexmorbo/seasonfill/internal/observability"
	"github.com/alexmorbo/seasonfill/internal/shared/ports"
)

// TorrentsyncBundle groups the qBit torrent-sync components constructed at
// boot. Returned by BuildTorrentsync. Threaded into:
//
//   - httpserver.NewServer (SeriesTorrentsHandler) — the HTTP wirer
//     remains in server.go for now.
//   - startSubscribers (Loop pointer satisfies the torrentsyncSwapper
//     contract via SwapSettings).
//   - server.go calls Loop.Start(rootCtx) directly because the loop
//     owner needs the cancellation-bearing rootCtx, which the wirer
//     does not (and should not) own.
//
// Field-level invariants:
//
//   - Store is the in-memory store consumed by both the UC and the Query.
//
//   - Policy is the persist policy fed to the UC; it captures the
//     qbit_torrents + qbit_torrent_events repos.
//
//   - Factory is the production session factory adapter — returned as
//     the torrentsync.SyncSessionFactory interface so it threads
//     directly into NewUseCase. Closes over regrabBundle.QbitSettingsUC
//     for password-decrypting Lookup.
//
//   - Reconciler is built with the same TorrentSeriesMapRepo as the
//     webhook UC (story 335 invariant: one repo pointer, two consumers).
//     sonarrFor closure reads through sonarrBundle.Holder.Load, so it
//     observes the live instance map after every reload publish.
//
//   - UC is the orchestrator. WithReconciler is applied here so callers
//     see a fully-configured handle.
//
//   - Loop is constructed here but NOT started — server.go owns rootCtx
//     and calls .Start(rootCtx) after BuildTorrentsync returns.
//
//   - Query is the read-side companion to the UC. It re-uses the same
//     Store pointer + qbit_torrents repo + TorrentSeriesMapRepo.
//
//   - SeriesTorrentsHandler wraps Query for the per-series torrents
//     endpoint (story 222 / A-4). Holds local seriesRepo +
//     seriesCacheRepo handles (stateless GORM wrappers, same pattern as
//     webhook.go + regrab.go).
type TorrentsyncBundle struct {
	Store                 *torrentsync.Store
	Policy                *torrentsync.PersistPolicy
	Factory               torrentsync.SyncSessionFactory
	Reconciler            *torrentsync.Reconciler
	UC                    *torrentsync.UseCase
	Loop                  *loops.TorrentsyncLoop
	Query                 *torrentsync.Query
	SeriesTorrentsHandler *handlers.SeriesTorrentsHandler
}

// BuildTorrentsync wires the torrentsync stack (220 A-2 + 221 A-3 +
// 222 A-4 in the pre-338 inline body).
//
// Construction order mirrors the pre-338 inline body verbatim:
//
//  1. qbit_torrents + qbit_torrent_events repos.
//  2. Store + PersistPolicy.
//  3. SessionFactory adapter (closes over regrabBundle.QbitSettingsUC).
//  4. sonarrFor closure over sonarrBundle.Holder.Load — reload-aware
//     by construction.
//  5. Reconciler (uses webhookBundle.TorrentSeriesMapRepo and
//     scanBundle.GrabRepo).
//  6. UseCase (with WithReconciler).
//  7. Loop (NewTorrentsyncLoop + NewProductionTorrentsyncRunner) —
//     NOT started here; server.go owns rootCtx.
//  8. Query (re-uses TorrentSeriesMapRepo as LookupRepo).
//  9. SeriesTorrentsHandler — seriesRepo + seriesCacheRepo are local
//     (stateless GORM wrappers).
//
// bgWG is the process-wide background wait group — forwarded to
// loops.NewTorrentsyncLoop so the per-instance polling goroutines
// block graceful shutdown's drainBackground.
//
// No error path — every step is in-memory construction. The signature
// returns error for symmetry with the other Build* wirers.
func BuildTorrentsync(
	persistence *PersistenceBundle,
	sonarrBundle *SonarrBundle,
	scanBundle *ScanBundle,
	webhookBundle *WebhookBundle,
	regrabBundle *RegrabBundle,
	bgWG *sync.WaitGroup,
	log *slog.Logger,
) (*TorrentsyncBundle, error) {
	db := persistence.DB
	holder := sonarrBundle.Holder

	// F-4b-2: qbitLog carries domain="qbit" per §6.5. Applied at the wirer
	// once and passed to every component the torrentsync context owns
	// (PersistPolicy, Reconciler, UseCase, ProductionTorrentsyncRunner,
	// TorrentsyncLoop). The SeriesTorrentsHandler stays on bare `log`
	// because HTTP handlers belong to the future F-4b-N handlers slice
	// and will use LoggerFromContext(ctx) (request scope already carries
	// domain="http"), not DomainLogger.
	qbitLog := ports.DomainLogger(log, "qbit")

	// 220 (A-2) — qbit_torrents + qbit_torrent_events repos.
	qbitTorrentsRepo := repositories.NewQbitTorrentsRepository(db)
	qbitTorrentEventsRepo := repositories.NewQbitTorrentEventsRepository(db)

	// Store + PersistPolicy.
	store := torrentsync.NewStore()
	policy := torrentsync.NewPersistPolicy(qbitTorrentsRepo, qbitTorrentEventsRepo, qbitLog)

	// Session factory adapter — closes over regrabBundle.QbitSettingsUC
	// for password-decrypting Lookup. Returned as the
	// torrentsync.SyncSessionFactory interface so it threads into
	// NewUseCase without a cast.
	factory := loops.NewTorrentsyncSessionFactoryAdapter(
		infraregrab.QbitClientFactoryFunc{},
		regrabBundle.QbitSettingsUC,
	)

	// 221 (A-3) — sonarrFor closure wires the per-instance Sonarr
	// client lookup the reconciler needs for sources 3 + 4. Production
	// wiring reuses the instance holder; the concrete *sonarr.Client
	// satisfies torrentsync.SonarrReconciler (its QueueAll +
	// GrabHistoryPaged are exactly the two methods in the port).
	sonarrFor := func(instance string) (torrentsync.SonarrReconciler, bool) {
		h := holder.Load()
		inst, ok := h[instance]
		if !ok || inst.Client == nil {
			return nil, false
		}
		client, ok := inst.Client.(*sonarr.Client)
		if !ok {
			return nil, false
		}
		return client, true
	}
	reconciler := torrentsync.NewReconciler(
		store,
		webhookBundle.TorrentSeriesMapRepo,
		scanBundle.GrabRepo,
		sonarrFor,
		observability.TorrentsyncMetricsAdapter{},
		qbitLog,
	)

	useCase := torrentsync.NewUseCase(
		store, policy,
		factory, qbitTorrentsRepo, qbitLog,
	).WithReconciler(reconciler)

	// Loop owns per-instance polling goroutines; SwapSettings is
	// called from the OnApplied fanout. NOT started here — server.go
	// owns rootCtx and calls .Start(rootCtx) inline after
	// BuildTorrentsync returns.
	loop := loops.NewTorrentsyncLoop(
		loops.NewProductionTorrentsyncRunner(useCase, qbitLog),
		bgWG, qbitLog,
	)

	// 222 (A-4) — per-series torrents endpoint. Reuses the store +
	// qbit_torrents repo wired above. TorrentSeriesMapRepo is shared
	// with the reconciler (story 335 invariant).
	query := torrentsync.NewQuery(store, qbitTorrentsRepo, webhookBundle.TorrentSeriesMapRepo)

	// seriesRepo + seriesCacheRepo are local (stateless GORM wrappers,
	// same pattern as webhook.go / regrab.go — re-constructing them
	// here is free and mirrors the pre-338 inline body which captured
	// the seriesdetail-block instances).
	seriesRepo := repositories.NewSeriesRepository(db)
	seriesCacheRepo := repositories.NewSeriesCacheRepository(db, seriesRepo)
	// HTTP handler stays on bare `log` — see qbitLog godoc above.
	seriesTorrentsHandler := handlers.NewSeriesTorrentsHandler(
		query, seriesCacheRepo, seriesRepo, log,
	)

	return &TorrentsyncBundle{
		Store:                 store,
		Policy:                policy,
		Factory:               factory,
		Reconciler:            reconciler,
		UC:                    useCase,
		Loop:                  loop,
		Query:                 query,
		SeriesTorrentsHandler: seriesTorrentsHandler,
	}, nil
}
