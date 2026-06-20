// Package seriesdetail is the bounded context that owns the canonical
// series-detail composition: the read-side use case that fans in
// catalog projection rows, enrichment data, queue state, and media
// asset references into the document the SPA renders at /series/:id.
//
// Layout (PRD §3.2 vertical slice, established in story 445 A-1-19):
//
//	internal/seriesdetail/
//	  app/  — Composer (9-branch errgroup fan-in for the series page),
//	          CastComposer (single-purpose cast & crew sibling),
//	          MediaResolver (raw TMDB path -> sha256 wire hash bridge),
//	          and the cross-context ports.go contract surface that
//	          declares the narrow read-side interfaces the composers
//	          depend on (SeriesPort, SeasonPort, EpisodePort,
//	          QbitTorrentPort, EnrichmentPort, PeoplePort,
//	          PersonCreditsPort, TaxonomyPort, MediaHashLookupPort,
//	          MediaAssetReader, SonarrQueueLister, ...).
//
// Import direction (PRD §3.3 — enforced by the depcheck test):
//
//	app → internal/catalog/domain/series,
//	      internal/enrichment/domain/{enrichment,people,taxonomy},
//	      internal/shared/{clients/sonarr,domain},
//	      application/ports (catch-all — carve-out, story 449 splits),
//	      infrastructure/database{,/repositories} (model carve-out —
//	      same deferral as catalog/grab/watchdog, story 449+).
//
// Cross-context boundary: the seriesdetail composers are consumed by
// the HTTP interface layer (interface/http/handlers/series_detail.go,
// series_cast.go, series_season.go, series_torrents.go) and wired by
// cmd/server/wiring. They expose value types (Detail, CastPage,
// CastDetail, RecentItem, RecommendationDetail, MediaResolver, ...)
// that other contexts read by value; behaviour is reached strictly
// through Composer/CastComposer constructors with narrow port
// dependencies — never reach into internal/seriesdetail/app/* for
// state or shared mutable singletons.
//
// B-13 invariants preserved bit-for-bit by the story 445 move (per
// project_seasonfill_b13_series_detail_v2):
//
//   - hero backdrop, ratings strip, ON DISK aired denominator,
//     in-progress pill from queue
//   - 9-branch composer errgroup fan-in (series + cache + seasons +
//     episodes + queue + enrichment + cast + recommendations + media)
//   - monogram fallback for missing posters (Star City reference)
//   - cast strip with eager-hash media resolution
//
// The 445 move is path-only — composer.go, cast.go, media_resolver.go,
// and ports.go are byte-identical to their application/seriesdetail/
// origins modulo the package's own import path. No logic, no field,
// no error message changed.
//
// Story origin:
//   - 215 / G-1 — composer hatchout (canonical series-detail document)
//   - 216 / H-1 — cast & crew sibling composer
//   - 312 / 316 — media resolver (TMDB raw-path -> sha256 bridge)
//   - 320 / 321 / 322 — eager-hash composer + on-demand sync + still
//     resolver (B-11 rebuild)
//   - 354-380 — B-13 Series Detail v2 (bleed hero + 9 polish + per-
//     season Sonarr stats + lucide icons + in-progress pill + scan-
//     path fix)
//   - 445 — vertical-slice extraction (this layout)
package seriesdetail
