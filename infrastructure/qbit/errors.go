package qbit

import "errors"

// Package-local sentinels for qBit operations. They join (errors.Join) the
// project-wide domain sentinels (domain.ErrInstanceNetwork,
// domain.ErrInstanceUnauthorized) at the call site so callers can use a
// single errors.Is check on either the package-specific or the cross-cutting
// sentinel.
var (
	// ErrTorrentNotFound is returned by GetTrackers when qBit responds 404
	// for the supplied hash. The upstream library normalises 404 to a nil
	// slice + nil error; the wrapper promotes that to a sentinel so callers
	// can distinguish "torrent gone" from "torrent has no trackers".
	ErrTorrentNotFound = errors.New("qbit torrent not found")

	// ErrInvalidConfig is returned by NewClient when cfg fails validation
	// (empty URL, bad scheme, unparseable host). Wraps the underlying
	// url.Parse error when available.
	ErrInvalidConfig = errors.New("qbit invalid config")
)
