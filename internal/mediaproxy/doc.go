// Package mediaproxy is the bounded context that owns the seasonfill
// media proxy: the pre-warm pipeline that downloads TMDB poster /
// backdrop images, persists them content-addressed under sha256(URL),
// and serves them back via /api/v1/media/<hash> from the local blob
// store (S3-backed in production, FS in dev, null for legacy boot).
//
// Layout (PRD §3.2 vertical slice, established in story 427 A-1-1):
//
//	internal/mediaproxy/
//	  domain/         — Asset value-type + Status state machine
//	  app/            — Downloader, Enqueuer, OnDemandFetcher, error_kind
//	  infrastructure/ — Store interface + S3/FS/null implementations + Key
//	  rest/           — HTTP handler for GET /api/v1/media/:hash
//
// Import direction (PRD §3.3 — enforced by the depcheck test):
//
//	rest        → app, domain, infrastructure
//	app         → domain, infrastructure
//	infrastructure → domain
//	domain      → (std lib + internal/shared/domain only)
//
// Cross-context boundary: catalog, enrichment, seriesdetail, and gc
// consume mediaproxy via the app package's exported AssetRepo,
// OnDemandFetcher, Enqueuer and BuildTMDBImageURL / HashFromURL
// helpers. They MUST NOT reach into domain or infrastructure
// directly (the depcheck test does not police that today — story
// 428+ will add the inter-context guard).
//
// Story origin:
//   - 320 — eager hash composer
//   - 321 — handler on-demand sync + SVG placeholder
//   - 346 — CDN rate limiter
//   - 352 — catalog EnsurePending kick
//   - 427 — vertical-slice extraction (this layout)
package mediaproxy
