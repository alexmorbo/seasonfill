// Package wiring contains constructor functions ("wirers") that
// instantiate seasonfill's bounded contexts.
//
// Each constructor returns a Bundle struct holding the wired
// collaborators for one bounded context. Bundles are passed by
// reference to higher layers (cmd/server.server.go, other wiring
// constructors).
//
// File layout (post-A-1 vertical-slice):
//
//   - persistence.go — DB handle + bedrock repositories (admin,
//     catalog) + crypto + tz resolver. Returned by BuildPersistence.
//   - integrations.go — outbound clients (Sonarr per-instance, TMDB,
//     OMDb, mediaproxy store) + their Holder/reload plumbing.
//   - runtime.go — reload bus, scheduler, runtime config snapshot,
//     watchdog runtime, GC use case.
//   - loops.go — long-running background loops (scan, rescan,
//     torrentsync, webhook, grab, regrab, watchdog, healthcheck).
//   - httpiface.go — HTTP edge: admin/auth, catalog, enrichment,
//     mediaproxy, seriesdetail REST routers + middleware wiring.
//
// Import rules (enforced by convention; depguard codifies the
// shared/http kernel boundary):
//
//   - wiring/<area>.go imports from per-context internal/<ctx>/
//     subtrees (app/, persistence/, rest/, domain/, infrastructure/)
//     and from internal/shared/ for cross-context primitives
//     (clients/, db/, domain/, http/, ports/, reload/, scheduler/).
//   - Legacy top-level application/, infrastructure/, and interface/
//     paths are still referenced where symbols have not yet been
//     drained into per-context trees (Phase 2+ work).
//   - wiring/<area>.go MUST NOT import cmd/server, cmd/server/loops,
//     or other wiring/<area>.go directly. Cross-area dependencies
//     flow via Bundle references passed into the constructor.
package wiring
