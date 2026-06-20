// Package media hosts the mediaproxy bounded context's application
// services — Enqueuer (producer side of the pre-warm pipeline),
// Downloader (consumer side), and OnDemandFetcher (handler-driven
// synchronous fill). The directory name is `app` (PRD §3.2 layout)
// but the Go package keeps its established short name `media` so
// consumers' `appmedia` alias survives the story 427 move
// unchanged. A later rename-pass story can normalize the package
// name to `app` (with operator-supplied aliases everywhere).
//
// ports.go gathers the narrow port interfaces this layer publishes
// to its consumers. The interfaces themselves are declared in their
// owning files (so the production impl + test fakes live next to the
// interface that constrains them) — this file is an index pointing
// at each port so future readers can find the contract surface
// without grepping. Story 427 (A-1-1) carved out the bounded
// context; story 428+ will move the operator-visible Enqueuer /
// AssetRepo / OnDemandFetcher decls into this file as part of the
// "ports.go is the contract" convention. Until then, see:
//
//   - AssetRepo (downloader.go) — repo port the Downloader writes
//     pending/stored/failed rows through. Production impl is
//     *infrastructure/database/repositories.MediaAssetsRepository.
//
//   - OnDemandFetcher (ondemand.go) — interface the HTTP handler
//     calls when a hash is known but bytes are not yet stored.
//     Production impl is *onDemandFetcher (same file).
//
//   - Enqueuer (enqueuer.go) — producer side, exposed to enrichment
//     + seriesdetail + catalog as the bottleneck-respecting kick.
//
// Cross-package consumers (catalog, enrichment, seriesdetail, gc)
// import these names directly from package media via the `appmedia`
// alias kept stable across the story 427 move.

package media
