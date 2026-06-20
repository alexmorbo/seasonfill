// Package wiring contains constructor functions ("wirers") that
// instantiate seasonfill's bounded contexts.
//
// Each constructor returns a Bundle struct holding the wired
// collaborators for one bounded context. Bundles are passed by
// reference to higher layers (cmd/server.server.go, other wiring
// constructors).
//
// File layout (post-A-1-26 per-context split, PRD §3.2):
//
//   - bootstrap.go    — PersistenceBundle, RuntimeConfigBundle,
//     SchedulerBundle, OnApplied fan-out + StartSubscribers,
//     BuildHTTPServer (root composer). Kernel — imports from every
//     per-context file.
//   - catalog.go      — SonarrBundle, ScanBundle, WebhookBundle,
//     TorrentsyncBundle, InstanceBundle (catalog HTTP handlers).
//   - enrichment.go   — ExtSvcBundle, EnrichmentBundle + the repo
//     adapter shims, dispatcher holder, OMDb batch scanner.
//   - watchdog.go     — WatchdogBundle (healthcheck + watchdog),
//     RegrabBundle (Phase 10 regrab loop + watchdog HTTP handlers).
//   - seriesdetail.go — SeriesDetailBundle (composer + cast + people
//   - refresh handlers).
//   - admin.go        — AuthBundle (admin users, OIDC, IP limiters).
//   - mediaproxy.go   — MediaBundle (mediastore + media_assets repo +
//     MediaHandler).
//   - grab.go         — placeholder; grab UC currently wired inside
//     BuildScan (catalog.go).
//   - discovery.go    — placeholder for future discovery wiring.
//
// Import rules (enforced by tests/lint_wiring_imports_test.go):
//
//   - bootstrap.go is the kernel: imports from every per-context
//     file are permitted.
//   - per-context files import from per-context internal/<ctx>/
//     subtrees (app/, persistence/, rest/, domain/, infrastructure/)
//     and from internal/shared/ for cross-context primitives
//     (clients/, db/, domain/, http/, ports/, reload/, scheduler/).
//   - Legacy top-level application/, infrastructure/, and interface/
//     paths are still referenced where symbols have not yet been
//     drained into per-context trees (Phase 2+ work).
//   - per-context wiring files MUST NOT import cmd/server,
//     cmd/server/loops directly. Cross-area dependencies flow via
//     Bundle references passed into the constructor.
package wiring
