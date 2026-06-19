package errors

import "fmt"

// MediaAssetNotFoundError signals that a media_assets row keyed on
// (Kind, Key) does not exist (or the lookup-by-hash misses). Reachable
// from media GET handlers — distinguishes the placeholder-fallback path
// from the bytes-on-disk path. Maps to HTTP 404 when surfaced directly.
//
// Kind is the asset kind slug (e.g. "poster_w342", "backdrop_w1280", or
// "hash" for hash-keyed lookups); Key is whatever identity the caller
// passed in (source_url, hash, or content hash).
//
// F-2b-3 keeps the legacy errors.Is(err, ports.ErrNotFound) consumer
// path intact via errors.Join — see the media_assets_repository.go
// migration sites — so the existing handler placeholder-fallback (see
// interface/http/handlers/media.go) keeps working until F-2c flips it
// to consume the typed error directly.
type MediaAssetNotFoundError struct {
	Kind string
	Key  string
}

func (e *MediaAssetNotFoundError) Error() string {
	return fmt.Sprintf("media_asset %s/%s not found", e.Kind, e.Key)
}

func (e *MediaAssetNotFoundError) Code() string { return "media_asset_not_found" }

func (e *MediaAssetNotFoundError) Retriable() bool { return false }
