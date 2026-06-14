package qbit

import (
	"regexp"
	"strconv"
	"strings"
)

// seasonEpisodeRE matches the canonical SxxExx pattern that release
// titles use. Accepted shapes:
//   - S03E07         — 1–4 digit season, 1–4 digit episode
//   - s03e07         — lowercase
//   - S03.E07        — dot separator (rare but seen in wild)
//   - S03E07E08      — multi-episode same season; the trailing E
//     run is matched by the multi-episode regex below.
//
// We do NOT match:
//   - 1x07           — alternate numbering schema (low prevalence)
//   - Episode 7      — no season qualifier
//   - S03            — pack torrents intentionally parse as nil
//     (no episode hit → no season hit either)
//
// The (?i) flag handles upper/lowercase; \b at the start prevents
// false-positives inside longer tokens ("FANTASY3E07" must not match
// because there is no `S` followed by digits at a word boundary).
// We omit a trailing \b so that runs like "S03E07E08" still match
// the first SxxExx token — Go's RE2 engine treats `7E` as same-word,
// and the trailing \b would reject the match.
var seasonEpisodeRE = regexp.MustCompile(`(?i)\bS(\d{1,4})\.?E\d{1,4}`)

// ParseSeason inspects the torrent name and returns the season number
// shared by every matched SxxExx hit. Returns nil when:
//   - no SxxExx hit is present (pack torrents, malformed names)
//   - matches reference more than one distinct season (multi-season
//     compilations — silence is the correct answer)
//
// Caller owns the *int — ParseSeason allocates fresh on every call.
// Implementation detail: we keep the first-seen season as the
// "candidate" and bail to nil the moment we see a different one,
// so common single-season inputs short-circuit after the first match.
func ParseSeason(name string) *int {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil
	}
	matches := seasonEpisodeRE.FindAllStringSubmatch(name, -1)
	if len(matches) == 0 {
		return nil
	}
	seen := -1
	for _, m := range matches {
		// m[1] is the season capture group; strconv.Atoi cannot
		// fail because the regex constrains it to 1–4 digits.
		n, err := strconv.Atoi(m[1])
		if err != nil || n <= 0 {
			// Defence-in-depth: if Atoi somehow fails or yields
			// a non-positive season, treat the whole name as
			// ambiguous and return nil.
			return nil
		}
		if seen == -1 {
			seen = n
			continue
		}
		if seen != n {
			return nil
		}
	}
	if seen <= 0 {
		return nil
	}
	out := seen
	return &out
}
