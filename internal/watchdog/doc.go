// Package watchdog is the bounded context that owns the Phase 10
// Watchdog regrab loop: domain entities for the regrab blacklist
// (NoBetterRelease counter, blacklist row taxonomy) and the cross-
// context cooldown value object, the application UseCase that drives
// the per-season regrab attempt (with backoff, blacklist gating,
// dry_run guard, and the explicit-confirm bypass path), plus a
// separate Settings UseCase that owns the watchdog runtime config
// CRUD with the webhook-required gate (C-3 invariant).
//
// Layout (PRD §3.2 vertical slice, established in story 427 A-1-1 for
// mediaproxy, 428 A-1-2 for admin, 431 A-1-5 for grab, replicated
// here in story 433 A-1-7 for watchdog domain+app and extended in
// story 434 A-1-8 for persistence + rest):
//
//	internal/watchdog/
//	  domain/
//	    regrab/    — BlacklistRow + Counter entity, blacklist taxonomy.
//	    cooldown/  — cross-context cooldown value object (read by
//	                 grab + scan + webhook through allowList carve-
//	                 outs in their depcheck guards).
//	  app/
//	    regrab/    — UseCase (Execute one regrab decision, with backoff
//	                 retry + transactional cooldown set), SettingsUseCase
//	                 (CRUD + C-3 gate), runtime state, qbit client
//	                 factory port, settings types, plus mocks/ for the
//	                 generated gomock surface.
//	  infrastructure/
//	    (watchdog.go) — boot-time state watchdog that rechecks
//	                    Unavailable* Sonarr instances at per-state
//	                    cadences (D-2.3).
//	    regrab/    — cmd/server qbit-factory adapter satisfying the
//	                 app/regrab.QbitClientFactory boundary.
//	  persistence/
//	    cooldown_repository.go         — GORM repo for the cooldowns
//	                                     table; satisfies the
//	                                     CooldownRepository port.
//	    no_better_counter_repository.go — GORM repo for the
//	                                     regrab_no_better_counter
//	                                     table (NoBetterCounterModel
//	                                     is exported because the
//	                                     watchdog_seasons_repository
//	                                     in the catalog repo bundle
//	                                     still reads the same table
//	                                     for its aggregate view).
//	  rest/
//	    watchdog_blacklist.go + cursor — list / delete on the
//	                                     blacklist table.
//	    watchdog_rollups.go            — fan-in dashboard endpoint.
//	    watchdog_seasons.go            — read-side aggregate view.
//	    rescan.go                      — POST /decisions/{id}/rescan
//	                                     (part of the watchdog grab
//	                                     decision flow — bypasses GUID
//	                                     cooldowns).
//
// The directory name is `app/regrab` (PRD §3.2 layout); the Go
// package keeps its established short name `regrab` so consumers'
// import paths churn only on the directory prefix without an alias
// rename — mirrors the grab `app` carve-out from story 431.
//
// Import direction (PRD §3.3 — enforced by the depcheck tests):
//
//	rest                 → app/regrab, domain/regrab, domain/cooldown,
//	                       interface/http/{dto,handlers,middleware}
//	                       (handlers for the shared WriteError /
//	                       WriteInternalError / ParseLimit /
//	                       HandleQueryErr helpers + InstanceLister
//	                       interface), application/{ports,rescan,scan}
//	                       (rescan handler), infrastructure/{qbit,
//	                       database/repositories} (read-side row
//	                       types).
//	persistence          → application/ports, infrastructure/database,
//	                       internal/shared/dbtx, domain/cooldown,
//	                       domain/regrab.
//	infrastructure       → domain/instance, internal/config.
//	infrastructure/regrab → infrastructure/qbit, app/regrab.
//	app/regrab           → domain/regrab, domain/cooldown,
//	                       internal/grab/{app,domain,...} (cross-
//	                       context: regrab Decision execution),
//	                       internal/shared/*
//	domain/regrab        → (std lib + internal/shared/domain only)
//	domain/cooldown      → (std lib + internal/shared/domain only)
//
// Cross-context boundary (kernel allow-list, enforced by
// tests/lint_watchdog_imports_test.go and mirrored in
// tests/lint_grab_imports_test.go):
//
//   - watchdog/app/regrab → grab/{app,domain,domain/decision} for the
//     regrab → grab handoff (one regrab attempt = one grab Decision
//     execution).
//   - grab/{app,domain,rest} ← watchdog/domain/cooldown for the
//     explicit-confirm path (look up active cooldowns before grab).
//   - scan + webhook ← watchdog/domain/cooldown (same shape — read-
//     only value-object consumer; allowList carve-out continues to
//     mark cooldown as a cross-context type even after its move
//     under watchdog).
//   - infrastructure/database/repositories/watchdog_seasons_repository
//     → watchdog/persistence.NoBetterCounterModel for the aggregate
//     read-side join (catalog-side seasons repo stays in the legacy
//     tree until a later story; the model type is exported so the
//     model travels with its primary owner without forcing the
//     seasons-repo move now).
//
// Story origin:
//   - 039+    — Phase 10 Watchdog parent (D-T1..D-T7 plan)
//   - 109+    — regrab UseCase + settings CRUD
//   - 120+    — manual regrab + per-season confirm
//   - 121+    — blacklist + NoBetterRelease counter
//   - 427     — vertical-slice extraction protocol (mediaproxy)
//   - 428     — admin extraction
//   - 431     — grab extraction
//   - 433     — this layout (domain + app in one shot)
//   - 434     — watchdog persistence + rest (cooldown / no_better_counter
//     repos, watchdog_* handlers, rescan handler)
package watchdog
