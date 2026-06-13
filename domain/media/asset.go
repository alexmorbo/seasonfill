// Package media owns the canonical Asset value-type for the media
// subsystem. The HTTP handler, the downloader, and the repository all
// share this one shape; the lifecycle is a strict state machine:
//
//	enqueue → pending → (download ok) → stored
//	                 ↘ (download fail × 2) → failed
//
// Hash is the sha256 hex string the mediastore key embeds; UpstreamURL
// is retained so the downloader can refetch on a lost-object recovery
// path. Size and ContentType mirror what the bytes carry. The Asset
// type is value-type — repositories return a fresh copy on every read.
package media

import (
	"errors"
	"strings"
)

// Status is the lifecycle enum. Stored as TEXT in media_assets so the
// SQLite/Postgres mirror requires zero migration churn.
type Status string

const (
	// StatusPending is the initial state right after enqueue + before
	// the first download attempt completes. The handler treats it as
	// a 404 (the frontend renders a placeholder).
	StatusPending Status = "pending"
	// StatusStored is the terminal happy path — bytes are in
	// mediastore + Content-Type / Size on the row are authoritative.
	StatusStored Status = "stored"
	// StatusFailed is the terminal sad path — two consecutive
	// download attempts failed. The handler treats it as a 404. A
	// future E-2 GC sweep MAY re-enqueue failed entries; F-1 does
	// not.
	StatusFailed Status = "failed"
)

// IsValid reports whether s is one of the three enum members.
// Returns false for the empty string. Used by the repository's
// upsert path so a bad caller never persists "foo".
func (s Status) IsValid() bool {
	switch s {
	case StatusPending, StatusStored, StatusFailed:
		return true
	}
	return false
}

// String implements fmt.Stringer.
func (s Status) String() string { return string(s) }

// Asset is the value-type for one row in media_assets. All fields are
// authoritative — the downloader fills them at Put time, the handler
// reads them at serve time.
type Asset struct {
	// Hash is the sha256 hex (lowercase, 64 chars) of UpstreamURL.
	// Doubles as the public path component in /api/v1/media/:hash
	// and as the primary key in media_assets.
	Hash string
	// UpstreamURL is the canonical, fully-qualified source URL
	// (e.g. https://image.tmdb.org/t/p/w342/abc.jpg). Stored to
	// enable refetch on a lost-object recovery path; the downloader
	// also uses it to compute the content-type fallback when the
	// upstream HEAD/GET response is sparse.
	UpstreamURL string
	// Kind is a free-form classifier ("poster_w342", "backdrop_w1280",
	// "network_logo_w185"). Informational only — not part of the
	// hashing input. The GC story (E-2) will read it; F-1 only writes.
	Kind string
	// ContentType is the upstream-reported content type ("image/jpeg",
	// "image/png", "image/webp"). Empty string is allowed but the
	// handler falls back to "application/octet-stream" if so.
	ContentType string
	// Size is the byte count of the stored payload. Used by the
	// LRU's accounting + by future GC sweeps.
	Size int64
	// Status is the lifecycle state.
	Status Status
}

// ErrInvalidAsset is returned by the repository when the caller hands
// in a row that fails the basic invariants (empty hash, empty
// upstream_url, unknown status).
var ErrInvalidAsset = errors.New("media: invalid asset")

// Validate enforces the basic invariants. Used by the repository's
// Upsert path AND by the downloader before it writes the row, so a
// bad enqueue-payload bug surfaces as a returned error rather than a
// silent persisted-garbage row.
func (a Asset) Validate() error {
	if strings.TrimSpace(a.Hash) == "" {
		return errors.New("media: asset.hash is empty")
	}
	if len(a.Hash) != 64 {
		return errors.New("media: asset.hash must be sha256 hex (64 chars)")
	}
	if strings.TrimSpace(a.UpstreamURL) == "" {
		return errors.New("media: asset.upstream_url is empty")
	}
	if !a.Status.IsValid() {
		return errors.New("media: asset.status is not a valid enum member")
	}
	return nil
}
