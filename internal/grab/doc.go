// Package grab is the bounded context that owns the seasonfill
// grab-execution surface: domain entities for one Sonarr grab event
// (status state machine, replay-kind taxonomy, parsed-release value
// object), the application UseCase that drives M-7 atomic success
// writes (grab_records + cooldowns + origin_releases) with backoff
// retries, the GORM-backed persistence repository (List + filter +
// count_imported + counts split across files), and the rest handlers
// for POST /api/v1/decisions/{id}/grab + GET /api/v1/instances/{name}/
// grabs/{id}/episode-files.
//
// Layout (PRD §3.2 vertical slice, established in story 427 A-1-1 for
// mediaproxy, 428 A-1-2 for admin, replicated here in story 431 A-1-5):
//
//	internal/grab/
//	  domain/      — Record entity (the row that flips through
//	                 grabbed→imported / grab_failed / import_failed),
//	                 Status state machine + StatusGroup taxonomy,
//	                 Parsed value object, ReplayKind classifier.
//	  app/         — UseCase (Execute one grab decision, with backoff
//	                 retry + Transactor-wrapped 3-write success path),
//	                 backoff helper.
//	  persistence/ — GORM-backed GrabRepository (the production impl
//	                 of ports.GrabRepository + torrentsync.GrabHashLookup),
//	                 split across grab_repository.go + counts + count_imported
//	                 + list test sub-files.
//	  rest/        — GrabHandler (POST /decisions/{id}/grab — explicit-
//	                 confirm path that bypasses global dry_run for one
//	                 decision), GrabEpisodeFilesHandler (lazy on-demand
//	                 fetch of on-disk files Sonarr placed for the grab).
//
// Import direction (PRD §3.3 — enforced by the depcheck tests):
//
//	rest         → app, domain
//	app          → domain
//	persistence  → domain, internal/shared/dbtx
//	domain       → (std lib + internal/shared/domain only)
//
// Cross-context boundary: the rest layer still references a handful
// of catch-all helpers from the legacy interface/http/handlers package
// (InstanceRegistry, WriteError, WriteInternalError, ToGrabDTO,
// audit.go's grab-render hook) and the shared interface/http/dto.
// These are explicit allowList carve-outs in tests/lint_grab_imports_test.go
// — a future refactor pass will relocate them into per-context homes.
//
// Persistence boundary: GrabRepository participates in transactions
// opened by the catalog-side ports.Transactor (currently
// repositories.GormTransactor). The tx-context key was extracted into
// internal/shared/dbtx during story 431 so both the legacy
// infrastructure/database/repositories tree and the new
// internal/grab/persistence package read through the same private
// txKey type — without this the M-7 atomic success path (grabs.Create
// + cooldowns.Set + origins.Upsert in one transaction) would silently
// auto-commit on Postgres.
//
// Story origin:
//   - 035..038 — grab domain + UseCase design
//   - 110+    — M-7 atomic success path
//   - 215+    — grab episode-files lazy fetch
//   - 427     — vertical-slice extraction protocol (mediaproxy)
//   - 428     — admin extraction
//   - 429     — admin persistence extraction
//   - 430     — admin rest extraction
//   - 431     — this layout (all four layers in one shot)
package grab
