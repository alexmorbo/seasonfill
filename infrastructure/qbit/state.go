package qbit

import "strings"

// StateGroup is the 8-bucket projection of qBit's 22-value raw state
// enum (PRD v4 §4.3). The series detail card colors and the
// lifecycle event log both work at this grain; raw state lives next
// to it for tooltips and diagnostics but never drives logic.
type StateGroup string

const (
	StateGroupDownloading StateGroup = "downloading"
	StateGroupSeeding     StateGroup = "seeding"
	StateGroupStalled     StateGroup = "stalled"
	StateGroupQueued      StateGroup = "queued"
	StateGroupPaused      StateGroup = "paused"
	StateGroupChecking    StateGroup = "checking"
	StateGroupError       StateGroup = "error"
	StateGroupUnknown     StateGroup = "unknown"
)

// stateGroup maps the verbatim qBit state string into the seasonfill
// lifecycle bucket. Source of truth for the 22 raw values is the
// autobrr/go-qbittorrent v1.16.0 TorrentState enum (domain.go:152–217),
// itself a verbatim mirror of qBit Web API v2.11.
//
// Two qBit versions in play simultaneously:
//   - qBit 4.x: pausedUP / pausedDL
//   - qBit 5.x: stoppedUP / stoppedDL (renamed in 5.0)
//
// Both spellings map to StateGroupPaused — operators with mixed
// fleets get one chip color regardless.
//
// Unknown / empty / future state strings fall through to
// StateGroupUnknown rather than panicking. qBit 5.x has begun
// reporting `forcedMetaDL` which the v1.16.0 enum does NOT yet
// include; explicit string match handles that until autobrr cuts a
// release.
func stateGroup(raw string) StateGroup {
	switch strings.TrimSpace(raw) {
	case "downloading", "forcedDL", "metaDL", "forcedMetaDL", "allocating":
		return StateGroupDownloading
	case "uploading", "forcedUP":
		return StateGroupSeeding
	case "stalledDL", "stalledUP":
		return StateGroupStalled
	case "queuedDL", "queuedUP":
		return StateGroupQueued
	case "pausedDL", "pausedUP", "stoppedDL", "stoppedUP":
		return StateGroupPaused
	case "checkingDL", "checkingUP", "checkingResumeData", "moving":
		return StateGroupChecking
	case "error", "missingFiles":
		return StateGroupError
	case "unknown", "":
		return StateGroupUnknown
	default:
		return StateGroupUnknown
	}
}

// StateGroupFor exposes stateGroup to other infrastructure packages
// (e.g. the A-2 reconciler that builds qbit_torrent_events rows
// without going through the SyncSession projection). Lowercase
// internal name is preserved for tests.
func StateGroupFor(raw string) StateGroup { return stateGroup(raw) }
