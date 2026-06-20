package grab

import "strings"

// ReplayKind classifies a grab row vs its replay parent. Derived
// server-side at read time from Parsed/Quality on both sides. The wire
// omits ReplayKindPrimary because most rows are primaries.
type ReplayKind string

const (
	ReplayKindPrimary ReplayKind = "primary"
	ReplayKindQuality ReplayKind = "replay_quality"
	ReplayKindDub     ReplayKind = "replay_dub"
	ReplayKindOther   ReplayKind = "replay_other"
)

// DeriveReplayKind classifies `current` relative to `parent`.
//
//   - current.ReplayOfID == nil  → ReplayKindPrimary
//   - parent == nil              → ReplayKindOther (parent off-page / unknown)
//   - quality axis improved      → ReplayKindQuality
//   - dub axis improved          → ReplayKindDub
//   - otherwise                  → ReplayKindOther
//
// Quality precedes dub when both axes change. "Better" on an axis
// requires both sides to expose comparable data — for quality, both
// must rank > 0; for dub, both must carry a non-nil Parsed. Otherwise
// the row falls through to ReplayKindOther: we know it IS a replay
// (replay_of_id is set) but we lack data to label the improvement.
func DeriveReplayKind(current Record, parent *Record) ReplayKind {
	if current.ReplayOfID == nil {
		return ReplayKindPrimary
	}
	if parent == nil {
		return ReplayKindOther
	}
	curQ := qualityRank(current.Parsed, current.Quality)
	parQ := qualityRank(parent.Parsed, parent.Quality)
	if curQ > parQ && parQ > 0 {
		return ReplayKindQuality
	}
	if dubAxisImproved(current.Parsed, parent.Parsed) {
		return ReplayKindDub
	}
	return ReplayKindOther
}

// qualityRank maps a (parsed, fallback) pair to an orderable score.
// Higher is better. Unknown returns 0. HDR adds +0.5 so an HDR release
// at the same resolution outranks the SDR original.
func qualityRank(parsed *Parsed, fallbackQuality string) float64 {
	res := 0
	if parsed != nil && parsed.Resolution > 0 {
		res = parsed.Resolution
	}
	var base float64
	switch {
	case res >= 2160:
		base = 7
	case res >= 1440:
		base = 6
	case res >= 1080:
		base = 5
	case res >= 720:
		base = 4
	case res >= 576:
		base = 3
	case res >= 480:
		base = 2
	case res > 0:
		base = 1
	default:
		q := fallbackQuality
		if parsed != nil && parsed.Quality != "" {
			q = parsed.Quality
		}
		base = rankFromQualityString(q)
	}
	if parsed != nil && len(parsed.HDRFlags) > 0 {
		base += 0.5
	}
	return base
}

func rankFromQualityString(q string) float64 {
	if q == "" {
		return 0
	}
	lc := strings.ToLower(q)
	switch {
	case strings.Contains(lc, "2160p"),
		strings.Contains(lc, "4k"),
		strings.Contains(lc, "uhd"):
		return 7
	case strings.Contains(lc, "1440p"):
		return 6
	case strings.Contains(lc, "1080p"):
		return 5
	case strings.Contains(lc, "720p"):
		return 4
	case strings.Contains(lc, "576p"):
		return 3
	case strings.Contains(lc, "480p"):
		return 2
	case strings.Contains(lc, "sd"):
		return 1
	}
	return 0
}

// dubAxisImproved returns true when the child has a dub track and the
// parent does not. Both sides must have a non-nil Parsed — a nil parent
// Parsed means we don't know whether the parent had a dub.
func dubAxisImproved(cur, parent *Parsed) bool {
	if cur == nil || parent == nil {
		return false
	}
	return cur.Dub != "" && parent.Dub == ""
}
