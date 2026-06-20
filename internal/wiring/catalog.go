package wiring

// catalog.go owns the wiring for the catalog bounded context:
// per-instance Sonarr clients (Story 332), the scan/grab/rescan
// stack (Story 334), the webhook UC + Syncer + Reconciler + StatusCache
// (Story 335), the torrentsync UC + reconciler + query (Stories
// 220/221/222), and the catalog-side HTTP handlers (instance CRUD +
// probe — Story 336).
//
// Per-context split per PRD §3.2 (Story 452). The original A-2 layout
// concentrated every wirer into 5 files (persistence/integrations/
// loops/httpiface/runtime); the per-context layout pushes each Build*
// wirer into the file matching its bounded context so future stories
// touch one file per change.
