// Package catalog is the bounded context that owns the seasonfill
// catalog domain: Sonarr instances, the canonical series cache, the
// per-season episode/file rollups, the indexer release record, and
// the inbound Sonarr webhook event vocabulary. Everything the rest of
// the system reads about "which series we monitor and what we know
// about them" originates here.
//
// Layout (PRD §3.2 vertical slice, established in story 441 A-1-15):
//
//	internal/catalog/
//	  domain/
//	    series/    — Canon, Season, SeasonStat, Episode, Hydration,
//	                 CacheEntry value types + per-series invariants
//	    instance/  — Instance config (Sonarr URL/key/mode), HealthRegistry
//	    webhook/   — Sonarr inbound webhook Event vocabulary
//	    release/   — Indexer release record + ranking value type
//	  app/
//	    instance/  — InstanceUseCase (CRUD + Sonarr settings refresh)
//
// Import direction (PRD §3.3 — enforced by the depcheck test):
//
//	app/instance     → domain/{series,instance,release,webhook},
//	                   application/ports, infrastructure/database
//	                   (model carve-outs while ports migration ongoing)
//	domain/series    → (std lib + internal/shared/domain only)
//	domain/instance  → (std lib + internal/shared/domain only)
//	domain/webhook   → (std lib only)
//	domain/release   → (std lib only)
//
// Cross-context boundary: enrichment, grab, watchdog, scan,
// seriesdetail, and the HTTP handler layer all consume catalog
// domain types (series.Canon, instance.Instance, release.Release,
// webhook.Event) by value through application/ports contracts. They
// MUST NOT reach into internal/catalog/app/* for behavior — the
// InstanceUseCase is exposed via the application/ports.Instance
// contract and wired by cmd/server/wiring.
//
// Story origin:
//   - 441 — vertical-slice extraction (this layout): domain/series,
//     domain/instance, domain/webhook, domain/release, and
//     application/instance moved under internal/catalog/.
//
// Downstream catalog stories (442-444) build on these moves:
// application/scan moves into internal/catalog/app/scan,
// application/seriesdetail into internal/catalog/app/seriesdetail,
// and the per-series episode-state persistence into
// internal/catalog/persistence.
package catalog
