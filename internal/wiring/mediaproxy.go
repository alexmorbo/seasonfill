package wiring

import (
	"context"
	"fmt"
	"log/slog"

	adminrest "github.com/alexmorbo/seasonfill/internal/admin/rest"
	"github.com/alexmorbo/seasonfill/internal/config"
	enrichpersistence "github.com/alexmorbo/seasonfill/internal/enrichment/persistence"
	mediastore "github.com/alexmorbo/seasonfill/internal/mediaproxy/infrastructure"
	mediaproxyrest "github.com/alexmorbo/seasonfill/internal/mediaproxy/rest"
)

// mediaproxy.go owns the wiring for the mediaproxy bounded context:
// the Story 214 F-1 media pipeline (mediastore.Store + media_assets
// repo + HTTP MediaHandler). The OnDemandFetcher is late-bound from
// server.go's LATE BIND ZONE (Story 321) — the handler is constructed
// here with PendingResolver but no OnDemandFetcher so the embedded
// SVG placeholder remains the boot fallback.

// MediaBundle groups the Story 214 (F-1) media pipeline components
// constructed at boot. Returned by BuildMedia. Threaded into:
//
//   - httpserver.NewServer (Handler) — the HTTP wirer remains in
//     server.go for now.
//   - server.go's enrichment-wiring block — Store + AssetsRepo are
//     forwarded into the enrichmentRepoBundle, and the gc weekly job
//     captures the same handles.
//   - server.go's MediaResolver fallback — *MediaAssetsRepository
//     satisfies media.HashLookupPort (shared package since story 526);
//     the wirer hands the concrete pointer back so the resolver gets
//     the nil-OK fallback.
//   - server.go calls Handler.SetOnDemandFetcher(...) AFTER wireEnrichment
//     returns (story 321 late-bind). The wirer intentionally leaves the
//     fetcher unset so the pre-339 boot ordering survives: handler exists
//     for router registration; fetcher plugs in once the media pipeline
//     is up.
//
// Field-level invariants:
//
//   - Store is the production mediastore.Store backed by S3/FS/null
//     depending on bootCfg.MediaStore.Mode. ErrNotSupported in mode=off
//     is intentional — downloader treats it as soft fail; the Handler
//     502s on lost-object, which is the correct behaviour for an
//     unconfigured deploy.
//
//   - AssetsRepo is shared between the downloader (via the enrichment
//     bundle) and the HTTP Handler — one source of truth for the
//     media_assets rows.
//
//   - Handler is constructed WITHOUT the on-demand fetcher. Story 321:
//     the fetcher is injected via SetOnDemandFetcher after enrichBundle
//     returns. The PendingResolver IS set here because AssetsRepo
//     already satisfies the GetSourceURLByHash contract (story 320);
//     the embedded SVG placeholder remains the boot fallback.
type MediaBundle struct {
	Store      mediastore.Store
	AssetsRepo *enrichpersistence.MediaAssetsRepository
	Handler    *mediaproxyrest.MediaHandler
}

// BuildMedia wires the media pipeline (Story 214 F-1 + Story 320 + 321
// pre-late-bind state). Construction order mirrors the pre-339 inline
// body verbatim:
//
//  1. mediastore.New (mode/S3/FSPath drawn from bootCfg.MediaStore).
//  2. MediaAssetsRepository over persistence.DB.
//  3. MediaHandler (without OnDemandFetcher — late-bound in server.go).
//
// rootCtx is required because mediastore.New takes a context (S3 client
// construction may issue early HEADs in some backends). server.go passes
// its rootCtx in — the same context the rest of the boot ladder uses.
//
// mediastore.New is the only fallible step; the error is wrapped with the
// `mediastore:` prefix to match the pre-339 message verbatim.
func BuildMedia(
	rootCtx context.Context,
	persistence *PersistenceBundle,
	bootCfg *config.Bootstrap,
	log *slog.Logger,
) (*MediaBundle, error) {
	store, err := mediastore.New(rootCtx, mediastore.Config{
		Mode: mediastore.Mode(bootCfg.MediaStore.Mode),
		S3: mediastore.S3Config{
			Endpoint:  bootCfg.MediaStore.S3.Endpoint,
			Bucket:    bootCfg.MediaStore.S3.Bucket,
			AccessKey: bootCfg.MediaStore.S3.AccessKey,
			SecretKey: bootCfg.MediaStore.S3.SecretKey,
			Region:    bootCfg.MediaStore.S3.Region,
			UseSSL:    bootCfg.MediaStore.S3.UseSSL,
		},
		FSPath:        bootCfg.MediaStore.FSPath,
		ReadInflight:  bootCfg.ExternalServices.MediaS3ReadInflight,
		WriteInflight: bootCfg.ExternalServices.MediaS3WriteInflight,
	})
	if err != nil {
		return nil, fmt.Errorf("mediastore: %w", err)
	}

	assetsRepo := enrichpersistence.NewMediaAssetsRepository(persistence.DB)

	// Story 321: handler constructed WITHOUT on-demand fetcher. server.go
	// calls SetOnDemandFetcher after wireEnrichment returns. Until then,
	// pending hashes serve the embedded SVG placeholder — visually stable
	// while the media pipeline boots.
	handler := mediaproxyrest.NewMediaHandler(mediaproxyrest.MediaHandlerDeps{
		Store:              store,
		Repo:               assetsRepo,
		PendingResolver:    assetsRepo, // story 320: satisfies GetSourceURLByHash
		Logger:             log,
		OnDemandWallBudget: bootCfg.ExternalServices.MediaOnDemandBudget, // W19-1
		ServeGetBudget:     bootCfg.ExternalServices.MediaServeGetBudget, // story 1099 Fix D
		// OnDemandFetcher: late-bound in server.go (story 321 wiring).
	})

	return &MediaBundle{
		Store:      store,
		AssetsRepo: assetsRepo,
		Handler:    handler,
	}, nil
}

// Compile-time check: *MediaAssetsRepository satisfies the catalog-side
// pending writer port consumed by InstancesHandler + AuditHandler
// (story 352). Catches a future signature drift at build time rather
// than at runtime.
var _ adminrest.CatalogMediaPendingWriter = (*enrichpersistence.MediaAssetsRepository)(nil)
