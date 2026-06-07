package sonarr

import (
	"regexp"
	"strings"
)

// Extras carries the bits Sonarr's /api/v3/parse parser does NOT
// expose as discrete fields. Operator decision: trust Sonarr for
// quality/source/languages, fall back to regex on the release title
// for codec / HDR variant / dub markers / explicit-sub markers.
type Extras struct {
	Codec    string   // "HEVC" | "x264" | "x265" | "AV1" | ""
	HDRFlags []string // any of "HDR10", "HDR10+", "DV", "HLG"
	Dub      string   // "MVO" | "DUB" | "Multi" | "VO" | "Original" | ""
	Subs     []string // "RUS", "ENG", "MULTI" — uppercased ISO-ish codes
}

var (
	// Codec markers — ordered: HEVC outranks x265/x264 if both appear.
	codecHEVC = regexp.MustCompile(`(?i)\bHEVC\b`)
	codecX265 = regexp.MustCompile(`(?i)(?:\bx265\b|h\.?265\b)`)
	codecX264 = regexp.MustCompile(`(?i)(?:\bx264\b|h\.?264\b)`)
	codecAV1  = regexp.MustCompile(`(?i)\bAV1\b`)

	// HDR markers — DV must NOT match e.g. "DVD" or "DVDRip".
	hdrHDR10Plus    = regexp.MustCompile(`(?i)\bHDR10\+`)
	hdrHDR10        = regexp.MustCompile(`(?i)\bHDR10\b`)
	hdrDolbyVision  = regexp.MustCompile(`(?i)\bDolby\s*Vision\b`)
	hdrDVStandalone = regexp.MustCompile(`(?i)(?:[\.\s\[\(\-,]|^)DV(?:[\s\]\)\-,\.]|$)`)
	hdrHLG          = regexp.MustCompile(`(?i)\bHLG\b`)

	// Dub markers — ordered by specificity.
	dubMVO      = regexp.MustCompile(`(?i)\bMVO\b`)
	dubDUB      = regexp.MustCompile(`(?i)\bDUB\b`)
	dubMulti    = regexp.MustCompile(`(?i)\bMulti(?:Audio|Dub)\b`)
	dubVO       = regexp.MustCompile(`(?i)\bVO\b`)
	dubOriginal = regexp.MustCompile(`(?i)\bOriginal\b`)

	// Subs markers — `Sub(s):RU`, `RUS sub`, `[RUS]`, `ENG sub`, etc. Conservative
	// — we only emit a sub when the string explicitly mentions it.
	subsRU    = regexp.MustCompile(`(?i)(?:\b(?:RU(?:S)?(?:\s*sub|subs?))|sub(?:s)?\s*:\s*ru(?:s)?\b|\brus\b)`)
	subsEN    = regexp.MustCompile(`(?i)(?:\b(?:EN(?:G)?(?:\s*sub|subs?))|sub(?:s)?\s*:\s*en(?:g)?\b|\beng\b)`)
	subsMulti = regexp.MustCompile(`(?i)\b(?:sub(?:s)?\s*:\s*multi|multi\s*sub(?:s)?)\b`)
)

// ExtractExtras runs the regex passes against the raw release title and
// returns a populated Extras. Conservative — never guesses. Always
// returns non-nil slices when empty (avoids nil-vs-empty JSON drift).
func ExtractExtras(title string) Extras {
	t := strings.TrimSpace(title)
	if t == "" {
		return Extras{HDRFlags: []string{}, Subs: []string{}}
	}
	out := Extras{HDRFlags: []string{}, Subs: []string{}}

	switch {
	case codecHEVC.MatchString(t):
		out.Codec = "HEVC"
	case codecX265.MatchString(t):
		out.Codec = "HEVC"
	case codecX264.MatchString(t):
		out.Codec = "H.264"
	case codecAV1.MatchString(t):
		out.Codec = "AV1"
	}

	if hdrHDR10Plus.MatchString(t) {
		out.HDRFlags = append(out.HDRFlags, "HDR10+")
	}
	if hdrHDR10.MatchString(t) && !hdrHDR10Plus.MatchString(t) {
		out.HDRFlags = append(out.HDRFlags, "HDR10")
	}
	if hdrDolbyVision.MatchString(t) || hdrDVStandalone.MatchString(t) {
		out.HDRFlags = append(out.HDRFlags, "DV")
	}
	if hdrHLG.MatchString(t) {
		out.HDRFlags = append(out.HDRFlags, "HLG")
	}

	switch {
	case dubMVO.MatchString(t):
		out.Dub = "MVO"
	case dubDUB.MatchString(t):
		out.Dub = "DUB"
	case dubMulti.MatchString(t):
		out.Dub = "Multi"
	case dubVO.MatchString(t):
		out.Dub = "VO"
	case dubOriginal.MatchString(t):
		out.Dub = "Original"
	}

	if subsRU.MatchString(t) {
		out.Subs = append(out.Subs, "RUS")
	}
	if subsEN.MatchString(t) {
		out.Subs = append(out.Subs, "ENG")
	}
	if subsMulti.MatchString(t) {
		out.Subs = append(out.Subs, "MULTI")
	}

	return out
}
