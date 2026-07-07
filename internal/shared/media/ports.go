// ports.go — narrow contracts the Resolver depends on. Lifted out of
// internal/seriesdetail/app/ports.go as part of the shared-media
// extraction (story 526). Other contexts (discovery, person, network)
// share the same Resolver so the raw-path → sha256-hash mapping is
// consistent across the wire.
package media

import "context"

// HashLookupPort resolves a raw TMDB image source URL → the sha256 hash
// of the media_assets row for the stored variant. Misses surface as
// ports.ErrNotFound — the resolver treats them as a non-error degrade
// path (eager-hash + EnsurePending under the unified contract, nil
// under the legacy flag-off).
//
// EnsurePending writes a media_assets row keyed by `hash` with
// status='pending', source_url=sourceURL, kind=kind, created_at=now —
// IFF the row doesn't already exist. Idempotent: a second call with
// the same hash is a no-op (existing status='stored' / 'failed' is
// preserved). The resolver calls this on miss so the mediaproxy
// handler (story 321) can recover the source URL via
// GetSourceURLByHash and synchronously fetch when the frontend later
// requests /api/v1/media/:hash.
//
// Production implementation: enrichpersistence.MediaAssetsRepository
// (it satisfies both methods by virtue of the same GORM-backed table).
type HashLookupPort interface {
	HashForSourceURL(ctx context.Context, sourceURL string) (string, error)
	EnsurePending(ctx context.Context, hash, sourceURL, kind string) error
}

// BatchHashLookupPort is the batched sibling of HashForSourceURL: it resolves a
// SET of source URLs → their stored sha256 hashes in one round-trip. The returned
// map is keyed by source URL and carries ONLY the stored hits — a URL absent from
// the map is a miss (the resolver handles it exactly as HashForSourceURL's
// ErrNotFound). Production impl:
// enrichpersistence.MediaAssetsRepository.HashForSourceURLs.
//
// Optional: Resolver.ResolveBatch type-asserts its lookup to this interface and
// transparently falls back to per-item Resolve when the concrete lookup does not
// implement it (NewNopResolver, sequential-only test fakes), so ResolveBatch is
// always safe to call. This keeps the existing two-method HashLookupPort (and
// its many test fakes) unbroken.
type BatchHashLookupPort interface {
	HashForSourceURLs(ctx context.Context, urls []string) (map[string]string, error)
}
