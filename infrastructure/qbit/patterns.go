// Package qbit wraps the autobrr/go-qbittorrent client and ships the
// unregistered-torrent detector used by the Phase 10 Watchdog.
//
// The pattern lists defaultUnregisteredStatuses and trackerDownStatuses
// in this file are copied verbatim from autobrr/tqm
// (https://github.com/autobrr/tqm/blob/master/pkg/config/torrent.go),
// licensed under GPL-3.0. seasonfill is GPL-3.0 (LICENSE), so the copy
// is license-compatible; attribution is preserved here per the GPL.
//
// Source commit reference: autobrr/tqm pkg/config/torrent.go,
// defaultUnregisteredStatuses + trackerDownStatuses literals (2026-05).
package qbit

import "strings"

// defaultUnregisteredStatuses is the baseline pattern list for detecting
// permanent tracker rejection ("torrent not found", "unregistered", etc.).
// Match is case-insensitive substring — caller lowercases the haystack
// before calling helpers below. List intentionally excludes "not found"
// (too many false positives per tqm upstream comment).
var defaultUnregisteredStatuses = []string{
	"complete season uploaded",
	"dead",
	"dupe",
	"i'm sorry dave, i can't do that",
	"infohash not found",
	"internal available",
	"not exist",
	"not registered",
	"nuked",
	"pack is available",
	"packs are available",
	"problem with description",
	"problem with file",
	"problem with pack",
	"retitled",
	"season pack",
	"specifically banned",
	"torrent does not exist",
	"torrent existiert nicht",
	"torrent has been deleted",
	"torrent has been nuked",
	"torrent is not authorized for use on this tracker",
	"torrent is not found",
	"torrent nicht gefunden",
	"tracker nicht registriert",
	"torrent not found",
	"trump",
	"unknown",
	"unregistered",
	"upgraded",
	"uploaded",
}

// trackerDownStatuses lists transient tracker error patterns. These take
// precedence over Unregistered detection — a tracker that is temporarily
// down MUST NOT trigger a re-grab. The list mirrors tqm's libtorrent HTTP
// error subset plus generic network/tracker-availability strings.
var trackerDownStatuses = []string{
	// libtorrent HTTP status messages
	"continue",
	"multiple choices",
	"not modified",
	"bad request",
	"unauthorized",
	"forbidden",
	"internal server error",
	"not implemented",
	"bad gateway",
	"service unavailable",
	"moved permanently",
	"moved temporarily",
	"(unknown http error)",
	// tracker / network errors
	"down",
	"maintenance",
	"tracker is down",
	"tracker unavailable",
	"truncated",
	"unreachable",
	"not working",
	"not responding",
	"timeout",
	"refused",
}

// IsTrackerDown reports whether msg matches any pattern in
// trackerDownStatuses. Empty msg is never down.
func IsTrackerDown(msg string) bool {
	if msg == "" {
		return false
	}
	lower := strings.ToLower(msg)
	for _, p := range trackerDownStatuses {
		if strings.Contains(lower, p) {
			return true
		}
	}
	return false
}

// IsUnregistered reports whether msg matches any pattern in
// defaultUnregisteredStatuses OR the caller-supplied custom list.
// Precedence rule: if IsTrackerDown(msg) is true, IsUnregistered always
// returns false — a tracker-down message must never trigger re-grab even
// if it happens to contain a substring also present in the unregistered
// list (e.g. a tracker error page that mentions "uploaded").
// Custom patterns are matched lowercase-substring — caller need not
// pre-lowercase them; the helper does it. Empty msg is never unregistered.
func IsUnregistered(msg string, custom []string) bool {
	if msg == "" {
		return false
	}
	if IsTrackerDown(msg) {
		return false
	}
	lower := strings.ToLower(msg)
	for _, p := range defaultUnregisteredStatuses {
		if strings.Contains(lower, p) {
			return true
		}
	}
	for _, p := range custom {
		if p == "" {
			continue
		}
		if strings.Contains(lower, strings.ToLower(p)) {
			return true
		}
	}
	return false
}
