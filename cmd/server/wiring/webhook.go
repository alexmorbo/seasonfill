package wiring

import (
	"log/slog"
	"time"

	"github.com/alexmorbo/seasonfill/application/ports"
	"github.com/alexmorbo/seasonfill/application/scan"
	webhookuc "github.com/alexmorbo/seasonfill/application/webhook"
	"github.com/alexmorbo/seasonfill/application/webhookinstall"
	"github.com/alexmorbo/seasonfill/cmd/server/adapters"
	"github.com/alexmorbo/seasonfill/infrastructure/database/repositories"
	"github.com/alexmorbo/seasonfill/infrastructure/sonarr"
	"github.com/alexmorbo/seasonfill/internal/runtime"
)

// WebhookBundle groups the webhook-domain components constructed at boot.
// Returned by BuildWebhook. Threaded into:
//
//   - httpserver.NewServer (WebhookUC, Reconciler, StatusCache) — the
//     HTTP wirer remains in server.go for now.
//   - instance.UseCase chained setters (WithWebhookReconciler,
//     WithWebhookStatusCache) — server.go composes
//     `adapters.ReconcilerAdapter{Inner: webhookReconciler}` directly
//     via the bundle's ReconcilerAdapter field (pre-baked).
//   - loops.NewWebhookReconcileLoop (Reconciler, StatusCache) — the
//     background reconcile safety net (041d) is spawned by server.go
//     on the lifecycle group, same as cooldown-sweeper.
//   - torrentsync.NewReconciler / torrentsync.NewQuery (TorrentSeriesMapRepo)
//     — story 221 (A-3) wiring consumes the same repo.
//
// Field-level invariants:
//
//   - WebhookUC owns four reload-aware closures over sonarrBundle.Holder:
//     GUIDCooldownLookup, SonarrClientFor, InstanceFor, and (via Syncer)
//     Lookup. The holder is pointer-stable across reload (sonarr.go
//     §SonarrBundle), so every Load() call observes the most recent
//     snapshot fanout-published by SonarrClientsSubscriber.
//
//   - Syncer is the E-1 (Story 210) SeriesAdd path. Re-uses the same
//     holder.Load closure as the UC so a webhook event lands on the
//     freshly-reloaded instance map. It is wired into the UC via
//     Deps.SeriesSyncer; the Bundle exposes it for symmetry with
//     ScanBundle (downstream tests + future stories may want a handle).
//
//   - Reconciler reads through adapters.NewWebhookReconcileLookup over
//     sonarrBundle.InstanceReg. InstanceReg is built once in BuildSonarr
//     with `Load: holder.Load`, so the lookup is reload-aware by the
//     same mechanism.
//
//   - StatusCache is shared by Reconciler, the background reconcile loop
//     (loops.NewWebhookReconcileLoop), and the instance UC cleanup hook
//     (WithWebhookStatusCache). The Bundle's StatusCache pointer is the
//     SAME pointer every consumer holds.
//
//   - ReconcilerAdapter is pre-baked from the same Reconciler pointer.
//     instance.UseCase consumes it via WithWebhookReconciler (server.go
//     constructs instance.UseCase AFTER this bundle is built — not a
//     true late-bind, just an in-order chained setter).
//
//   - TorrentSeriesMapRepo + EpisodeStatesRepo are pre-Story-221/218
//     repos consumed both inside the UC (UpsertTx in the same tx as
//     UpdateTorrentHash; SeriesDelete cascade) and outside it
//     (torrentsync reconciler, torrentsync query). Exposed on the
//     bundle so server.go can pass the same pointer to both call sites.
type WebhookBundle struct {
	WebhookUC            *webhookuc.UseCase
	Syncer               *scan.Syncer
	Reconciler           *webhookinstall.Reconciler
	StatusCache          *webhookinstall.StatusCache
	ReconcilerAdapter    adapters.ReconcilerAdapter
	TorrentSeriesMapRepo *repositories.TorrentSeriesMapRepository
	EpisodeStatesRepo    *repositories.EpisodeStatesRepository
}

// BuildWebhook wires the webhook UC + Syncer + Reconciler + StatusCache
// stack.
//
// Construction order mirrors the pre-335 inline body verbatim:
//
//  1. EpisodeStatesRepo (Story 218 E-2 cascade) + TorrentSeriesMapRepo
//     (Story 221 A-3 bridge).
//  2. The 5 scan.Syncer-internal repos (episodes, episode_texts, genres,
//     genres_i18n, networks). These are stateless GORM wrappers — re-
//     constructing them here is free and they are not re-exposed on the
//     bundle (downstream consumers — seriesdetail, enrichment — build
//     their own instances from PersistenceBundle.DB).
//  3. scan.Syncer (Story 300 E-1 wiring fix) — captures
//     sonarrBundle.Holder.Load for reload-aware lookup.
//  4. webhook.UseCase with the 4 reload-aware closures.
//  5. webhookinstall.StatusCache.
//  6. webhookinstall.Reconciler — reads through
//     adapters.NewWebhookReconcileLookup(sonarrBundle.InstanceReg).
//  7. adapters.ReconcilerAdapter pre-baked over the Reconciler pointer.
//
// seriesRepo + seriesCacheRepo are NOT inputs because the wirer
// constructs them locally — same pattern as ScanBundle. They are
// stateless GORM wrappers; building a second instance here matches the
// pre-335 inline body (server.go line 253-254 already built one for
// the seriesdetail block; the webhook block built its own
// indirectly through the Syncer Deps).
//
// cfg is the HTTPServeConfig from BuildRuntimeConfig — the wirer only
// reads cfg.HTTP.Auth.APIKey to seed the reconciler's API-key header.
//
// scanBundle is reserved — currently unused (the webhook UC depends on
// GrabRepo, CooldownRepo from scanBundle via the same Bundle that
// holds them, but the inline body constructs them through scanBundle
// fields). Passed in for symmetry + future-proofing (if the wirer
// later needs Evaluator / Txr from the scan stack).
//
// No error path — every step is in-memory construction. The signature
// returns error for symmetry with the other Build* wirers (room for
// future seed-or-validate logic).
func BuildWebhook(
	persistence *PersistenceBundle,
	sonarrBundle *SonarrBundle,
	scanBundle *ScanBundle,
	cfg HTTPServeConfig,
	log *slog.Logger,
) (*WebhookBundle, error) {
	_ = scanBundle // reserved — see godoc
	db := persistence.DB
	holder := sonarrBundle.Holder

	// Story 218 (E-2) — webhook SeriesDelete cascade soft-deletes
	// episode_states under the deleted series. Repo is constructed
	// here so the cascade port is wired at boot.
	webhookEpisodeStatesRepo := repositories.NewEpisodeStatesRepository(db)
	// 221 (A-3) — torrent_series_map repo wired here so the webhook
	// path can write the bridge row in the same tx as the
	// grab_records.torrent_hash update. Repo also feeds the
	// torrentsync reconciler constructed later in server.go.
	torrentSeriesMapRepo := repositories.NewTorrentSeriesMapRepository(db)

	// Story 300 (E-1 wiring fix) — construct scan.Syncer so the
	// webhook SeriesAdd path populates the canonical entity model
	// (series + episodes + episode_states + series_genres +
	// series_networks) instead of falling back to the thin
	// CacheEntry write. Repos are stateless GORM wrappers (same
	// shape as the Story 215 seriesdetail block), so re-
	// constructing them here is free. Lookup returns the concrete
	// *sonarr.Client because Syncer.SyncFromSonarrAPI needs the
	// payload-fetcher methods (GetSeriesPayload / ListEpisodesForSync
	// / ListEpisodeFilesForSync) that live on the concrete type,
	// not on ports.SonarrClient. Unknown instance OR a non-concrete
	// client → (nil, false), webhook silently falls back to the
	// pre-E-1 thin CacheEntry path.
	seriesRepo := repositories.NewSeriesRepository(db)
	seriesCacheRepo := repositories.NewSeriesCacheRepository(db, seriesRepo)
	webhookEpisodesRepo := repositories.NewEpisodesRepository(db)
	webhookEpisodeTextsRepo := repositories.NewEpisodeTextsRepository(db)
	webhookGenresRepo := repositories.NewGenresRepository(db)
	webhookGenresI18nRepo := repositories.NewGenresI18nRepository(db)
	webhookNetworksRepo := repositories.NewNetworksRepository(db)
	webhookSeriesSyncer := &scan.Syncer{
		Deps: scan.SyncDeps{
			Series:        seriesRepo,
			SeriesCache:   seriesCacheRepo,
			Episodes:      webhookEpisodesRepo,
			EpisodeStates: webhookEpisodeStatesRepo,
			EpisodeTexts:  webhookEpisodeTextsRepo,
			Genres:        scan.NewGenresAdapter(webhookGenresRepo, webhookGenresI18nRepo),
			Networks:      scan.NewNetworksAdapter(webhookNetworksRepo),
			Logger:        log,
		},
		Lookup: func(name string) (*sonarr.Client, bool) {
			h := holder.Load()
			if h == nil {
				return nil, false
			}
			inst, ok := h[name]
			if !ok || inst.Client == nil {
				return nil, false
			}
			concrete, ok := inst.Client.(*sonarr.Client)
			if !ok {
				return nil, false
			}
			return concrete, true
		},
		Logger: log,
	}

	// scan stack repos come from scanBundle (GrabRepo, CooldownRepo);
	// SeriesCacheRepo is the local one (matches pre-335: the inline
	// body re-used the same seriesCacheRepo built at server.go:254).
	webhookUC := webhookuc.New(webhookuc.Deps{
		Grabs:            scanBundle.GrabRepo,
		Cooldowns:        scanBundle.CooldownRepo,
		SeriesCache:      seriesCacheRepo,
		Tx:               scanBundle.Txr,
		EpisodeStates:    webhookEpisodeStatesRepo,
		TorrentSeriesMap: torrentSeriesMapRepo,
		SeriesSyncer:     webhookSeriesSyncer,
		GUIDCooldownLookup: func(name string) time.Duration {
			inst, ok := holder.Load()[name]
			if !ok {
				return 0
			}
			return inst.Config.Cooldown.GUIDAfterFailedImport
		},
		Logger: log,
		SonarrClientFor: func(name string) (ports.SonarrClient, bool) {
			if h := holder.Load(); h != nil {
				if inst, ok := h[name]; ok && inst.Client != nil {
					return inst.Client, true
				}
			}
			return nil, false
		},
		InstanceFor: func(name string) (runtime.InstanceSnapshot, bool) {
			if h := holder.Load(); h != nil {
				if inst, ok := h[name]; ok {
					return inst.Config, true
				}
			}
			return runtime.InstanceSnapshot{}, false
		},
	})

	webhookStatusCache := webhookinstall.NewStatusCache()
	webhookReconciler := webhookinstall.New(webhookinstall.Deps{
		Lookup:    adapters.NewWebhookReconcileLookup(sonarrBundle.InstanceReg),
		PublicURL: webhookinstall.PublicURLFromContext,
		Cache:     webhookStatusCache,
		APIKey:    cfg.HTTP.Auth.APIKey,
		Logger:    log,
	})

	return &WebhookBundle{
		WebhookUC:            webhookUC,
		Syncer:               webhookSeriesSyncer,
		Reconciler:           webhookReconciler,
		StatusCache:          webhookStatusCache,
		ReconcilerAdapter:    adapters.ReconcilerAdapter{Inner: webhookReconciler},
		TorrentSeriesMapRepo: torrentSeriesMapRepo,
		EpisodeStatesRepo:    webhookEpisodeStatesRepo,
	}, nil
}
