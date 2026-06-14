// Package seriesdetail — media asset resolver (Story 312).
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
package seriesdetail

import (
	"context"
	"errors"
	"log/slog"

	"github.com/alexmorbo/seasonfill/application/media"
	"github.com/alexmorbo/seasonfill/application/ports"
)

// MediaResolver wraps a MediaHashLookupPort with the URL-construction
// convention the pre-warm pipeline uses. Stateless; safe for concurrent use.
type MediaResolver struct {
	lookup MediaHashLookupPort
	logger *slog.Logger
}

// NewMediaResolver constructs the resolver. Nil-lookup is a valid zero state
// (the composer hands a no-op resolver to keep the call sites uniform when
// the media subsystem is disabled — e.g., MediaAssets repo nil at boot).
func NewMediaResolver(lookup MediaHashLookupPort, logger *slog.Logger) *MediaResolver {
	if logger == nil {
		logger = slog.Default()
	}
	return &MediaResolver{lookup: lookup, logger: logger}
}

// Resolve takes a raw TMDB image path (nil-or-empty allowed) + the size
// variant the pre-warm pipeline used + a kind tag (for logging). Returns a
// pointer to the sha256 hex when a stored media_assets row exists, nil
// otherwise.
//
// Lookup errors are logged at Debug (not Warn — a cold series produces many
// expected misses; warning per miss would be noise). The returned pointer is
// the value the composer assigns to the DTO field; nil renders as the
// frontend's monogram fallback.
func (r *MediaResolver) Resolve(ctx context.Context, rawPath *string, size, kind string) *string {
	if r == nil || r.lookup == nil {
		return nil
	}
	if rawPath == nil || *rawPath == "" {
		return nil
	}
	url := media.BuildTMDBImageURL(size, *rawPath)
	if url == "" {
		return nil
	}
	hash, err := r.lookup.HashForSourceURL(ctx, url)
	if err != nil {
		if !errors.Is(err, ports.ErrNotFound) {
			// Real lookup error (DB down, etc). Log + return nil so the
			// page degrades gracefully.
			r.logger.DebugContext(ctx, "media_resolver.lookup_error",
				slog.String("kind", kind),
				slog.String("source_url", url),
				slog.String("error", err.Error()))
		}
		return nil
	}
	if hash == "" {
		return nil
	}
	return &hash
}

// NewNopMediaResolver returns a resolver that always yields nil. Composer
// behaves the same as if no media_assets rows existed (frontend renders
// monogram fallback). Used at the composer wiring site when MediaAssets is
// unavailable.
func NewNopMediaResolver() *MediaResolver { return &MediaResolver{lookup: nil} }
