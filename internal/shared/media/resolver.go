// Package media owns the cross-context bridge between raw TMDB image
// paths (carried on canon.PosterAsset / person.ProfileAsset / network.
// LogoAsset / season.PosterAsset, etc.) and the sha256 wire hash the
// frontend hands to /api/v1/media/:hash.
//
// Pre-history: the resolver originated inside internal/seriesdetail/
// app (story 312/316/320/347). Story 526 lifted it into
// internal/shared/media so the discovery, enrichment, and future
// cross-context handlers (network logos, person credits) share a
// single hash-translation surface — preventing the "works in series
// detail but not in /discovery" class of bug.
//
// The TMDB mapper writes raw image paths into the canon. The pre-warm
// pipeline (internal/mediaproxy/app/enqueuer.go) hashes the FULL CDN
// URL (https://image.tmdb.org/t/p/{size}{path}) and stores the bytes
// in S3 + writes a media_assets row keyed by that sha256. The
// frontend treats every *_asset wire field as a sha256 hex and
// serves it via /api/v1/media/:hash.
//
// The resolver bridges the two: given (raw_path, size), build the
// source URL the pre-warm pipeline would have used, look up the
// matching media_assets row, return the hash. Nil-or-empty raw path
// short-circuits to nil (legacy flag) or sentinel (unified flag).
// Lookup miss with the unified flag mints the eager content hash +
// pending media_assets row so the FE gets a stable wire field that
// the mediaproxy can fill on the user's next GET. ResolveSync is the
// first-fold variant that does a synchronous fetch with a per-asset
// budget, returning the hash once the bytes are in S3.
package media

import (
	"context"
	"errors"
	"log/slog"
	"sync/atomic"
	"time"

	"github.com/VictoriaMetrics/metrics"

	appmedia "github.com/alexmorbo/seasonfill/internal/mediaproxy/app"
	ports "github.com/alexmorbo/seasonfill/internal/shared/dataports"
	sharedports "github.com/alexmorbo/seasonfill/internal/shared/ports"
)

// eagerPersistTimeout bounds the detached EnsurePending + enqueue that
// hands the frontend an eager hash after the caller's sync fetch budget
// is spent. Short — it only writes one pending row + a channel push.
const eagerPersistTimeout = 3 * time.Second

// sentinelEmitCounter increments every time Resolve/ResolveSync hands
// back the sentinel hash. Labels split the cases an operator wants to
// triage independently:
//
//   - reason="nil_path"   — canon row carries a NULL/empty raw asset
//     path. Typical root cause is a known prior merge-policy bug that
//     zeroed canon.poster_asset / canon.backdrop_asset on upsert; the
//     fix is a backfill (see `cli backfill-assets`), not a code change.
//   - reason="empty_url"  — BuildTMDBImageURL returned empty (path was
//     whitespace-only or unmappable). Rare; usually a mapper bug.
//   - reason="ensure_pending_failed" — eager-hash path failed to write
//     a pending media_assets row; resolver fell back to sentinel so
//     the FE renders a stable visual instead of a broken slot.
//
// Diagnoses Bug B class: "every series tile renders sentinel" — the
// counter pinpoints whether the cause is data (nil_path), mapper
// (empty_url), or persistence (ensure_pending_failed) without
// requiring repro tracing.
func sentinelEmitCounter(reason, kind string) *metrics.Counter {
	return metrics.GetOrCreateCounter(
		`seasonfill_media_resolver_sentinel_emit_total{reason="` + reason +
			`",kind="` + kind + `"}`)
}

// Resolver wraps a HashLookupPort with the URL-construction convention
// the pre-warm pipeline uses. Stateless for reads; the enqueuer +
// fetcher fields are atomic.Pointer for late-binding via SetSideEffects
// (the wiring layer constructs Resolver before the media pipeline
// exists in cmd/server/server.go).
//
// Story 347 — unifiedResolve toggles the always-emit-hash contract:
// when true (the production default), Resolve emits a real hash via
// eager-hash + EnsurePending for every non-nil rawPath, and the
// sentinel-missing hash for nil/empty rawPath. When false (env
// kill-switch), Resolve falls back to legacy nil-on-miss behavior.
type Resolver struct {
	lookup         HashLookupPort
	enqueuer       atomic.Pointer[enqueuerBox]    // story 316 — async priority enqueue
	fetcher        atomic.Pointer[syncFetcherBox] // story 316 — sync first-fold fetch
	unifiedResolve atomic.Bool                    // story 347 — always-emit-hash contract
	logger         *slog.Logger
}

// Enqueuer is the story 316 async surface — kicks the pre-warm
// pipeline to fetch an asset NOW rather than at the next cold-start
// pass. Nil-OK (legacy behavior — no enqueue side effect).
type Enqueuer interface {
	Enqueue(ctx context.Context, reqs []appmedia.EnqueueRequest)
}

// SyncFetcher is the story 316 synchronous fetch surface. Nil-OK
// (ResolveSync falls back to Resolve when nil).
type SyncFetcher interface {
	FetchSync(ctx context.Context, upstreamURL, kind, ext string) (string, bool)
}

// enqueuerBox / syncFetcherBox are pointer-wrapper boxes so
// atomic.Pointer[T] can store an interface value (atomic.Pointer
// requires a concrete pointer type).
type enqueuerBox struct{ v Enqueuer }
type syncFetcherBox struct{ v SyncFetcher }

// NewResolver constructs the resolver. Nil-lookup is a valid zero state
// (the composer hands a no-op resolver to keep the call sites uniform
// when the media subsystem is disabled — e.g., MediaAssets repo nil at
// boot). Story 316: enqueuer + fetcher MAY be nil — Resolve still
// works (no async side effect; ResolveSync falls back to Resolve).
func NewResolver(lookup HashLookupPort, enqueuer Enqueuer, fetcher SyncFetcher, logger *slog.Logger) *Resolver {
	if logger == nil {
		logger = sharedports.DomainLogger(slog.Default(), "composer")
	}
	r := &Resolver{lookup: lookup, logger: logger}
	if enqueuer != nil {
		r.enqueuer.Store(&enqueuerBox{v: enqueuer})
	}
	if fetcher != nil {
		r.fetcher.Store(&syncFetcherBox{v: fetcher})
	}
	return r
}

// SetSideEffects late-binds the Story 316 enqueuer + fetcher onto an
// already-constructed resolver. Used by cmd/server/server.go: the
// resolver is created before wireEnrichment runs (so the composers
// can take a stable *Resolver pointer), then the enqueuer + fetcher
// are plugged in once the media pipeline is up. Either arg MAY be
// nil. Concurrent reads are safe — Resolve / ResolveSync load via
// atomic.Pointer.Load.
func (r *Resolver) SetSideEffects(enqueuer Enqueuer, fetcher SyncFetcher) {
	if r == nil {
		return
	}
	if enqueuer != nil {
		r.enqueuer.Store(&enqueuerBox{v: enqueuer})
	}
	if fetcher != nil {
		r.fetcher.Store(&syncFetcherBox{v: fetcher})
	}
}

// SetUnifiedResolve toggles the story-347 always-emit-hash contract on
// this resolver. Wiring sets it post-construction so the constructor
// signature stays stable for existing callers + tests. Concurrent reads
// are safe via atomic.Bool. Production wiring reads the bool from
// cfg.Enrichment.MediaUnifiedResolve (default-on; env kill-switch).
func (r *Resolver) SetUnifiedResolve(v bool) {
	if r == nil {
		return
	}
	r.unifiedResolve.Store(v)
}

// Resolve takes a raw TMDB image path (nil-or-empty allowed) + the size
// variant the pre-warm pipeline used + a kind tag (for logging). Returns a
// pointer to the sha256 hex when a stored media_assets row exists.
//
// Story 316: on miss, fire-and-forget enqueues the asset for async fetch
// (priority hot — the existing pre-warm pipeline is FIFO, so just landing
// in the queue gives it precedence over cold-start enqueues from minutes
// ago).
//
// Story 347: when unifiedResolve is on (production default), Resolve
// uniformly emits a hash for every call — the sentinel for nil/empty
// rawPath, the eager content-hash + EnsurePending for misses, the
// stored hash on hits. The frontend gets a stable visual slot for every
// category (cast, networks, season posters, episode stills,
// recommendations) instead of nil. When the flag is off (env
// kill-switch), the legacy nil-on-miss behavior is preserved verbatim.
//
// Lookup errors are logged at Debug. The returned pointer is the value the
// composer assigns to the DTO field; nil renders as the frontend's monogram.
func (r *Resolver) Resolve(ctx context.Context, rawPath *string, size, kind string) *string {
	if r == nil || r.lookup == nil {
		return nil
	}
	unified := r.unifiedResolve.Load()
	if rawPath == nil || *rawPath == "" {
		if unified {
			r.emitSentinel(ctx, "nil_path", kind, "")
			h := appmedia.SentinelMissingHash
			return &h
		}
		return nil
	}
	url := appmedia.BuildTMDBImageURL(size, *rawPath)
	if url == "" {
		if unified {
			r.emitSentinel(ctx, "empty_url", kind, *rawPath)
			h := appmedia.SentinelMissingHash
			return &h
		}
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
	ext := appmedia.ExtractExt(*rawPath)
	if unified {
		// Miss — mint the eager content-hash + EnsurePending so the
		// handler can recover on the user's GET /api/v1/media/:hash.
		// Mirrors the ResolveSync story-320 eager-hash path. On
		// EnsurePending failure, fall back to sentinel (better than
		// nil; the FE renders the SVG placeholder).
		eagerHash := appmedia.HashFromURL(url)
		if perr := r.lookup.EnsurePending(ctx, eagerHash, url, kind); perr != nil {
			r.logger.DebugContext(ctx, "media_resolver.ensure_pending_failed",
				slog.String("kind", kind),
				slog.String("source_url", url),
				slog.String("error", perr.Error()))
			r.enqueueAsync(ctx, url, kind, ext)
			r.emitSentinel(ctx, "ensure_pending_failed", kind, url)
			sentinel := appmedia.SentinelMissingHash
			return &sentinel
		}
		r.enqueueAsync(ctx, url, kind, ext)
		return &eagerHash
	}
	// Legacy flag-off path: async enqueue + nil.
	r.enqueueAsync(ctx, url, kind, ext)
	return nil
}

// ResolveSync is the first-fold variant — on lookup miss, synchronously
// fetches the asset under a per-asset budget. Returns the hash on success
// (bytes are in store + media_assets row written). When sync fetch misses
// the budget, the eager-hash path (story 320) returns the deterministic
// sha256-hex of the source URL anyway + writes a media_assets row with
// status='pending' so the handler's pending-row sync fetch (story 321)
// can recover. Callers MUST pass a ctx with a deadline; an undeadlined
// ctx will be capped at the fetcher's onDemandTimeout default.
//
// Callers: use this for hero poster + backdrop + person hero portrait.
// Cast/recommendations/networks/seasons stay on plain Resolve (async only +
// nil on miss — they render as monograms below the fold).
func (r *Resolver) ResolveSync(ctx context.Context, rawPath *string, size, kind string) *string {
	if r == nil || r.lookup == nil {
		return nil
	}
	unified := r.unifiedResolve.Load()
	if rawPath == nil || *rawPath == "" {
		if unified {
			r.emitSentinel(ctx, "nil_path", kind, "")
			h := appmedia.SentinelMissingHash
			return &h
		}
		return nil
	}
	url := appmedia.BuildTMDBImageURL(size, *rawPath)
	if url == "" {
		if unified {
			r.emitSentinel(ctx, "empty_url", kind, *rawPath)
			h := appmedia.SentinelMissingHash
			return &h
		}
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
	ext := appmedia.ExtractExt(*rawPath)
	// Miss — try sync fetch first; FetchSync writes status='stored' on
	// success, so callers get the warm hash directly.
	if box := r.fetcher.Load(); box != nil && box.v != nil {
		if h, ok := box.v.FetchSync(ctx, url, kind, ext); ok {
			return &h
		}
	}
	// Sync fetch missed (budget / failure). Story 320: eager-hash path —
	// the canonical sha256-hex of the URL is deterministic and stable, so
	// we hand it to the frontend now AND pre-register a status='pending'
	// row in media_assets so the handler can recover the source URL via
	// GetSourceURLByHash and synchronously fetch on the user's GET
	// /api/v1/media/:hash (story 321). Best-effort: an EnsurePending
	// error logs at Debug and falls back to the legacy async-enqueue +
	// nil path so the frontend's next refresh has a chance.
	eagerHash := appmedia.HashFromURL(url)
	// The sync fetch consumed the caller's budget (posterResolveBudget),
	// but registering the pending row + enqueue must still succeed so the
	// frontend receives the eager hash and GET /media/:hash can recover the
	// source URL and download-through. Detach from the (now-expired) caller
	// ctx while preserving request-scoped values; give it a fresh short
	// budget. Mirrors the detached-refresh pattern in
	// internal/seriesdetail/app/series_ratings.go:251.
	persistCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), eagerPersistTimeout)
	defer cancel()
	if perr := r.lookup.EnsurePending(persistCtx, eagerHash, url, kind); perr != nil {
		r.logger.DebugContext(persistCtx, "media_resolver.ensure_pending_failed",
			slog.String("kind", kind),
			slog.String("source_url", url),
			slog.String("error", perr.Error()))
		r.enqueueAsync(persistCtx, url, kind, ext)
		return nil
	}
	// Pending row written — also kick the async pre-warm pipeline so the
	// downloader has a shot at landing the bytes before the handler's
	// next request races for them. Cheap; FIFO-deduped on hash.
	r.enqueueAsync(persistCtx, url, kind, ext)
	return &eagerHash
}

// emitSentinel records a sentinel-hash emission for production
// diagnostics. Two surfaces — a Debug log line with the reason / kind
// (so a single-series grep correlates) and a labelled counter (so the
// VictoriaMetrics dashboard can plot per-reason rates without
// requiring trace digging). source is the offending path / URL when
// known; empty when the trigger was a nil pointer.
//
// Cheap — the metric handle is interned by VictoriaMetrics on
// reason+kind so we don't churn allocs in the hot composer loop.
func (r *Resolver) emitSentinel(ctx context.Context, reason, kind, source string) {
	sentinelEmitCounter(reason, kind).Inc()
	if r == nil || r.logger == nil {
		return
	}
	r.logger.DebugContext(ctx, "media_resolver.sentinel_emitted",
		slog.String("reason", reason),
		slog.String("kind", kind),
		slog.String("source", source),
	)
}

// enqueueAsync fires a best-effort hot enqueue. Nil enqueuer / context done
// silently no-op.
func (r *Resolver) enqueueAsync(ctx context.Context, url, kind, ext string) {
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

// NewNopResolver returns a resolver that always yields nil. Composer
// behaves the same as if no media_assets rows existed (frontend renders
// monogram fallback). Used at the composer wiring site when MediaAssets is
// unavailable.
func NewNopResolver() *Resolver { return &Resolver{lookup: nil} }
