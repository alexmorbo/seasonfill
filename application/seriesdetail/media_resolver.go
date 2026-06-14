// Package seriesdetail — media asset resolver (Story 312 + Story 316).
//
// The TMDB mapper writes raw image paths into canon.PosterAsset /
// person.ProfileAsset / network.LogoAsset / season.PosterAsset. The pre-warm
// pipeline (application/media/enqueuer.go) hashes the FULL CDN URL
// (https://image.tmdb.org/t/p/{size}{path}) and stores the bytes in S3 +
// writes a media_assets row keyed by that sha256. The frontend treats every
// *_asset wire field as a sha256 hex and serves it via /api/v1/media/:hash.
//
// The resolver bridges the two: given (raw_path, size), build the source URL
// the pre-warm pipeline would have used, look up the matching media_assets
// row, return the hash. Nil-or-empty raw path short-circuits to nil. Lookup
// miss returns nil (NOT an error) — the frontend's monogram fallback covers
// the gap. Lookup errors surface to the composer's per-branch tracker (they
// degrade the branch but never 5xx the request).
//
// Story 316 — Resolve gains a priority enqueue side effect (best-effort, no
// wait) so a missed lookup tells the async pre-warm pipeline to fetch now
// instead of waiting for the next cold-start tick. ResolveSync is the
// first-fold variant that does a synchronous fetch with a per-asset budget,
// returning the hash once the bytes are in S3.
package seriesdetail

import (
	"context"
	"errors"
	"log/slog"
	"sync/atomic"

	appmedia "github.com/alexmorbo/seasonfill/application/media"
	"github.com/alexmorbo/seasonfill/application/ports"
)

// MediaResolver wraps a MediaHashLookupPort with the URL-construction
// convention the pre-warm pipeline uses. Stateless for reads; the
// enqueuer + fetcher fields are atomic.Pointer for late-binding via
// SetSideEffects (the wiring layer constructs MediaResolver before the
// media pipeline exists in cmd/server/main.go).
type MediaResolver struct {
	lookup   MediaHashLookupPort
	enqueuer atomic.Pointer[mediaEnqueuerBox]    // story 316 — async priority enqueue
	fetcher  atomic.Pointer[mediaSyncFetcherBox] // story 316 — sync first-fold fetch
	logger   *slog.Logger
}

// MediaEnqueuer is the story 316 async surface — kicks the pre-warm
// pipeline to fetch an asset NOW rather than at the next cold-start
// pass. Nil-OK (legacy behavior — no enqueue side effect).
type MediaEnqueuer interface {
	Enqueue(ctx context.Context, reqs []appmedia.EnqueueRequest)
}

// MediaSyncFetcher is the story 316 synchronous fetch surface. Nil-OK
// (ResolveSync falls back to Resolve when nil).
type MediaSyncFetcher interface {
	FetchSync(ctx context.Context, upstreamURL, kind, ext string) (string, bool)
}

// mediaEnqueuerBox / mediaSyncFetcherBox are pointer-wrapper boxes so
// atomic.Pointer[T] can store an interface value (atomic.Pointer
// requires a concrete pointer type).
type mediaEnqueuerBox struct{ v MediaEnqueuer }
type mediaSyncFetcherBox struct{ v MediaSyncFetcher }

// NewMediaResolver constructs the resolver. Nil-lookup is a valid zero state
// (the composer hands a no-op resolver to keep the call sites uniform when
// the media subsystem is disabled — e.g., MediaAssets repo nil at boot).
// Story 316: enqueuer + fetcher MAY be nil — Resolve still works (no async
// side effect; ResolveSync falls back to Resolve).
func NewMediaResolver(lookup MediaHashLookupPort, enqueuer MediaEnqueuer, fetcher MediaSyncFetcher, logger *slog.Logger) *MediaResolver {
	if logger == nil {
		logger = slog.Default()
	}
	r := &MediaResolver{lookup: lookup, logger: logger}
	if enqueuer != nil {
		r.enqueuer.Store(&mediaEnqueuerBox{v: enqueuer})
	}
	if fetcher != nil {
		r.fetcher.Store(&mediaSyncFetcherBox{v: fetcher})
	}
	return r
}

// SetSideEffects late-binds the Story 316 enqueuer + fetcher onto an
// already-constructed resolver. Used by cmd/server/main.go: the
// resolver is created before wireEnrichment runs (so the composers
// can take a stable *MediaResolver pointer), then the enqueuer +
// fetcher are plugged in once the media pipeline is up. Either arg
// MAY be nil. Concurrent reads are safe — Resolve / ResolveSync load
// via atomic.Pointer.Load.
func (r *MediaResolver) SetSideEffects(enqueuer MediaEnqueuer, fetcher MediaSyncFetcher) {
	if r == nil {
		return
	}
	if enqueuer != nil {
		r.enqueuer.Store(&mediaEnqueuerBox{v: enqueuer})
	}
	if fetcher != nil {
		r.fetcher.Store(&mediaSyncFetcherBox{v: fetcher})
	}
}

// Resolve takes a raw TMDB image path (nil-or-empty allowed) + the size
// variant the pre-warm pipeline used + a kind tag (for logging). Returns a
// pointer to the sha256 hex when a stored media_assets row exists, nil
// otherwise.
//
// Story 316: on miss, fire-and-forget enqueues the asset for async fetch
// (priority hot — the existing pre-warm pipeline is FIFO, so just landing
// in the queue gives it precedence over cold-start enqueues from minutes
// ago).
//
// Lookup errors are logged at Debug. The returned pointer is the value the
// composer assigns to the DTO field; nil renders as the frontend's monogram.
func (r *MediaResolver) Resolve(ctx context.Context, rawPath *string, size, kind string) *string {
	if r == nil || r.lookup == nil {
		return nil
	}
	if rawPath == nil || *rawPath == "" {
		return nil
	}
	url := appmedia.BuildTMDBImageURL(size, *rawPath)
	if url == "" {
		return nil
	}
	hash, err := r.lookup.HashForSourceURL(ctx, url)
	if err == nil && hash != "" {
		return &hash
	}
	if err != nil && !errors.Is(err, ports.ErrNotFound) {
		r.logger.DebugContext(ctx, "media_resolver.lookup_error",
			slog.String("kind", kind),
			slog.String("source_url", url),
			slog.String("error", err.Error()))
	}
	// Miss — async enqueue (best-effort, no wait).
	r.enqueueAsync(ctx, url, kind, appmedia.ExtractExt(*rawPath))
	return nil
}

// ResolveSync is the first-fold variant — on lookup miss, synchronously
// fetches the asset under a per-asset budget. Returns the hash on success
// (bytes are in store + media_assets row written), nil otherwise. Callers
// MUST pass a ctx with a deadline; an undeadlined ctx will be capped at
// the fetcher's onDemandTimeout default.
//
// Callers: use this for hero poster + backdrop + person hero portrait.
// Cast/recommendations/networks/seasons stay on plain Resolve (async only).
func (r *MediaResolver) ResolveSync(ctx context.Context, rawPath *string, size, kind string) *string {
	if r == nil || r.lookup == nil {
		return nil
	}
	if rawPath == nil || *rawPath == "" {
		return nil
	}
	url := appmedia.BuildTMDBImageURL(size, *rawPath)
	if url == "" {
		return nil
	}
	hash, err := r.lookup.HashForSourceURL(ctx, url)
	if err == nil && hash != "" {
		return &hash
	}
	if err != nil && !errors.Is(err, ports.ErrNotFound) {
		r.logger.DebugContext(ctx, "media_resolver.lookup_error",
			slog.String("kind", kind),
			slog.String("source_url", url),
			slog.String("error", err.Error()))
	}
	// Miss — try sync fetch. On failure / timeout, fall back to async
	// enqueue so the frontend's next refresh has a chance.
	if box := r.fetcher.Load(); box != nil && box.v != nil {
		if h, ok := box.v.FetchSync(ctx, url, kind, appmedia.ExtractExt(*rawPath)); ok {
			return &h
		}
	}
	r.enqueueAsync(ctx, url, kind, appmedia.ExtractExt(*rawPath))
	return nil
}

// enqueueAsync fires a best-effort hot enqueue. Nil enqueuer / context done
// silently no-op.
func (r *MediaResolver) enqueueAsync(ctx context.Context, url, kind, ext string) {
	box := r.enqueuer.Load()
	if box == nil || box.v == nil {
		return
	}
	box.v.Enqueue(ctx, []appmedia.EnqueueRequest{{
		UpstreamURL: url,
		Kind:        kind,
		Extension:   ext,
	}})
}

// NewNopMediaResolver returns a resolver that always yields nil. Composer
// behaves the same as if no media_assets rows existed (frontend renders
// monogram fallback). Used at the composer wiring site when MediaAssets is
// unavailable.
func NewNopMediaResolver() *MediaResolver { return &MediaResolver{lookup: nil} }
