// Package clients is the umbrella for the shared kernel HTTP clients that
// every bounded context can import without taking an enrichment or
// mediaproxy dependency. Per PRD §3.2 these live under
// internal/shared/clients/ so the enrichment context (story 436) and
// the mediaproxy + seriesdetail contexts (stories 427 + 444) can each
// reach them via the kernel without crossing context boundaries.
//
// Layout (story 435 A-1-9):
//
//	internal/shared/clients/
//	  tmdb/              — TMDB v3 API client (TV / season / person /
//	                       find endpoints) + DTO mappers, rate-limit
//	                       backoff with shared pause/resume signaling,
//	                       runtime-config-reload aware (the cmd/server
//	                       subscriber swaps in a fresh client on
//	                       /api/v1/runtime/reload).
//	  omdb/              — OMDb /?i=<imdb_id> client + rating mapper.
//	                       Similar reload subscriber surface.
//	  externalservices/  — shared HTTP client primitive (User-Agent,
//	                       timeout, retry middleware) + the runtime-
//	                       config Settings UseCase that owns the
//	                       TMDB/OMDb/Trakt key + quota state CRUD
//	                       (subscribed to by the cmd/server tmdb +
//	                       omdb client subscribers, see
//	                       cmd/server/adapters/*_subscriber.go).
//
// Import direction (PRD §3.3 — enforced by
// tests/lint_shared_clients_imports_test.go):
//
//	tmdb              → internal/shared/{ports,observability},
//	                    internal/shared/http/httpx (metrics transport,
//	                    endpoint URL constants).
//	omdb              → internal/shared/{ports,observability},
//	                    internal/shared/http/httpx.
//	externalservices  → internal/shared/{ports,observability},
//	                    application/ports (UseCase consumer surface;
//	                    will relocate to a per-context home in a
//	                    later pass).
//
// Cross-context kernel boundary: leaves below clients/ are kernel —
// they import only std + internal/shared/* + a small set of
// application/ports surfaces while the model split (story 449) is
// still pending. They MUST NOT import application/{enrichment,
// seriesdetail,mediaproxy} or any horizontal-CA path. The depcheck
// guard above pins this until 436 lands.
//
// Story origin:
//   - 116, 121, 131  — original TMDB client (Phase 5)
//   - 124            — OMDb client
//   - 311+           — externalservices rate limiter
//   - 427            — vertical-slice extraction protocol
//   - 435            — this layout (clients moved to shared kernel)
//   - 436            — enrichment context will consume via shared
//     kernel path (deferred)
package clients
