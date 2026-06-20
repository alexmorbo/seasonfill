// Package enrichment is the bounded context that owns the seasonfill
// enrichment pipeline: pulling third-party metadata (TMDB images +
// credits, OMDb ratings, biographies) into the local series cache and
// people biography store via TTL-driven workers, coordinated by a
// dispatcher and shaped by the 18-rule per-field source-priority
// merge policy.
//
// Layout (PRD §3.2 vertical slice, established in story 436 A-1-10):
//
//	internal/enrichment/
//	  domain/
//	    enrichment/   — Source enum, TTL math, merge_policy (18 rules),
//	                    backoff, degraded state, hydration, sync_log
//	    people/       — Person + Credit value-type aggregates
//	    taxonomy/     — Genre + Network + Company + Keyword value types
//	  app/
//	    enrichment    — Dispatcher, series_worker, omdb_worker,
//	                    person_worker, omdb_budget, queue, cold_start,
//	                    backoff retry strategy, prewarm hooks
//	    people/       — Person page composer (prewarm + on-demand)
//	    externalservices/ — Settings UseCase (test_runner + provider
//	                    credentials persistence)
//	  persistence/  — Postgres GORM repositories for series, seasons,
//	                  episodes, people, credits, biographies,
//	                  episode_people, series_people, taxonomy, i18n,
//	                  external_ids, content_ratings, origin_release,
//	                  recommendations, live_assets, media_assets
//	                  (story 437 A-1-11).
//	  rest/         — HTTP handlers for the enrichment surface
//	                  (story 438 A-1-12):
//	                    - PeopleHandler    GET    /api/v1/people/:tmdbId
//	                    - SeriesRefreshHandler POST
//	                                /api/v1/instances/:name/series/:id/refresh
//	                    - ExternalServicesHandler
//	                                GET/PUT/POST /api/v1/external-services
//	                                            (+ /:service[/test])
//	                    seriesrefresh/ — UseCase behind SeriesRefreshHandler
//	                                (cache resolve → enrichment.Enqueue).
//
// Import direction (PRD §3.3 — enforced by the depcheck test):
//
//	app          → domain/enrichment, domain/people, domain/taxonomy,
//	               internal/shared/clients/{tmdb,omdb,externalservices},
//	               internal/shared/http, internal/shared/ports
//	domain/*     → (std lib + internal/shared only)
//
// Cross-context boundary: scan, seriesdetail, seriesrefresh, webhook
// and the interface/http handlers consume enrichment through its app
// package (Dispatcher.Enqueue, Worker.Run, ComposeUseCase.Build, and
// the Settings UseCase). They MUST NOT reach into domain/ directly.
//
// 18-rule merge policy invariant: every line of merge logic is
// preserved bit-for-bit from the legacy domain/enrichment/merge_policy
// path. Per-field source-priority ordering (TMDB > OMDb > Sonarr or
// the inverse depending on field) MUST stay byte-identical; see
// merge_policy_test.go for the rule-by-rule contract.
//
// Story origin:
//   - 287 — enrichment dispatcher introduction
//   - 311 — OMDb rating backfill + budget guard
//   - 318 — cold-start re-sweep
//   - 346/352 — TMDB image prewarm + EnsurePending wiring
//   - 353 — engraved-monogram fallback hand-off
//   - 392-398 — F-4b domain logger slog migration
//   - 436 — vertical-slice extraction (this layout)
//   - 437 — persistence/ migrated from infrastructure/database
//   - 438 — rest/ migrated from interface/http/handlers +
//     application/seriesrefresh
package enrichment
