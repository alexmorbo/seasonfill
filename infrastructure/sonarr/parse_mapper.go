package sonarr

import (
	"strconv"
	"strings"

	grab "github.com/alexmorbo/seasonfill/internal/grab/domain"
)

// ParseResult is the trimmed projection of Sonarr's ParseResource we
// keep on the wire. Internal to the sonarr package — domain wants a
// grab.Parsed, which MergeParse builds together with Extras.
type ParseResult struct {
	Quality      string
	Source       string
	Resolution   int
	Languages    []string
	ReleaseGroup string
}

// parseResultFromDTO drops a parseResourceDTO into a ParseResult.
// Tolerant of nil parsedEpisodeInfo (Sonarr returns that for
// un-recognised titles).
func parseResultFromDTO(d parseResourceDTO) ParseResult {
	out := ParseResult{Languages: []string{}}
	if d.ParsedEpisodeInfo == nil {
		return out
	}
	pi := d.ParsedEpisodeInfo
	out.ReleaseGroup = strings.TrimSpace(pi.ReleaseGroup)
	if pi.Quality != nil && pi.Quality.Quality != nil {
		out.Quality = strings.TrimSpace(pi.Quality.Quality.Name)
		out.Source = strings.TrimSpace(pi.Quality.Quality.Source)
		out.Resolution = pi.Quality.Quality.Resolution
	}
	if out.Resolution == 0 && out.Quality != "" {
		// Cheap fallback: pull a trailing 2160p / 1080p / 720p / 480p out
		// of the quality name. Sonarr almost always populates Resolution
		// directly; this catches operator-customised quality definitions.
		if r := extractResolutionFromName(out.Quality); r > 0 {
			out.Resolution = r
		}
	}
	for _, l := range pi.Languages {
		name := strings.TrimSpace(l.Name)
		if name == "" {
			continue
		}
		out.Languages = append(out.Languages, name)
	}
	return out
}

func extractResolutionFromName(q string) int {
	q = strings.ToLower(q)
	for _, candidate := range []string{"2160p", "1080p", "720p", "480p", "576p", "360p"} {
		if strings.Contains(q, candidate) {
			n, _ := strconv.Atoi(strings.TrimSuffix(candidate, "p"))
			return n
		}
	}
	return 0
}

// MergeParse fuses the Sonarr-authoritative ParseResult with the
// regex-derived Extras into a domain grab.Parsed. ParseResult wins on
// overlapping fields (Sonarr is canonical for quality/source/languages),
// Extras wins for codec / HDR / dub / subs (Sonarr doesn't expose
// these discretely). Always returns a non-nil-slices Parsed.
func MergeParse(p ParseResult, e Extras) grab.Parsed {
	out := grab.Parsed{
		Codec:        e.Codec,
		Source:       p.Source,
		Quality:      p.Quality,
		Resolution:   p.Resolution,
		HDRFlags:     normalisedStrings(e.HDRFlags),
		Dub:          e.Dub,
		Languages:    normalisedStrings(p.Languages),
		Subs:         normalisedStrings(e.Subs),
		ReleaseGroup: p.ReleaseGroup,
	}
	return out
}

func normalisedStrings(in []string) []string {
	if len(in) == 0 {
		return []string{}
	}
	out := make([]string, 0, len(in))
	seen := make(map[string]struct{}, len(in))
	for _, s := range in {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		if _, dup := seen[s]; dup {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	return out
}
