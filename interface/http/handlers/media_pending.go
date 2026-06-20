package handlers

import (
	"context"
	"log/slog"

	appmedia "github.com/alexmorbo/seasonfill/internal/mediaproxy/app"
)

// CatalogMediaPendingWriter is the narrow port the catalog handlers
// (ListSeriesCache, enrichMissingFromCache, collectGrabCacheFields)
// use to ensure a media_assets row exists for an eager poster_hash
// before the wire DTO ships. The production implementation is
// *infrastructure/database/repositories.MediaAssetsRepository — its
// EnsurePending is ON CONFLICT (hash) DO NOTHING, so concurrent
// callers race-safely produce exactly one row.
//
// Nil-OK: handlers gate every kick on a nil check so the existing
// test fixtures (and minimal boot) keep compiling without the wire
// plumbing.
type CatalogMediaPendingWriter interface {
	EnsurePending(ctx context.Context, hash, sourceURL, kind string) error
}

// CatalogMediaPrewarmer is the optional kick the catalog handlers
// fire AFTER EnsurePending lands the rows so the downloader starts
// fetching bytes immediately rather than waiting for the next
// background sweep. *appmedia.Enqueuer satisfies it. Nil-OK: when
// the prewarmer isn't wired (boot ordering, story 352 MVP) the
// media handler's on-demand fetch path covers the bytes-not-ready
// case on the first GET /api/v1/media/<hash>.
type CatalogMediaPrewarmer interface {
	Enqueue(ctx context.Context, reqs []appmedia.EnqueueRequest)
}

// catalogPosterEntry is the minimal shape batchEnsurePendingForCatalog
// reads off each catalog item. series.CacheEntry exposes PosterAsset
// directly; the helper takes a slice of *string so callers can
// project from any source.
type catalogPosterEntry struct {
	PosterAsset *string
}

// kickEnsurePendingForCatalog fires a background goroutine that
// ensures a pending media_assets row exists for each catalog entry's
// derived eager poster_hash. The kind is fixed to "poster_w342" to
// match what the series_worker prewarmer would write — the catalog
// only mints w342 hero hashes (see mediaHashForPosterAsset).
//
// Returns immediately. The goroutine inherits a context that
// survives the HTTP request returning (context.WithoutCancel) so
// EnsurePending writes are NOT rolled back if the FE cancels its
// poster request mid-flight. Panics inside the goroutine are
// recovered + WARN-logged so a malformed entry can never crash the
// server.
//
// nil writer → no-op (boot ordering / minimal-boot tests).
func kickEnsurePendingForCatalog(
	ctx context.Context,
	writer CatalogMediaPendingWriter,
	prewarmer CatalogMediaPrewarmer,
	entries []catalogPosterEntry,
	kind string,
	logger *slog.Logger,
) {
	if writer == nil || len(entries) == 0 {
		return
	}
	if logger == nil {
		logger = slog.Default()
	}
	// Snapshot the slice into a fresh allocation — the caller's slice
	// may be reused or mutated after the response commits.
	work := make([]catalogPosterEntry, 0, len(entries))
	for _, e := range entries {
		if e.PosterAsset == nil {
			continue
		}
		work = append(work, e)
	}
	if len(work) == 0 {
		return
	}
	// context.WithoutCancel detaches cancellation but preserves
	// values (trace IDs, logger fields). go.mod is go 1.26 — the
	// helper is stable since 1.21.
	bgCtx := context.WithoutCancel(ctx)

	go func() {
		defer func() {
			if r := recover(); r != nil {
				logger.WarnContext(bgCtx, "catalog_media_pending.panic",
					slog.Any("recover", r),
				)
			}
		}()
		runEnsurePendingForCatalog(bgCtx, writer, prewarmer, work, kind, logger)
	}()
}

// runEnsurePendingForCatalog is the synchronous body of the kicker.
// Extracted so tests can call it directly (and so the goroutine's
// defer-recover wrapper stays a tight one-liner). Best-effort:
// per-entry errors WARN-log and continue.
func runEnsurePendingForCatalog(
	ctx context.Context,
	writer CatalogMediaPendingWriter,
	prewarmer CatalogMediaPrewarmer,
	entries []catalogPosterEntry,
	kind string,
	logger *slog.Logger,
) {
	reqs := make([]appmedia.EnqueueRequest, 0, len(entries))
	for _, e := range entries {
		if e.PosterAsset == nil {
			continue
		}
		url := appmedia.BuildTMDBImageURL(appmedia.SeriesPosterListSize, *e.PosterAsset)
		if url == "" {
			continue
		}
		hash := appmedia.HashFromURL(url)
		if err := writer.EnsurePending(ctx, hash, url, kind); err != nil {
			logger.WarnContext(ctx, "catalog_media_pending.ensure_failed",
				slog.String("hash", hash),
				slog.String("source_url", url),
				slog.String("kind", kind),
				slog.String("error", err.Error()),
			)
			continue
		}
		reqs = append(reqs, appmedia.EnqueueRequest{
			UpstreamURL: url,
			Kind:        kind,
			Extension:   appmedia.ExtractExt(*e.PosterAsset),
		})
	}
	if prewarmer != nil && len(reqs) > 0 {
		prewarmer.Enqueue(ctx, reqs)
	}
}

// catalogPosterEntriesFromCacheEntries projects a slice of
// series.CacheEntry (or anything else exposing PosterAsset) onto
// the helper's input shape without a heap allocation per element
// past the outer slice. Callers in instances.go / audit.go inline
// this conversion.
//
// Intentionally not a generic — the two call sites differ on the
// element type (series.CacheEntry vs (grab.Record + per-instance
// series.CacheEntry lookup)) enough that an explicit conversion
// keeps the call site clearer than a Go 1.18 generic helper.
const catalogPosterKindW342 = "poster_w342"
