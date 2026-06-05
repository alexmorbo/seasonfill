package qbit

import (
	"context"
	"fmt"
)

// qBit TrackerStatus values mirrored locally so the detector does not
// import the upstream library — keeps the file unit-testable without
// pulling httptest fixtures.
const (
	trackerStatusDisabled   = 0 // DHT, PeX, LSD entries
	trackerStatusNotContact = 1
	trackerStatusWorking    = 2
	trackerStatusUpdating   = 3
	trackerStatusNotWorking = 4
)

// DetectionResult is the per-hash verdict returned by Detect. Both
// Unregistered and TrackerDown can be false (unknown error or all-disabled
// list); they are never both true (precedence rule in IsUnregistered).
type DetectionResult struct {
	Hash         string
	Unregistered bool
	TrackerDown  bool
	TrackerMsg   string
	TrackerURL   string
}

// Detector wraps a Client and applies the C-4 one-tracker-per-release rule
// plus the trackerDown-precedence rule on top of GetTrackers output. It
// owns the custom-unregistered-msg list (operator-configurable per
// instance) but does not own the Client lifecycle.
type Detector struct {
	client          Client
	customUnregMsgs []string
}

// NewDetector binds a Client and a per-instance custom unregistered-msg
// list. The list may be nil or empty; the detector treats nil and empty
// identically (defaults-only matching).
func NewDetector(c Client, customUnregMsgs []string) *Detector {
	cp := make([]string, 0, len(customUnregMsgs))
	for _, s := range customUnregMsgs {
		if s != "" {
			cp = append(cp, s)
		}
	}
	return &Detector{client: c, customUnregMsgs: cp}
}

// Detect fetches the tracker list for hash and applies the verdict
// pipeline:
//
//  1. Skip Status == Disabled (DHT/PeX/LSD).
//  2. If any remaining tracker has Status == Working → torrent is alive.
//     Return all-false (Unregistered=false, TrackerDown=false). This is
//     parent invariant C-4 — multi-tracker torrents are NEVER flagged
//     unregistered on partial-tracker death.
//  3. Else, iterate the remaining trackers. trackerDown takes precedence
//     over unregistered: if any tracker matches IsTrackerDown → return
//     TrackerDown=true. Else if any tracker matches IsUnregistered (with
//     the custom list extending defaults) → return Unregistered=true.
//  4. Else → return all-false (unknown error class — neutral, no re-grab).
//
// On the Unregistered / TrackerDown branches, TrackerMsg + TrackerURL
// carry the first tracker that produced the verdict (deterministic by
// list order — the order qBit returns trackers in).
func (d *Detector) Detect(ctx context.Context, hash string) (DetectionResult, error) {
	if hash == "" {
		return DetectionResult{}, fmt.Errorf("%w: empty hash", ErrTorrentNotFound)
	}
	trackers, err := d.client.GetTrackers(ctx, hash)
	if err != nil {
		return DetectionResult{Hash: hash}, err
	}

	res := DetectionResult{Hash: hash}

	// Build the working set (non-Disabled trackers).
	active := make([]Tracker, 0, len(trackers))
	for _, t := range trackers {
		if t.Status == trackerStatusDisabled {
			continue
		}
		active = append(active, t)
	}
	if len(active) == 0 {
		return res, nil
	}

	// C-4: any working tracker → alive, short-circuit.
	for _, t := range active {
		if t.Status == trackerStatusWorking {
			return res, nil
		}
	}

	// Precedence: tracker-down first, then unregistered.
	for _, t := range active {
		if IsTrackerDown(t.Msg) {
			res.TrackerDown = true
			res.TrackerMsg = t.Msg
			res.TrackerURL = t.URL
			return res, nil
		}
	}
	for _, t := range active {
		if IsUnregistered(t.Msg, d.customUnregMsgs) {
			res.Unregistered = true
			res.TrackerMsg = t.Msg
			res.TrackerURL = t.URL
			return res, nil
		}
	}

	// Unknown error — no verdict, no re-grab.
	_ = trackerStatusNotContact
	_ = trackerStatusUpdating
	_ = trackerStatusNotWorking
	return res, nil
}
